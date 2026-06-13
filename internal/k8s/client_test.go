package k8s

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"

	"github.com/ferterahadi/rmq-vertical-scaler/internal/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	corefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func testConfig() *config.Config {
	return &config.Config{
		Namespace:      "prod",
		RMQServiceName: "rmq",
		ConfigMapName:  "rmq-config",
		ProfileNames:   []string{"LOW", "MEDIUM", "HIGH", "CRITICAL"},
		CPUToProfile: map[string]string{
			"330m": "LOW", "800m": "MEDIUM", "1600m": "HIGH", "2400m": "CRITICAL",
		},
	}
}

// rabbitmqCluster builds an unstructured RabbitmqCluster with the given CPU req.
func rabbitmqCluster(name, namespace, cpu string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "rabbitmq.com/v1beta1",
		"kind":       "RabbitmqCluster",
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"spec": map[string]any{
			"resources": map[string]any{
				"requests": map[string]any{"cpu": cpu, "memory": "2Gi"},
			},
		},
	}}
}

func newDynamic(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		rabbitmqGVR: "RabbitmqClusterList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

func newTestClient(t *testing.T, core *corefake.Clientset, dyn *dynamicfake.FakeDynamicClient) *Client {
	t.Helper()
	return newClient(testConfig(), core, dyn, log.New(io.Discard, "", 0))
}

func TestGetCurrentProfile(t *testing.T) {
	c := newTestClient(t, corefake.NewSimpleClientset(), newDynamic(rabbitmqCluster("rmq", "prod", "1600m")))
	if got := c.GetCurrentProfile(context.Background()); got != "HIGH" {
		t.Errorf("GetCurrentProfile = %q, want HIGH", got)
	}
}

func TestGetCurrentProfileUnknownCPU(t *testing.T) {
	c := newTestClient(t, corefake.NewSimpleClientset(), newDynamic(rabbitmqCluster("rmq", "prod", "999m")))
	if got := c.GetCurrentProfile(context.Background()); got != "UNKNOWN" {
		t.Errorf("GetCurrentProfile = %q, want UNKNOWN for unmapped CPU", got)
	}
}

func TestGetCurrentProfileMissingCluster(t *testing.T) {
	c := newTestClient(t, corefake.NewSimpleClientset(), newDynamic()) // no cluster object
	if got := c.GetCurrentProfile(context.Background()); got != "UNKNOWN" {
		t.Errorf("GetCurrentProfile = %q, want UNKNOWN on get error", got)
	}
}

func TestApplyPatchUpdatesClusterCPUMemory(t *testing.T) {
	dyn := newDynamic(rabbitmqCluster("rmq", "prod", "330m"))
	c := newTestClient(t, corefake.NewSimpleClientset(), dyn)

	if err := c.ApplyPatch(context.Background(), "2400m", "8Gi"); err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}

	obj, err := dyn.Resource(rabbitmqGVR).Namespace("prod").Get(context.Background(), "rmq", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get after patch: %v", err)
	}
	cpu, _, _ := unstructured.NestedString(obj.Object, "spec", "resources", "requests", "cpu")
	mem, _, _ := unstructured.NestedString(obj.Object, "spec", "resources", "requests", "memory")
	if cpu != "2400m" || mem != "8Gi" {
		t.Errorf("after patch cpu/mem = %q/%q, want 2400m/8Gi", cpu, mem)
	}
}

func TestGetStabilityState(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "rmq-config", Namespace: "prod"},
		Data:       map[string]string{"stable_profile": "HIGH", "stable_since": "1717171717"},
	}
	c := newTestClient(t, corefake.NewSimpleClientset(cm), newDynamic())

	st := c.GetStabilityState(context.Background())
	if st.StableProfile != "HIGH" || st.StableSince != 1717171717 {
		t.Errorf("state = %+v, want {HIGH 1717171717}", st)
	}
}

func TestGetStabilityStateMissingConfigMap(t *testing.T) {
	c := newTestClient(t, corefake.NewSimpleClientset(), newDynamic()) // no ConfigMap
	st := c.GetStabilityState(context.Background())
	if st.StableProfile != "" || st.StableSince != 0 {
		t.Errorf("state = %+v, want zero on get error", st)
	}
}

func TestUpdateStabilityTracking(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "rmq-config", Namespace: "prod"},
		Data:       map[string]string{"stable_profile": "LOW", "stable_since": "0"},
	}
	core := corefake.NewSimpleClientset(cm)
	c := newTestClient(t, core, newDynamic())

	c.UpdateStabilityTracking(context.Background(), "MEDIUM", 1234567890)

	got, err := core.CoreV1().ConfigMaps("prod").Get(context.Background(), "rmq-config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get after patch: %v", err)
	}
	if got.Data["stable_profile"] != "MEDIUM" || got.Data["stable_since"] != "1234567890" {
		t.Errorf("after patch data = %v, want MEDIUM/1234567890", got.Data)
	}
}

func TestNewClientNilLoggerDefaults(t *testing.T) {
	// Covers newClient's `logger == nil` branch.
	c := newClient(testConfig(), corefake.NewSimpleClientset(), newDynamic(), nil)
	if c == nil || c.logger == nil {
		t.Fatal("newClient returned nil client/logger")
	}
}

func TestGetCurrentProfileMissingCPUField(t *testing.T) {
	// Cluster exists but has no spec.resources.requests.cpu -> defaults to "0" -> UNKNOWN.
	cluster := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "rabbitmq.com/v1beta1",
		"kind":       "RabbitmqCluster",
		"metadata":   map[string]any{"name": "rmq", "namespace": "prod"},
		"spec":       map[string]any{},
	}}
	c := newTestClient(t, corefake.NewSimpleClientset(), newDynamic(cluster))
	if got := c.GetCurrentProfile(context.Background()); got != "UNKNOWN" {
		t.Errorf("GetCurrentProfile = %q, want UNKNOWN when cpu field absent", got)
	}
}

func TestUpdateStabilityTrackingPatchErrorIsSwallowed(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "rmq-config", Namespace: "prod"},
		Data:       map[string]string{"stable_profile": "LOW", "stable_since": "0"},
	}
	core := corefake.NewSimpleClientset(cm)
	core.PrependReactor("patch", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("patch boom")
	})
	c := newTestClient(t, core, newDynamic())
	// Must not panic; the error is logged and swallowed (v1 parity).
	c.UpdateStabilityTracking(context.Background(), "MEDIUM", 1)
}

func TestApplyPatchReturnsErrorOnFailure(t *testing.T) {
	dyn := newDynamic(rabbitmqCluster("rmq", "prod", "330m"))
	dyn.PrependReactor("patch", "rabbitmqclusters", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("patch boom")
	})
	c := newTestClient(t, corefake.NewSimpleClientset(), dyn)
	if err := c.ApplyPatch(context.Background(), "2400m", "8Gi"); err == nil {
		t.Error("ApplyPatch = nil, want error when the patch fails")
	}
}
