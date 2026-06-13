// Package k8s is the Kubernetes integration ported from the v1
// KubernetesClient (lib/KubernetesClient.js). It uses client-go's typed CoreV1
// client for the stability ConfigMap and the dynamic client for the
// RabbitmqCluster CRD (rabbitmq.com/v1beta1), both mutated via JSON Patch.
package k8s

import (
	"context"
	"encoding/json"
	"log"
	"strconv"

	"github.com/ferterahadi/rmq-vertical-scaler/internal/config"
	"github.com/ferterahadi/rmq-vertical-scaler/internal/scaling"

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
