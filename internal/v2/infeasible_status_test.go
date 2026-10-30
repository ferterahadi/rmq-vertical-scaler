package v2

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestInfeasibleRefreshesAllBrokerOutcomesWithoutFurtherWrites(t *testing.T) {
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
	op.Stage = "Submit"
	op.Revision = 1
	raw, _ := json.Marshal(op.Resources)
	var resources map[string]any
	json.Unmarshal(raw, &resources)
	cr := s.Cluster.DeepCopy()
	cr.Object["spec"].(map[string]any)["resources"] = resources
	dyn.Resource(clusterGVR).Namespace("test").Update(context.Background(), cr, metav1.UpdateOptions{})
	st := s.StatefulSet.DeepCopy()
	st.Spec.Template.Spec.Containers[0].Resources = op.Resources
	core.AppsV1().StatefulSets("test").Update(context.Background(), st, metav1.UpdateOptions{})
	want := []string{"Effective", "Infeasible", "Deferred"}
	for i, broker := range s.Brokers {
		pod := broker.Pod.DeepCopy()
		pod.Spec.Containers[0].Resources = op.Resources
		if i == 0 {
			pod.Status.ContainerStatuses[0].Resources = op.Resources.DeepCopy()
		} else {
			pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodResizePending, Status: corev1.ConditionTrue, Reason: want[i]}}
		}
		core.CoreV1().Pods("test").Update(context.Background(), pod, metav1.UpdateOptions{})
		progress := op.Brokers[broker.Name]
		progress.Attempts, progress.AcceptedAt, progress.SubmittedAt = 1, at, at
		op.Brokers[broker.Name] = progress
	}
	var cm *corev1.ConfigMap
	if err := c.save(context.Background(), Status{Version: 2, Revision: 1, Operation: op}, &cm, s); err != nil {
		t.Fatal(err)
	}
	core.ClearActions()
	dyn.ClearActions()
	for second := 0; second <= 30; second += 15 {
		if err := c.Tick(context.Background(), at.Add(time.Duration(second)*time.Second)); err != nil {
			t.Fatal(err)
		}
		state, _, err := c.load(context.Background())
		if err != nil || state.Phase != "Blocked" || state.Reason != "ResizeInfeasibleTargetRetained" || state.Operation == nil || state.Operation.Revision != 1 || state.Operation.Target != "HIGH" {
			t.Fatalf("infeasible operation lost blocked/retained state: %+v %v", state, err)
		}
		for i, broker := range s.Brokers {
			progress := state.Operation.Brokers[broker.Name]
			if progress.Outcome != want[i] || progress.Attempts != 1 || !progress.AcceptedAt.Equal(at) || !progress.SubmittedAt.Equal(at) {
				t.Fatalf("stale or destructive progress for %s: %+v", broker.Name, progress)
			}
		}
		assertNoBrokerWrites(t, core, dyn)
	}
}
