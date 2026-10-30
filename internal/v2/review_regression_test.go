package v2

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/policy"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
	"os"
	"strings"
	"testing"
	"time"
)

func TestReviewFailedReadInterruptsStabilization(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, dyn := fakeBackend(t, at)
	b.Policy.Behavior.ScaleUp.StabilizationSeconds = 30
	c := NewController(b, "status", quietLog(), at)
	if err := c.Tick(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	failed := true
	dyn.PrependReactor("get", "rabbitmqclusters", func(ktesting.Action) (bool, runtime.Object, error) {
		if failed {
			return true, nil, errors.New("outage")
		}
		return false, nil, nil
	})
	if err := c.Tick(context.Background(), at.Add(15*time.Second)); err == nil {
		t.Fatal("expected read failure")
	}
	failed = false
	metrics, _ := dyn.Resource(metricsGVR).Namespace("test").List(context.Background(), metav1.ListOptions{})
	for _, m := range metrics.Items {
		m.Object["timestamp"] = at.Add(30 * time.Second).Format(time.RFC3339)
		dyn.Resource(metricsGVR).Namespace("test").Update(context.Background(), &m, metav1.UpdateOptions{})
	}
	if err := c.Tick(context.Background(), at.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	cm, _ := core.CoreV1().ConfigMaps("test").Get(context.Background(), "status", metav1.GetOptions{})
	var state Status
	json.Unmarshal([]byte(cm.Data["status.json"]), &state)
	if state.Decision.Target >= 0 {
		t.Fatalf("failed read counted as continuity: %+v", state.Decision)
	}
}
func TestReviewMissingStateMixedPodsBlockWrites(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, dyn := fakeBackend(t, at)
	b.Policy.Update.Mode = "Auto"
	pod, _ := core.CoreV1().Pods("test").Get(context.Background(), "rabbit-server-0", metav1.GetOptions{})
	r := b.Policy.Profiles["HIGH"].Resources()
	pod.Spec.Containers[0].Resources = r
	pod.Status.ContainerStatuses[0].Resources = &r
	core.CoreV1().Pods("test").Update(context.Background(), pod, metav1.UpdateOptions{})
	c := NewController(b, "status", quietLog(), at)
	if err := c.Tick(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	assertNoBrokerWrites(t, core, dyn)
}
func TestReviewQueueOnlyInvalidMetricWindowBlocksDown(t *testing.T) {
	p := testPolicy(t)
	p.Triggers = p.Triggers[:1]
	p.Triggers[0].Type = "rabbitmq"
	p.Behavior.ScaleDown.StepIntervalSeconds = 60
	at := time.Unix(10000, 0)
	e := NewEngine(p, at.Add(-time.Hour))
	for seconds := 0; seconds <= 60; seconds += 15 {
		o := engineObservation(p, at.Add(time.Duration(seconds)*time.Second), 1)
		for i := range o.Samples {
			o.Samples[i].Window = 0
		}
		if yes, _ := e.DownReady(o, 0); yes {
			t.Fatal("zero-window samples authorized downscale")
		}
	}
}
func TestReviewSupersessionDoesNotAdoptExternalCR(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, dyn := fakeBackend(t, at)
	b.Policy.Update.Mode = "Auto"
	high := b.Policy.Profiles["HIGH"]
	middle := high
	middle.Requests.CPU = "400m"
	b.Policy.Profiles["MEDIUM"] = middle
	b.Policy.Order = []string{"LOW", "MEDIUM", "HIGH"}
	c := NewController(b, "status", quietLog(), at)
	s, err := b.Read(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	op, why := c.newOperation(context.Background(), s, Decision{Target: 1}, nil)
	if op == nil {
		t.Fatal(why)
	}
	state := Status{Version: 2, Operation: op}
	var cm *corev1.ConfigMap
	if err := c.save(context.Background(), state, &cm, s); err != nil {
		t.Fatal(err)
	}
	external := middle
	external.Requests.CPU = "600m"
	raw, _ := json.Marshal(external.Resources())
	var resources map[string]any
	json.Unmarshal(raw, &resources)
	cr := s.Cluster.DeepCopy()
	cr.Object["spec"].(map[string]any)["resources"] = resources
	dyn.Resource(clusterGVR).Namespace("test").Update(context.Background(), cr, metav1.UpdateOptions{})
	metrics, _ := dyn.Resource(metricsGVR).Namespace("test").List(context.Background(), metav1.ListOptions{})
	for _, m := range metrics.Items {
		m.Object["containers"].([]any)[0].(map[string]any)["usage"].(map[string]any)["cpu"] = "500m"
		dyn.Resource(metricsGVR).Namespace("test").Update(context.Background(), &m, metav1.UpdateOptions{})
	}
	core.ClearActions()
	dyn.ClearActions()
	if err := c.Tick(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	assertNoBrokerWrites(t, core, dyn)
}

func TestReviewCaseAliasesCannotOverrideOff(t *testing.T) {
	data, err := os.ReadFile("../../examples/v2/resources.json")
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"mode": "Off"`, `"mode": "Off", "Mode": "Auto"`, 1))
	p, err := policy.Parse(data)
	if err == nil {
		t.Fatalf("ambiguous key accepted; effective update mode=%s", p.Update.Mode)
	}
}
