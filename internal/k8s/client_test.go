package k8s

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/config"
	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/scaling"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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

// withDiscovery sets the fake API server's discovery resources.
func withDiscovery(core *corefake.Clientset, lists ...*metav1.APIResourceList) *corefake.Clientset {
	core.Fake.Resources = lists
	return core
}

func v1Resources(names ...string) *metav1.APIResourceList {
	rl := &metav1.APIResourceList{GroupVersion: "v1"}
	for _, n := range names {
		rl.APIResources = append(rl.APIResources, metav1.APIResource{Name: n})
	}
	return rl
}

func TestResizeSupportedTrue(t *testing.T) {
	core := withDiscovery(corefake.NewSimpleClientset(), v1Resources("pods", "pods/resize"))
	c := newTestClient(t, core, newDynamic())
	if !c.ResizeSupported() {
		t.Error("ResizeSupported = false, want true when pods/resize is in discovery")
	}
}

func TestResizeSupportedFalseWhenAbsent(t *testing.T) {
	core := withDiscovery(corefake.NewSimpleClientset(), v1Resources("pods", "pods/exec"))
	c := newTestClient(t, core, newDynamic())
	if c.ResizeSupported() {
		t.Error("ResizeSupported = true, want false without pods/resize")
	}
}

func TestResizeSupportedFalseOnDiscoveryError(t *testing.T) {
	// No v1 group registered at all -> discovery lookup errors -> false.
	c := newTestClient(t, corefake.NewSimpleClientset(), newDynamic())
	if c.ResizeSupported() {
		t.Error("ResizeSupported = true, want false on discovery error")
	}
}

func TestEffectiveScaleMode(t *testing.T) {
	cases := []struct {
		mode      string
		supported bool
		want      string
	}{
		{config.ScaleModeRolling, true, config.ScaleModeRolling},
		{config.ScaleModeRolling, false, config.ScaleModeRolling},
		{config.ScaleModeInPlace, true, config.ScaleModeInPlace},
		{config.ScaleModeInPlace, false, config.ScaleModeInPlace}, // forced, warn only
		{config.ScaleModeAuto, true, config.ScaleModeInPlace},
		{config.ScaleModeAuto, false, config.ScaleModeRolling},
	}
	for _, tc := range cases {
		core := corefake.NewSimpleClientset()
		if tc.supported {
			withDiscovery(core, v1Resources("pods", "pods/resize"))
		} else {
			withDiscovery(core, v1Resources("pods"))
		}
		cfg := testConfig()
		cfg.ScaleMode = tc.mode
		c := newClient(cfg, core, newDynamic(), log.New(io.Discard, "", 0))
		if got := c.EffectiveScaleMode(); got != tc.want {
			t.Errorf("mode=%s supported=%v: EffectiveScaleMode = %q, want %q",
				tc.mode, tc.supported, got, tc.want)
		}
	}
}

// rmqPod builds a running RabbitMQ pod as the cluster operator labels it.
func rmqPod(name, cluster, cpu, mem string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "prod",
			Labels: map[string]string{"app.kubernetes.io/name": cluster},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "rabbitmq",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(cpu),
					corev1.ResourceMemory: resource.MustParse(mem),
				},
			},
		}}},
	}
}

func TestResizePodsPatchesEveryPodViaResizeSubresource(t *testing.T) {
	core := corefake.NewSimpleClientset(
		rmqPod("rmq-server-0", "rmq", "330m", "1Gi"),
		rmqPod("rmq-server-1", "rmq", "330m", "1Gi"),
		rmqPod("other-app-0", "other", "100m", "128Mi"), // must be ignored
	)
	c := newTestClient(t, core, newDynamic())

	if err := c.ResizePods(context.Background(), "800m", "2Gi"); err != nil {
		t.Fatalf("ResizePods: %v", err)
	}

	resizePatches := 0
	for _, a := range core.Fake.Actions() {
		if p, ok := a.(k8stesting.PatchAction); ok && a.GetVerb() == "patch" {
			if a.GetResource().Resource != "pods" {
				continue
			}
			if p.GetSubresource() != "resize" {
				t.Errorf("pod patch used subresource %q, want resize", p.GetSubresource())
			}
			if n := p.GetName(); n != "rmq-server-0" && n != "rmq-server-1" {
				t.Errorf("patched unexpected pod %q", n)
			}
			resizePatches++
		}
	}
	if resizePatches != 2 {
		t.Errorf("resize patches = %d, want 2", resizePatches)
	}

	pod, err := core.CoreV1().Pods("prod").Get(context.Background(), "rmq-server-0", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod: %v", err)
	}
	req := pod.Spec.Containers[0].Resources.Requests
	if req.Cpu().String() != "800m" || req.Memory().String() != "2Gi" {
		t.Errorf("pod requests = %s/%s, want 800m/2Gi", req.Cpu(), req.Memory())
	}
}

func TestResizePodsReturnsErrorWhenListFails(t *testing.T) {
	core := corefake.NewSimpleClientset()
	core.Fake.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("list boom")
	})
	c := newTestClient(t, core, newDynamic())
	if err := c.ResizePods(context.Background(), "800m", "2Gi"); err == nil {
		t.Error("ResizePods = nil error, want list error")
	}
}

func TestResizePodsContinuesPastPerPodFailure(t *testing.T) {
	core := corefake.NewSimpleClientset(
		rmqPod("rmq-server-0", "rmq", "330m", "1Gi"),
		rmqPod("rmq-server-1", "rmq", "330m", "1Gi"),
	)
	core.Fake.PrependReactor("patch", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.(k8stesting.PatchAction).GetName() == "rmq-server-0" {
			return true, nil, errors.New("infeasible")
		}
		return false, nil, nil
	})
	c := newTestClient(t, core, newDynamic())

	err := c.ResizePods(context.Background(), "800m", "2Gi")
	if err == nil {
		t.Fatal("ResizePods = nil error, want aggregated per-pod error")
	}

	// The healthy pod must still have been resized.
	pod, gerr := core.CoreV1().Pods("prod").Get(context.Background(), "rmq-server-1", metav1.GetOptions{})
	if gerr != nil {
		t.Fatalf("get pod: %v", gerr)
	}
	if pod.Spec.Containers[0].Resources.Requests.Cpu().String() != "800m" {
		t.Error("rmq-server-1 was not resized after rmq-server-0 failed")
	}
}

func TestResizePodsNoPodsIsError(t *testing.T) {
	c := newTestClient(t, corefake.NewSimpleClientset(), newDynamic())
	if err := c.ResizePods(context.Background(), "800m", "2Gi"); err == nil {
		t.Error("ResizePods with zero matching pods = nil error, want error")
	}
}

func TestResizePodsDetectsInfeasible(t *testing.T) {
	pod := rmqPod("rmq-server-0", "rmq", "330m", "1Gi")
	pod.Status.Conditions = []corev1.PodCondition{{
		Type:   corev1.PodResizePending,
		Status: corev1.ConditionTrue,
		Reason: "Infeasible",
	}}
	c := newTestClient(t, corefake.NewSimpleClientset(pod), newDynamic())

	err := c.ResizePods(context.Background(), "8000m", "64Gi")
	if !errors.Is(err, scaling.ErrResizeInfeasible) {
		t.Errorf("err = %v, want ErrResizeInfeasible", err)
	}
}

func TestResizePodsDeferredIsNotInfeasible(t *testing.T) {
	pod := rmqPod("rmq-server-0", "rmq", "330m", "1Gi")
	pod.Status.Conditions = []corev1.PodCondition{{
		Type:   corev1.PodResizePending,
		Status: corev1.ConditionTrue,
		Reason: "Deferred", // node busy now, may fit later — not a hard failure
	}}
	c := newTestClient(t, corefake.NewSimpleClientset(pod), newDynamic())

	if err := c.ResizePods(context.Background(), "800m", "2Gi"); err != nil {
		t.Errorf("err = %v, want nil for Deferred", err)
	}
}

// updateStrategyType reads spec.override.statefulSet.spec.updateStrategy.type
// from the fake RabbitmqCluster.
func updateStrategyType(t *testing.T, dyn *dynamicfake.FakeDynamicClient) string {
	t.Helper()
	obj, err := dyn.Resource(rabbitmqGVR).Namespace("prod").Get(context.Background(), "rmq", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cluster: %v", err)
	}
	s, _, _ := unstructured.NestedString(obj.Object,
		"spec", "override", "statefulSet", "spec", "updateStrategy", "type")
	return s
}

func TestEnsureOnDeleteStrategyPatchesWhenAbsent(t *testing.T) {
	dyn := newDynamic(rabbitmqCluster("rmq", "prod", "330m"))
	c := newTestClient(t, corefake.NewSimpleClientset(), dyn)

	if err := c.EnsureOnDeleteStrategy(context.Background()); err != nil {
		t.Fatalf("EnsureOnDeleteStrategy: %v", err)
	}
	if got := updateStrategyType(t, dyn); got != "OnDelete" {
		t.Errorf("updateStrategy.type = %q, want OnDelete", got)
	}
}

func TestEnsureOnDeleteStrategyIdempotent(t *testing.T) {
	cluster := rabbitmqCluster("rmq", "prod", "330m")
	unstructured.SetNestedField(cluster.Object, "OnDelete",
		"spec", "override", "statefulSet", "spec", "updateStrategy", "type")
	dyn := newDynamic(cluster)
	c := newTestClient(t, corefake.NewSimpleClientset(), dyn)

	if err := c.EnsureOnDeleteStrategy(context.Background()); err != nil {
		t.Fatalf("EnsureOnDeleteStrategy: %v", err)
	}
	for _, a := range dyn.Fake.Actions() {
		if a.GetVerb() == "patch" {
			t.Error("patched the cluster although OnDelete was already set")
		}
	}
}

func TestEnsureOnDeleteStrategyErrorOnMissingCluster(t *testing.T) {
	c := newTestClient(t, corefake.NewSimpleClientset(), newDynamic())
	if err := c.EnsureOnDeleteStrategy(context.Background()); err == nil {
		t.Error("EnsureOnDeleteStrategy = nil, want error when cluster is missing")
	}
}

func TestSetRollingUpdateStrategy(t *testing.T) {
	cluster := rabbitmqCluster("rmq", "prod", "330m")
	unstructured.SetNestedField(cluster.Object, "OnDelete",
		"spec", "override", "statefulSet", "spec", "updateStrategy", "type")
	dyn := newDynamic(cluster)
	c := newTestClient(t, corefake.NewSimpleClientset(), dyn)

	if err := c.SetRollingUpdateStrategy(context.Background()); err != nil {
		t.Fatalf("SetRollingUpdateStrategy: %v", err)
	}
	if got := updateStrategyType(t, dyn); got != "RollingUpdate" {
		t.Errorf("updateStrategy.type = %q, want RollingUpdate", got)
	}
}

// --- watermark re-signal ---

type execCall struct {
	pod, container string
	cmd            []string
}

func withExecRecorder(c *Client) *[]execCall {
	calls := &[]execCall{}
	c.execFn = func(_ context.Context, _, pod, container string, cmd []string) error {
		*calls = append(*calls, execCall{pod, container, cmd})
		return nil
	}
	return calls
}

func TestResignalWatermarkExecsInEveryPod(t *testing.T) {
	core := corefake.NewSimpleClientset(
		rmqPod("rmq-server-0", "rmq", "800m", "2Gi"),
		rmqPod("rmq-server-1", "rmq", "800m", "2Gi"),
	)
	c := newTestClient(t, core, newDynamic())
	calls := withExecRecorder(c)

	c.ResignalWatermark(context.Background(), "4Gi")

	if len(*calls) != 2 {
		t.Fatalf("exec calls = %d, want 2", len(*calls))
	}
	// 40% of 4Gi = 1717986918 bytes.
	want := []string{"rabbitmqctl", "set_vm_memory_high_watermark", "absolute", "1717986918"}
	for _, call := range *calls {
		if call.container != "rabbitmq" {
			t.Errorf("container = %q, want rabbitmq", call.container)
		}
		if len(call.cmd) != len(want) {
			t.Fatalf("cmd = %v, want %v", call.cmd, want)
		}
		for i := range want {
			if call.cmd[i] != want[i] {
				t.Errorf("cmd = %v, want %v", call.cmd, want)
				break
			}
		}
	}
}

func TestResignalWatermarkExecErrorIsBestEffort(t *testing.T) {
	core := corefake.NewSimpleClientset(rmqPod("rmq-server-0", "rmq", "800m", "2Gi"))
	c := newTestClient(t, core, newDynamic())
	c.execFn = func(context.Context, string, string, string, []string) error {
		return errors.New("exec denied")
	}
	// Must not panic and must not propagate: watermark is best-effort.
	c.ResignalWatermark(context.Background(), "4Gi")
}

func TestResignalWatermarkBadQuantityIsBestEffort(t *testing.T) {
	c := newTestClient(t, corefake.NewSimpleClientset(), newDynamic())
	calls := withExecRecorder(c)
	c.ResignalWatermark(context.Background(), "not-a-quantity")
	if len(*calls) != 0 {
		t.Errorf("exec calls = %d, want 0 for unparseable memory", len(*calls))
	}
}

func TestResignalWatermarkListErrorIsBestEffort(t *testing.T) {
	core := corefake.NewSimpleClientset()
	core.Fake.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("list boom")
	})
	c := newTestClient(t, core, newDynamic())
	c.ResignalWatermark(context.Background(), "4Gi")
}

func TestCheckResizeFeasibleGetErrorIsTolerated(t *testing.T) {
	core := corefake.NewSimpleClientset(rmqPod("rmq-server-0", "rmq", "330m", "1Gi"))
	core.Fake.PrependReactor("get", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("get boom")
	})
	c := newTestClient(t, core, newDynamic())
	// Patch is accepted; the post-patch feasibility read failing must not fail the action.
	if err := c.ResizePods(context.Background(), "800m", "2Gi"); err != nil {
		t.Errorf("err = %v, want nil when the feasibility re-read fails", err)
	}
}

func TestResignalWatermarkNilExecIsBestEffort(t *testing.T) {
	core := corefake.NewSimpleClientset(rmqPod("rmq-server-0", "rmq", "800m", "2Gi"))
	c := newTestClient(t, core, newDynamic()) // no execFn wired
	c.ResignalWatermark(context.Background(), "4Gi")
}

func TestSetUpdateStrategyPatchErrorPropagates(t *testing.T) {
	dyn := newDynamic(rabbitmqCluster("rmq", "prod", "330m"))
	dyn.PrependReactor("patch", "rabbitmqclusters", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("patch boom")
	})
	c := newTestClient(t, corefake.NewSimpleClientset(), dyn)
	if err := c.EnsureOnDeleteStrategy(context.Background()); err == nil {
		t.Error("EnsureOnDeleteStrategy = nil, want error when the patch fails")
	}
}
