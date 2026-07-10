// Package k8s is the Kubernetes integration ported from the v1
// KubernetesClient (lib/KubernetesClient.js). It uses client-go's typed CoreV1
// client for the stability ConfigMap and the dynamic client for the
// RabbitmqCluster CRD (rabbitmq.com/v1beta1), both mutated via JSON Patch.
package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/config"
	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/scaling"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// rabbitmqGVR identifies the RabbitMQ Cluster Operator's custom resource.
var rabbitmqGVR = schema.GroupVersionResource{
	Group:    "rabbitmq.com",
	Version:  "v1beta1",
	Resource: "rabbitmqclusters",
}

// Client wraps the typed and dynamic Kubernetes clients for one namespace.
type Client struct {
	cfg     *config.Config
	core    kubernetes.Interface
	dynamic dynamic.Interface
	logger  *log.Logger
}

// New builds a Client from the in-cluster service-account config (v1's
// loadFromCluster). It only runs inside a pod.
func New(cfg *config.Config, logger *log.Logger) (*Client, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	core, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}
	return newClient(cfg, core, dyn, logger), nil
}

// newClient assembles a Client from already-built clients. New() uses it with
// real in-cluster clients; tests use it with fakes.
func newClient(cfg *config.Config, core kubernetes.Interface, dyn dynamic.Interface, logger *log.Logger) *Client {
	if logger == nil {
		logger = log.Default()
	}
	return &Client{cfg: cfg, core: core, dynamic: dyn, logger: logger}
}

// ResizeSupported reports whether the API server exposes the pods/resize
// subresource (InPlacePodVerticalScaling, on by default since Kubernetes 1.33).
// Discovery errors count as unsupported.
func (c *Client) ResizeSupported() bool {
	rl, err := c.core.Discovery().ServerResourcesForGroupVersion("v1")
	if err != nil {
		c.logger.Printf("Error discovering core/v1 resources: %v", err)
		return false
	}
	for _, r := range rl.APIResources {
		if r.Name == "pods/resize" {
			return true
		}
	}
	return false
}

// CheckResizePermissions asks the API server, via SelfSubjectAccessReview,
// whether this pod's service account may perform the in-place resize
// operations: patch on the pods/resize subresource and list on pods, both in
// the scaler's namespace. It returns the human-readable RBAC descriptions of
// any verbs that are denied (empty slice = fully permitted). This tests
// *permission*, complementing ResizeSupported which only tests API capability.
func (c *Client) CheckResizePermissions(ctx context.Context) ([]string, error) {
	checks := []struct {
		desc        string
		resource    string
		subresource string
		verb        string
	}{
		{"pods/resize (patch)", "pods", "resize", "patch"},
		{"pods (list)", "pods", "", "list"},
	}
	var missing []string
	for _, chk := range checks {
		review := &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace:   c.cfg.Namespace,
					Verb:        chk.verb,
					Group:       "",
					Resource:    chk.resource,
					Subresource: chk.subresource,
				},
			},
		}
		resp, err := c.core.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("SelfSubjectAccessReview for %s: %w", chk.desc, err)
		}
		if !resp.Status.Allowed {
			missing = append(missing, chk.desc)
		}
	}
	return missing, nil
}

// EffectiveScaleMode resolves the configured SCALE_MODE against what the
// cluster supports (API discovery) AND what this service account is permitted
// to do (SelfSubjectAccessReview):
//   - rolling: honoured as-is, no checks.
//   - inplace (explicit): the operator asserted the capability, so a missing
//     permission is a misconfiguration — return an error naming the exact RBAC
//     so startup fails fast rather than 403-ing silently at the first resize.
//   - auto: pick inplace only when pods/resize is both supported and permitted;
//     otherwise degrade to rolling with a loud warning naming what's missing.
//
// It returns an error only in the explicit-inplace-denied case; all other
// paths resolve to a usable mode.
func (c *Client) EffectiveScaleMode(ctx context.Context) (string, error) {
	switch c.cfg.ScaleMode {
	case config.ScaleModeRolling:
		return config.ScaleModeRolling, nil
	case config.ScaleModeInPlace:
		if !c.ResizeSupported() {
			c.logger.Println("⚠️  SCALE_MODE=inplace but the cluster does not advertise pods/resize; resizes will likely fail")
		}
		missing, err := c.CheckResizePermissions(ctx)
		if err != nil {
			return "", fmt.Errorf("SCALE_MODE=inplace: could not verify in-place resize permissions: %w", err)
		}
		if len(missing) > 0 {
			return "", fmt.Errorf("SCALE_MODE=inplace but the scaler's service account lacks required RBAC: %s — grant these verbs (see the chart's rbac.yaml) or set SCALE_MODE=auto to degrade to rolling",
				strings.Join(missing, ", "))
		}
		c.logger.Println("🔧 Scale mode: inplace (pods/resize permitted)")
		return config.ScaleModeInPlace, nil
	default: // auto
		if !c.ResizeSupported() {
			c.logger.Println("🔧 Scale mode: rolling (pods/resize not supported)")
			return config.ScaleModeRolling, nil
		}
		missing, err := c.CheckResizePermissions(ctx)
		if err != nil {
			c.logger.Printf("⚠️  Scale mode: rolling — could not verify pods/resize permissions (%v)", err)
			return config.ScaleModeRolling, nil
		}
		if len(missing) > 0 {
			c.logger.Printf("⚠️  Scale mode: rolling — pods/resize is supported but the scaler lacks RBAC: %s (grant these for in-place resize)",
				strings.Join(missing, ", "))
			return config.ScaleModeRolling, nil
		}
		c.logger.Println("🔧 Scale mode: inplace (pods/resize permitted)")
		return config.ScaleModeInPlace, nil
	}
}

// GetCurrentProfile reads the RabbitmqCluster's spec.resources.requests.cpu and
// reverse-maps it to a profile name. On any error or unknown CPU it returns
// "UNKNOWN" (parity with v1).
func (c *Client) GetCurrentProfile(ctx context.Context) string {
	obj, err := c.dynamic.Resource(rabbitmqGVR).Namespace(c.cfg.Namespace).
		Get(ctx, c.cfg.RMQServiceName, metav1.GetOptions{})
	if err != nil {
		c.logger.Printf("Error getting current profile: %v", err)
		return "UNKNOWN"
	}

	cpu, found, err := unstructured.NestedString(obj.Object, "spec", "resources", "requests", "cpu")
	if err != nil || !found {
		cpu = "0" // v1 defaulted the missing value to '0'
	}
	if profile, ok := c.cfg.CPUToProfile[cpu]; ok {
		return profile
	}
	return "UNKNOWN"
}

// GetStabilityState reads the debounce tracking from the ConfigMap. On error it
// returns the zero state ("", 0), matching v1.
func (c *Client) GetStabilityState(ctx context.Context) scaling.StabilityState {
	c.logger.Printf("🔍 Getting stability state from ConfigMap: %s in namespace: %s",
		c.cfg.ConfigMapName, c.cfg.Namespace)

	cm, err := c.core.CoreV1().ConfigMaps(c.cfg.Namespace).
		Get(ctx, c.cfg.ConfigMapName, metav1.GetOptions{})
	if err != nil {
		c.logger.Printf("Error getting stability state: %v", err)
		return scaling.StabilityState{}
	}

	since, _ := strconv.ParseInt(cm.Data["stable_since"], 10, 64) // "" -> 0
	return scaling.StabilityState{
		StableProfile: cm.Data["stable_profile"],
		StableSince:   since,
	}
}

// UpdateStabilityTracking JSON-patches the ConfigMap's stable_profile and
// stable_since. now is unix seconds (injected by the caller). Errors are logged
// and swallowed, matching v1.
func (c *Client) UpdateStabilityTracking(ctx context.Context, profile string, now int64) {
	patch := []map[string]any{
		{"op": "replace", "path": "/data/stable_profile", "value": profile},
		{"op": "replace", "path": "/data/stable_since", "value": strconv.FormatInt(now, 10)},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		c.logger.Printf("Error updating stability tracking: %v", err)
		return
	}
	if _, err := c.core.CoreV1().ConfigMaps(c.cfg.Namespace).
		Patch(ctx, c.cfg.ConfigMapName, types.JSONPatchType, body, metav1.PatchOptions{}); err != nil {
		c.logger.Printf("Error updating stability tracking: %v", err)
		return
	}
	c.logger.Printf("📝 Updated stability tracking: %s since %d", profile, now)
}

// ResizePods patches every pod of the RabbitmqCluster in place through the
// pods/resize subresource (no restart). Pods are matched by the operator's
// app.kubernetes.io/name label. A per-pod failure doesn't stop the others;
// all failures are aggregated into the returned error. Zero matching pods is
// an error — it means the label selector or cluster name is wrong.
func (c *Client) ResizePods(ctx context.Context, cpu, memory string) error {
	pods, err := c.core.CoreV1().Pods(c.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=" + c.cfg.RMQServiceName,
	})
	if err != nil {
		// A 403 here is missing `pods (list)` RBAC — tag it so auto mode can
		// degrade to rolling instead of hard-failing (defence-in-depth behind
		// the startup preflight).
		if apierrors.IsForbidden(err) {
			return fmt.Errorf("listing pods for %s: %w: %v", c.cfg.RMQServiceName, scaling.ErrResizePermission, err)
		}
		return fmt.Errorf("listing pods for %s: %w", c.cfg.RMQServiceName, err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no pods found with app.kubernetes.io/name=%s in %s",
			c.cfg.RMQServiceName, c.cfg.Namespace)
	}

	patch := map[string]any{
		"spec": map[string]any{
			"containers": []map[string]any{{
				"name": "rabbitmq",
				"resources": map[string]any{
					"requests": map[string]string{"cpu": cpu, "memory": memory},
				},
			}},
		},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}

	var errs []error
	for _, pod := range pods.Items {
		if _, err := c.core.CoreV1().Pods(c.cfg.Namespace).Patch(ctx, pod.Name,
			types.StrategicMergePatchType, body, metav1.PatchOptions{}, "resize"); err != nil {
			c.logger.Printf("❌ In-place resize failed for pod %s: %v", pod.Name, err)
			// Newer API servers reject an unschedulable resize synchronously
			// ("node didn't have enough allocatable resources") instead of the
			// async kubelet Infeasible condition — same semantics, same error.
			switch {
			case apierrors.IsForbidden(err) && strings.Contains(err.Error(), "allocatable"):
				errs = append(errs, fmt.Errorf("pod %s: %w: %v", pod.Name, scaling.ErrResizeInfeasible, err))
			case apierrors.IsForbidden(err):
				// 403 without an allocatable verdict = missing pods/resize RBAC.
				errs = append(errs, fmt.Errorf("pod %s: %w: %v", pod.Name, scaling.ErrResizePermission, err))
			default:
				errs = append(errs, fmt.Errorf("pod %s: %w", pod.Name, err))
			}
			continue
		}
		if err := c.checkResizeFeasible(ctx, pod.Name); err != nil {
			errs = append(errs, err)
			continue
		}
		c.logger.Printf("📐 Resized pod %s in place: CPU=%s, Memory=%s", pod.Name, cpu, memory)
	}
	return errors.Join(errs...)
}

// checkResizeFeasible re-reads a pod after a resize patch and surfaces a
// kubelet Infeasible verdict as ErrResizeInfeasible. Deferred (may fit later)
// and an absent condition (kubelet hasn't reacted yet) are not failures.
func (c *Client) checkResizeFeasible(ctx context.Context, name string) error {
	pod, err := c.core.CoreV1().Pods(c.cfg.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		c.logger.Printf("Error re-reading pod %s after resize: %v", name, err)
		return nil // patch was accepted; don't fail the action on a read error
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodResizePending && cond.Reason == "Infeasible" {
			c.logger.Printf("❌ Resize infeasible for pod %s: %s", name, cond.Message)
			return fmt.Errorf("pod %s: %w", name, scaling.ErrResizeInfeasible)
		}
	}
	return nil
}

// EnsureOnDeleteStrategy makes sure the RabbitmqCluster overrides its
// StatefulSet updateStrategy to OnDelete, so CR patches update the pod
// template without rolling pods (the scaler resizes them in place instead).
// Idempotent: it only patches when the override is not already set.
func (c *Client) EnsureOnDeleteStrategy(ctx context.Context) error {
	return c.setUpdateStrategy(ctx, "OnDelete")
}

// SetRollingUpdateStrategy reverts the StatefulSet updateStrategy override to
// RollingUpdate, so the next CR patch rolls pods (the fallback path when an
// in-place resize is infeasible).
func (c *Client) SetRollingUpdateStrategy(ctx context.Context) error {
	return c.setUpdateStrategy(ctx, "RollingUpdate")
}

func (c *Client) setUpdateStrategy(ctx context.Context, strategy string) error {
	obj, err := c.dynamic.Resource(rabbitmqGVR).Namespace(c.cfg.Namespace).
		Get(ctx, c.cfg.RMQServiceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting RabbitmqCluster %s: %w", c.cfg.RMQServiceName, err)
	}
	current, _, _ := unstructured.NestedString(obj.Object,
		"spec", "override", "statefulSet", "spec", "updateStrategy", "type")
	if current == strategy {
		return nil
	}

	patch := map[string]any{
		"spec": map[string]any{
			"override": map[string]any{
				"statefulSet": map[string]any{
					"spec": map[string]any{
						"updateStrategy": map[string]any{"type": strategy},
					},
				},
			},
		},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	if _, err := c.dynamic.Resource(rabbitmqGVR).Namespace(c.cfg.Namespace).
		Patch(ctx, c.cfg.RMQServiceName, types.MergePatchType, body, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("setting updateStrategy %s: %w", strategy, err)
	}
	c.logger.Printf("🔧 StatefulSet updateStrategy override set to %s", strategy)
	return nil
}

// ApplyPatch JSON-patches the RabbitmqCluster's CPU and memory requests.
func (c *Client) ApplyPatch(ctx context.Context, cpu, memory string) error {
	patch := []map[string]any{
		{"op": "replace", "path": "/spec/resources/requests/cpu", "value": cpu},
		{"op": "replace", "path": "/spec/resources/requests/memory", "value": memory},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	if _, err := c.dynamic.Resource(rabbitmqGVR).Namespace(c.cfg.Namespace).
		Patch(ctx, c.cfg.RMQServiceName, types.JSONPatchType, body, metav1.PatchOptions{}); err != nil {
		c.logger.Printf("❌ Scaling failed: %v", err)
		return err
	}
	c.logger.Println("✅ Scaling completed successfully")
	return nil
}
