package v2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/policy"
	appsv1 "k8s.io/api/apps/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func fakeBackend(t *testing.T, at time.Time) (*Backend, *kubefake.Clientset, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	p := testPolicy(t)
	p.Target.Namespace = "test"
	p.Target.ServiceName = "rabbit"
	p.Behavior.ScaleUp.StabilizationSeconds = 0
	controlled := true
	replicas := int32(3)
	r := p.Profiles["LOW"].Resources()
	owner := func(kind, name, uid string) []metav1.OwnerReference {
		return []metav1.OwnerReference{{Kind: kind, Name: name, UID: types.UID(uid), Controller: &controlled}}
	}
	st := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "rabbit-server", Namespace: "test", UID: "sts", ResourceVersion: "1", OwnerReferences: owner("RabbitmqCluster", "rabbit", "cluster")}, Spec: appsv1.StatefulSetSpec{Replicas: &replicas, UpdateStrategy: appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType}, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "rabbitmq", Resources: r}}}}}}
	objects := []runtime.Object{st}
	metrics := []runtime.Object{}
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("rabbit-server-%d", i)
		res := r.DeepCopy()
		objects = append(objects, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test", UID: types.UID(name), Generation: 1, ResourceVersion: "1", OwnerReferences: owner("StatefulSet", st.Name, "sts")}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "rabbitmq", Resources: r}}}, Status: corev1.PodStatus{QOSClass: corev1.PodQOSBurstable, ObservedGeneration: 1, ContainerStatuses: []corev1.ContainerStatus{{Name: "rabbitmq", ContainerID: name, Ready: true, Resources: res, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(at.Add(-time.Hour))}}}}}})
		metrics = append(metrics, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "metrics.k8s.io/v1beta1", "kind": "PodMetrics", "metadata": map[string]any{"name": name, "namespace": "test"}, "timestamp": at.Format(time.RFC3339), "window": "10s", "containers": []any{map[string]any{"name": "rabbitmq", "usage": map[string]any{"cpu": "100m", "memory": "100Mi"}}}}})
	}
	core := kubefake.NewSimpleClientset(objects...)
	core.Resources = []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "pods/resize"}}}}
	core.PrependReactor("create", "selfsubjectaccessreviews", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, &authorizationv1.SelfSubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	})
	raw, _ := json.Marshal(r)
	var rr map[string]any
	json.Unmarshal(raw, &rr)
	cr := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "rabbitmq.com/v1beta1", "kind": "RabbitmqCluster", "metadata": map[string]any{"name": "rabbit", "namespace": "test", "uid": "cluster", "resourceVersion": "1"}, "spec": map[string]any{"replicas": int64(3), "resources": rr, "override": map[string]any{"statefulSet": map[string]any{"spec": map[string]any{"updateStrategy": map[string]any{"type": "OnDelete"}}}}}}}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{clusterGVR: "RabbitmqClusterList", metricsGVR: "PodMetricsList"}, cr)
	for _, metric := range metrics {
		if _, err := dyn.Resource(metricsGVR).Namespace("test").Create(context.Background(), metric.(*unstructured.Unstructured), metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	return &Backend{Policy: p, Core: core, Dynamic: dyn}, core, dyn
}
func quietLog() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }
func assertNoBrokerWrites(t *testing.T, core *kubefake.Clientset, dyn *dynamicfake.FakeDynamicClient) {
	t.Helper()
	for _, a := range append(core.Actions(), dyn.Actions()...) {
		if a.GetVerb() == "patch" {
			t.Fatalf("unexpected broker mutation: %+v", a)
		}
	}
}
func TestOffWritesOnlyControllerStatus(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, dyn := fakeBackend(t, at)
	c := NewController(b, "status", quietLog(), at)
	if err := c.Tick(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	assertNoBrokerWrites(t, core, dyn)
	cm, err := core.CoreV1().ConfigMaps("test").Get(context.Background(), "status", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var state Status
	json.Unmarshal([]byte(cm.Data["status.json"]), &state)
	if state.Phase != "RecommendationOnly" || state.Decision.Recommended != 1 {
		t.Fatalf("Off lost recommendation: %+v", state.Decision)
	}
}
func TestUnsupportedMemoryLimitBlocksBeforeStrategyAndResources(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, dyn := fakeBackend(t, at)
	b.Policy.Update.Mode = "Auto"
	high := b.Policy.Profiles["HIGH"]
	high.Limits.Memory = "1536Mi"
	b.Policy.Profiles["HIGH"] = high
	c := NewController(b, "status", quietLog(), at)
	if err := c.Tick(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	assertNoBrokerWrites(t, core, dyn)
	cm, _ := core.CoreV1().ConfigMaps("test").Get(context.Background(), "status", metav1.GetOptions{})
	var state Status
	json.Unmarshal([]byte(cm.Data["status.json"]), &state)
	if state.Reason != "MemoryLimitRequiresBrokerBudgetReload" || state.Operation != nil {
		t.Fatalf("unsupported transition not explained: %s / %s", state.Phase, state.Reason)
	}
}
func TestFailedIntentPersistencePreventsAllBrokerWrites(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, dyn := fakeBackend(t, at)
	b.Policy.Update.Mode = "Auto"
	core.PrependReactor("create", "configmaps", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("injected persistence failure")
	})
	c := NewController(b, "status", quietLog(), at)
	if err := c.Tick(context.Background(), at); err == nil {
		t.Fatal("persistence failure swallowed")
	}
	assertNoBrokerWrites(t, core, dyn)
}
func TestConcurrentSubmissionDoesNotWaitForAnotherBroker(t *testing.T) {
	at := time.Unix(10000, 0)
	b, _, _ := fakeBackend(t, at)
	b.Policy.Update.Mode = "Auto"
	c := NewController(b, "status", quietLog(), at)
	s, err := b.Read(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	op, reason := c.newOperation(context.Background(), s, Decision{Target: 1}, nil)
	if op == nil {
		t.Fatal(reason)
	}
	op.Stage = "Submit"
	s.ClusterResources = op.Resources
	s.TemplateResources = op.Resources
	state := Status{Operation: op}
	entered := make(chan string, 3)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(metav1.APIResourceList{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "pods/resize"}}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(authorizationv1.SelfSubjectAccessReview{TypeMeta: metav1.TypeMeta{APIVersion: "authorization.k8s.io/v1", Kind: "SelfSubjectAccessReview"}, Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}})
			return
		}

		if !strings.HasSuffix(r.URL.Path, "/resize") {
			t.Error("missing resize subresource")
		}
		var patch []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil || len(patch) != 6 {
			t.Error("full conditioned resource tuple not submitted")
		}
		entered <- r.URL.Path
		<-release
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"apiVersion":"v1","kind":"Pod"}`)
	}))
	realClient, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	b.Core = realClient
	done := make(chan error, 1)
	defer func() {
		close(release)
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("fan-out did not finish")
		}
		server.Close()
	}()
	go func() {
		var cm *corev1.ConfigMap
		_, err := c.advance(context.Background(), s, &state, &cm)
		done <- err
	}()
	for i := 0; i < 3; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("requests serialized before all three were submitted")
		}
	}
	// The deferred close releases fake requests; all three have already entered.
}
func TestAcceptedSpecAndPendingStatusNeverMeanCompletion(t *testing.T) {
	at := time.Unix(10000, 0)
	b, _, _ := fakeBackend(t, at)
	c := NewController(b, "status", quietLog(), at)
	s, err := b.Read(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := c.newOperation(context.Background(), s, Decision{Target: 1}, nil)
	s.ClusterResources = op.Resources
	s.TemplateResources = op.Resources
	for i := range s.Brokers {
		s.Brokers[i].Desired = op.Resources
	}
	if c.converged(s, op) {
		t.Fatal("desired spec counted as effective completion")
	}
	for i := range s.Brokers {
		s.Brokers[i].Effective = op.Resources
	}
	s.Brokers[0].Conditions = []corev1.PodCondition{{Type: corev1.PodResizePending, Status: corev1.ConditionTrue, Reason: "Deferred"}}
	if c.converged(s, op) {
		t.Fatal("deferred resize counted as complete")
	}
	s.Brokers[0].Conditions = nil
	s.Brokers[0].CurrentGeneration = false
	if c.converged(s, op) {
		t.Fatal("old generation counted as complete")
	}
	s.Brokers[0].CurrentGeneration = true
	if !c.converged(s, op) {
		t.Fatal("fully effective target failed completion")
	}
}
func TestOwnershipCensusExcludesUnrelatedPod(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, _ := fakeBackend(t, at)
	pod, _ := core.CoreV1().Pods("test").Get(context.Background(), "rabbit-server-1", metav1.GetOptions{})
	pod.OwnerReferences[0].UID = "unrelated-sts"
	core.CoreV1().Pods("test").Update(context.Background(), pod, metav1.UpdateOptions{})
	s, err := b.Read(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	if s.Complete || s.Current >= 0 || len(s.Brokers) != 2 {
		t.Fatal("unexpected ownership completed the census")
	}
}

func TestCollectorPreservesOneResourceWhenOtherIsMissing(t *testing.T) {
	for _, missing := range []string{"cpu", "memory"} {
		t.Run(missing, func(t *testing.T) {
			at := time.Unix(10000, 0)
			b, _, dyn := fakeBackend(t, at)
			list, err := dyn.Resource(metricsGVR).Namespace("test").List(context.Background(), metav1.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range list.Items {
				usage := item.Object["containers"].([]any)[0].(map[string]any)["usage"].(map[string]any)
				delete(usage, missing)
				if missing == "cpu" {
					usage["memory"] = "300Mi"
				}
				if _, err := dyn.Resource(metricsGVR).Namespace("test").Update(context.Background(), &item, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			s, err := b.Read(context.Background(), at)
			if err != nil {
				t.Fatal(err)
			}
			s = b.Collect(context.Background(), s)
			if len(s.Samples) != 3 {
				t.Fatal("whole samples discarded")
			}
			available, absent := 0, 1
			if missing == "cpu" {
				available, absent = 1, 0
			}
			if !s.Signals[available].Complete || !s.Signals[available].Available || s.Signals[available].Profile != 1 || s.Signals[absent].Available {
				t.Fatalf("coupled resource validity: %+v", s.Signals)
			}
		})
	}
}
func TestRetriedTemplateWriteRequiresObservedOnDelete(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, dyn := fakeBackend(t, at)
	b.Policy.Update.Mode = "Auto"
	c := NewController(b, "status", quietLog(), at)
	s, err := b.Read(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	op, reason := c.newOperation(context.Background(), s, Decision{Target: 1}, nil)
	if op == nil {
		t.Fatal(reason)
	}
	op.Stage = "Template"
	s.StatefulSet.Spec.UpdateStrategy.Type = appsv1.RollingUpdateStatefulSetStrategyType
	state := Status{Operation: op}
	var cm *corev1.ConfigMap
	core.ClearActions()
	dyn.ClearActions()
	if _, err := c.advance(context.Background(), s, &state, &cm); err == nil {
		t.Fatal("strategy drift allowed a resource write")
	}
	assertNoBrokerWrites(t, core, dyn)
}
func TestRecoveryFinishesCommittedIncreaseWithoutRenewedOverload(t *testing.T) {
	at := time.Unix(10000, 0)
	b, _, _ := fakeBackend(t, at)
	b.Policy.Behavior.ScaleUp.StabilizationSeconds = 30
	c := NewController(b, "status", quietLog(), at.Add(20*time.Second))
	op := &Operation{Direction: "up", Target: "HIGH", Resources: b.Policy.Profiles["HIGH"].Resources()}
	for _, second := range []int{35, 50, 65} {
		o := engineObservation(b.Policy, at.Add(time.Duration(second)*time.Second), 0)
		s := Snapshot{Observation: o}
		ready := c.recoveryReady(s, op)
		if ready != (second == 65) {
			t.Fatalf("retained repair depended on renewed overload or skipped stabilization at%d: %v", second, ready)
		}
	}
}
func TestPartialDecreaseRecoveryBuildsWindowWhileUnresolved(t *testing.T) {
	at := time.Unix(10000, 0)
	b, _, _ := fakeBackend(t, at)
	b.Policy.Behavior.ScaleDown.StepIntervalSeconds = 60
	c := NewController(b, "status", quietLog(), at)
	op := &Operation{Direction: "down", Target: "LOW", Resources: b.Policy.Profiles["LOW"].Resources()}
	for second := 15; second <= 75; second += 15 {
		o := engineObservation(b.Policy, at.Add(time.Duration(second)*time.Second), 1)
		o.Current = -1
		o.Brokers[0].Desired = op.Resources
		o.Brokers[0].Effective = op.Resources
		ready := c.recoveryReady(Snapshot{Observation: o}, op)
		if ready != (second == 75) {
			t.Fatalf("unresolved down repair deadlocked or resumed early at%d: %v", second, ready)
		}
	}
}

func TestSupersessionAcceptsRecordedOperatorLagButRejectsExternalTuples(t *testing.T) {
	at := time.Unix(10000, 0)
	b, _, _ := fakeBackend(t, at)
	b.Policy.Update.Mode = "Auto"
	medium := b.Policy.Profiles["HIGH"]
	medium.Requests.CPU = "400m"
	b.Policy.Profiles["MEDIUM"] = medium
	b.Policy.Order = []string{"LOW", "MEDIUM", "HIGH"}
	c := NewController(b, "status", quietLog(), at)
	s, err := b.Read(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	old, reason := c.newOperation(context.Background(), s, Decision{Target: 1}, nil)
	if old == nil {
		t.Fatal(reason)
	}
	s.ClusterResources = old.Resources // Operator template and Pods still lag at LOW.
	next, reason := c.newOperation(context.Background(), s, Decision{Target: 2}, old)
	if next == nil {
		t.Fatal(reason)
	}
	if reason := operationConflict(s, next); reason != "" {
		t.Fatalf("recorded initial template lag blocked: %s", reason)
	}
	s.ClusterResources = next.Resources
	s.TemplateResources = old.Resources
	s.Brokers[0].Desired = old.Resources
	if reason := operationConflict(s, next); reason != "" {
		t.Fatalf("previous known target blocked: %s", reason)
	}
	external := medium
	external.Requests.CPU = "600m"
	s.Brokers[0].Desired = external.Resources()
	if reason := operationConflict(s, next); reason != "ExternalPodResourceChange" {
		t.Fatalf("external tuple adopted: %s", reason)
	}
}
func TestNewOperationRevisionAdvancesPastTerminalHistory(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, _ := fakeBackend(t, at)
	b.Policy.Update.Mode = "Auto"
	c := NewController(b, "status", quietLog(), at)
	s, err := b.Read(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	state := Status{Version: 2, Revision: 8, LastOperation: &Operation{Revision: 9, Stage: "Completed"}}
	var cm *corev1.ConfigMap
	if err := c.save(context.Background(), state, &cm, s); err != nil {
		t.Fatal(err)
	}
	if err := c.Tick(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	cm, err = core.CoreV1().ConfigMaps("test").Get(context.Background(), "status", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(cm.Data["status.json"]), &state); err != nil {
		t.Fatal(err)
	}
	if state.Revision != 10 || state.Operation == nil || state.Operation.Revision != 10 {
		t.Fatal("revision reused after completed operation")
	}
}

func TestCompletionDoesNotCreateASecondCurrentProfileOperation(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, dyn := fakeBackend(t, at)
	b.Policy.Update.Mode = "Auto"
	c := NewController(b, "status", quietLog(), at)
	s, err := b.Read(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	op, why := c.newOperation(context.Background(), s, Decision{Target: 1}, nil)
	if op == nil {
		t.Fatal(why)
	}
	op.Stage = "Submit"
	var cm *corev1.ConfigMap
	if err := c.save(context.Background(), Status{Version: 2, Revision: 1, Operation: op}, &cm, s); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(op.Resources)
	var resources map[string]any
	json.Unmarshal(raw, &resources)
	cr := s.Cluster.DeepCopy()
	cr.Object["spec"].(map[string]any)["resources"] = resources
	dyn.Resource(clusterGVR).Namespace("test").Update(context.Background(), cr, metav1.UpdateOptions{})
	st := s.StatefulSet.DeepCopy()
	st.Spec.Template.Spec.Containers[0].Resources = op.Resources
	core.AppsV1().StatefulSets("test").Update(context.Background(), st, metav1.UpdateOptions{})
	for _, broker := range s.Brokers {
		pod := broker.Pod.DeepCopy()
		pod.Spec.Containers[0].Resources = op.Resources
		pod.Status.ContainerStatuses[0].Resources = op.Resources.DeepCopy()
		core.CoreV1().Pods("test").Update(context.Background(), pod, metav1.UpdateOptions{})
	}
	core.ClearActions()
	dyn.ClearActions()
	if err := c.Tick(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	cm, _ = core.CoreV1().ConfigMaps("test").Get(context.Background(), "status", metav1.GetOptions{})
	var state Status
	json.Unmarshal([]byte(cm.Data["status.json"]), &state)
	if state.Operation != nil || state.LastOperation == nil || state.LastOperation.Revision != 1 || state.Revision != 1 {
		t.Fatal("completed HIGH spawned a redundant HIGH operation")
	}
	assertNoBrokerWrites(t, core, dyn)
}

func TestStrictPreflightRejectsQoSChangeAndRestartRequiredPolicy(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, dyn := fakeBackend(t, at)
	c := NewController(b, "status", quietLog(), at)
	s, err := b.Read(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	guaranteed := b.Policy.Profiles["HIGH"]
	guaranteed.Requests.CPU = "1"
	guaranteed.Limits.CPU = "1"
	guaranteed.Requests.Memory = "1Gi"
	if path, reason := c.preflight(context.Background(), s, guaranteed.Resources()); path != "" || reason != "QoSClassChange" {
		t.Fatalf("QoS transition permitted in place: %s %s", path, reason)
	}
	for i := range s.Brokers {
		s.Brokers[i].Pod.Spec.Containers[0].ResizePolicy = []corev1.ContainerResizePolicy{{ResourceName: corev1.ResourceMemory, RestartPolicy: corev1.RestartContainer}}
	}
	if path, reason := c.preflight(context.Background(), s, b.Policy.Profiles["HIGH"].Resources()); path != "" || reason != "ContainerResizeRequiresRestart" {
		t.Fatalf("restart-required policy permitted in place: %s %s", path, reason)
	}
	assertNoBrokerWrites(t, core, dyn)
}
func TestTemplateWriteFailureRetainsIntentAndRetriesWithoutPodWrites(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, dyn := fakeBackend(t, at)
	b.Policy.Update.Mode = "Auto"
	c := NewController(b, "status", quietLog(), at)
	failed := true
	dyn.PrependReactor("patch", "rabbitmqclusters", func(ktesting.Action) (bool, runtime.Object, error) {
		if failed {
			return true, nil, errors.New("injected template write failure")
		}
		return false, nil, nil
	})
	if err := c.Tick(context.Background(), at); err == nil {
		t.Fatal("failed CR write reported success")
	}
	cm, _ := core.CoreV1().ConfigMaps("test").Get(context.Background(), "status", metav1.GetOptions{})
	var state Status
	json.Unmarshal([]byte(cm.Data["status.json"]), &state)
	if state.Operation == nil || state.Operation.Stage != "Template" {
		t.Fatal("failed write discarded durable intent")
	}
	for _, a := range core.Actions() {
		if a.GetResource().Resource == "pods" && a.GetVerb() == "patch" {
			t.Fatal("submitted Pods before template convergence")
		}
	}
	failed = false
	if err := c.Tick(context.Background(), at.Add(15*time.Second)); err != nil {
		t.Fatal(err)
	}
	cr, _ := dyn.Resource(clusterGVR).Namespace("test").Get(context.Background(), "rabbit", metav1.GetOptions{})
	raw, _, _ := unstructured.NestedMap(cr.Object, "spec", "resources")
	actual, _ := decodeResources(raw)
	if !policy.Equal(actual, b.Policy.Profiles["HIGH"].Resources()) {
		t.Fatal("retained target did not repair the failed CR write")
	}
	for _, a := range core.Actions() {
		if a.GetResource().Resource == "pods" && a.GetVerb() == "patch" {
			t.Fatal("submitted before owned StatefulSet reflected target")
		}
	}
}
