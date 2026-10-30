package v2

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
)

func TestLostResizeReplyRecoversFromLiveStateWithoutResubmission(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, _ := fakeBackend(t, at)
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
	op.Stage, op.Revision = "Submit", 1
	s.ClusterResources, s.TemplateResources = op.Resources, op.Resources
	state := Status{Version: 2, Operation: op}
	var calls atomic.Int32
	core.PrependReactor("patch", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "resize" {
			return false, nil, nil
		}
		calls.Add(1)
		name := action.(ktesting.PatchAction).GetName()
		object, err := core.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), "test", name)
		if err != nil {
			return true, nil, err
		}
		pod := object.(*corev1.Pod).DeepCopy()
		pod.Spec.Containers[0].Resources = op.Resources
		pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodResizeInProgress, Status: corev1.ConditionTrue}}
		if err := core.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), pod, "test"); err != nil {
			return true, nil, err
		}
		return true, nil, errors.New("response lost after API applied resize")
	})
	var cm *corev1.ConfigMap
	if _, err := c.advance(context.Background(), s, &state, &cm); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected three initial submissions, got %d", calls.Load())
	}
	for _, second := range []int{15, 30, 60} {
		s, err = b.Read(context.Background(), at.Add(time.Duration(second)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		s.ClusterResources, s.TemplateResources = op.Resources, op.Resources
		if c.converged(s, op) {
			t.Fatal("accepted desired resources incorrectly completed pending work")
		}
		if _, err := c.advance(context.Background(), s, &state, &cm); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 3 || op.Revision != 1 {
		t.Fatal("lost replies caused resubmission or target replacement")
	}
	for _, broker := range s.Brokers {
		pod := broker.Pod.DeepCopy()
		pod.Status.ContainerStatuses[0].Resources = op.Resources.DeepCopy()
		pod.Status.Conditions = nil
		if _, err := core.CoreV1().Pods("test").Update(context.Background(), pod, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	s, err = b.Read(context.Background(), at.Add(75*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	s.ClusterResources, s.TemplateResources = op.Resources, op.Resources
	if !c.converged(s, op) {
		t.Fatal("live effective state did not recover the retained operation")
	}
	for name, progress := range op.Brokers {
		if progress.Outcome != "Effective" || progress.Attempts != 1 || progress.Error != "" {
			t.Fatalf("incorrect recovered progress for %s: %+v", name, progress)
		}
	}
}
