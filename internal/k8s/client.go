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
	"io"
	"log"
	"strconv"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/config"
	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/scaling"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
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

	// execFn runs a command inside a pod's container. New() wires the real
	// SPDY executor; tests inject a recorder. nil means exec is unavailable.
	execFn func(ctx context.Context, namespace, pod, container string, cmd []string) error
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
	c := newClient(cfg, core, dyn, logger)
	c.execFn = func(ctx context.Context, namespace, pod, container string, cmd []string) error {
		req := core.CoreV1().RESTClient().Post().
			Resource("pods").Namespace(namespace).Name(pod).SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Container: container,
				Command:   cmd,
				Stdout:    true,
				Stderr:    true,
			}, kscheme.ParameterCodec)
		exec, err := remotecommand.NewSPDYExecutor(restCfg, "POST", req.URL())
		if err != nil {
			return err
		}
		return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
	}
	return c, nil
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

// EffectiveScaleMode resolves the configured SCALE_MODE against what the
// cluster actually supports: auto picks inplace when pods/resize exists and
// rolling otherwise; an explicit inplace is honoured even without support (the
// resize patches will fail visibly) but logged.
func (c *Client) EffectiveScaleMode() string {
	switch c.cfg.ScaleMode {
	case config.ScaleModeRolling:
		return config.ScaleModeRolling
	case config.ScaleModeInPlace:
		if !c.ResizeSupported() {
			c.logger.Println("⚠️  SCALE_MODE=inplace but the cluster does not advertise pods/resize; resizes will likely fail")
		}
		return config.ScaleModeInPlace
	default: // auto
		if c.ResizeSupported() {
			c.logger.Println("🔧 Scale mode: inplace (pods/resize supported)")
			return config.ScaleModeInPlace
		}
		c.logger.Println("🔧 Scale mode: rolling (pods/resize not supported)")
		return config.ScaleModeRolling
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
			errs = append(errs, fmt.Errorf("pod %s: %w", pod.Name, err))
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

// ResignalWatermark best-effort resets RabbitMQ's absolute memory high
// watermark to 40% (RabbitMQ's default relative watermark) of the new memory
// request in every cluster pod, via `rabbitmqctl set_vm_memory_high_watermark
// absolute <bytes>`. RabbitMQ reads total memory at boot, so a live resize is
// invisible to it until re-signalled. Runtime-only by design: a restarted pod
// re-reads its (already updated) limits. Never fails the scaling action —
// every problem is logged and swallowed.
func (c *Client) ResignalWatermark(ctx context.Context, memory string) {
	q, err := resource.ParseQuantity(memory)
	if err != nil {
		c.logger.Printf("Watermark re-signal skipped: bad memory quantity %q: %v", memory, err)
		return
	}
	watermark := strconv.FormatInt(q.Value()*4/10, 10)

	pods, err := c.core.CoreV1().Pods(c.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=" + c.cfg.RMQServiceName,
	})
	if err != nil {
		c.logger.Printf("Watermark re-signal skipped: listing pods: %v", err)
		return
	}
	if c.execFn == nil {
		c.logger.Println("Watermark re-signal skipped: exec unavailable")
		return
	}

	cmd := []string{"rabbitmqctl", "set_vm_memory_high_watermark", "absolute", watermark}
	for _, pod := range pods.Items {
		if err := c.execFn(ctx, c.cfg.Namespace, pod.Name, "rabbitmq", cmd); err != nil {
			c.logger.Printf("Watermark re-signal failed for pod %s: %v", pod.Name, err)
			continue
		}
		c.logger.Printf("💧 Watermark re-signalled for pod %s: %s bytes", pod.Name, watermark)
	}
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
