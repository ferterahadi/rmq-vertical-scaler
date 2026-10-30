package v2

import (
	"context"
	"encoding/json"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

func TestReviewCompletionStartsFreshDownWindow(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, dyn := fakeBackend(t, at)
	b.Policy.Update.Mode = "Auto"
	b.Policy.Behavior.ScaleDown.StepIntervalSeconds = 60
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
	for seconds := 15; seconds <= 75; seconds += 15 {
		stamp := at.Add(time.Duration(seconds) * time.Second)
		metrics, _ := dyn.Resource(metricsGVR).Namespace("test").List(context.Background(), metav1.ListOptions{})
		for _, m := range metrics.Items {
			m.Object["timestamp"] = stamp.Format(time.RFC3339)
			m.Object["containers"].([]any)[0].(map[string]any)["usage"].(map[string]any)["cpu"] = "10m"
			dyn.Resource(metricsGVR).Namespace("test").Update(context.Background(), &m, metav1.UpdateOptions{})
		}
		if err := c.Tick(context.Background(), stamp); err != nil {
			t.Fatal(err)
		}
		latest, _ := core.CoreV1().ConfigMaps("test").Get(context.Background(), "status", metav1.GetOptions{})
		state = Status{}
		json.Unmarshal([]byte(latest.Data["status.json"]), &state)
		if seconds <= 60 && state.Operation != nil {
			t.Fatalf("early next operation at %ds: %+v", seconds, state.Operation)
		}
		if seconds == 75 && (state.Operation == nil || state.Operation.Direction != "down") {
			t.Fatalf("fresh full down window did not authorize next step: %+v", state)
		}
	}

}
