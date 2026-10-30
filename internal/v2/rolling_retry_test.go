package v2

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
)

func TestRollingRetryRechecksSafetyAfterFailedStrategyWrite(t *testing.T) {
	for _, lost := range []string{"ready broker", "shutdown guard"} {
		t.Run(lost, func(t *testing.T) {
			at := time.Unix(10000, 0)
			b, core, dyn := fakeBackend(t, at)
			c := NewController(b, "status", quietLog(), at)
			s, err := b.Read(context.Background(), at)
			if err != nil {
				t.Fatal(err)
			}
			s.StatefulSet.Spec.Template.Spec.Containers[0].Lifecycle = &corev1.Lifecycle{
				PreStop: &corev1.LifecycleHandler{Exec: &corev1.ExecAction{Command: []string{"await_online_quorum_plus_one"}}},
			}
			state := Status{Operation: &Operation{Path: "rolling", Stage: "Submit"}}
			var cm *corev1.ConfigMap
			dyn.PrependReactor("patch", "rabbitmqclusters", func(ktesting.Action) (bool, runtime.Object, error) {
				return true, nil, fmt.Errorf("temporary strategy write failure")
			})
			if _, err := c.advance(context.Background(), s, &state, &cm); err == nil || state.Operation.Stage != "Rolling" {
				t.Fatalf("expected failed strategy write after durable Rolling stage: %v", err)
			}
			if lost == "ready broker" {
				s.Brokers[0].Ready = false
			} else {
				s.StatefulSet.Spec.Template.Spec.Containers[0].Lifecycle = nil
			}
			core.ClearActions()
			dyn.ClearActions()
			if _, err := c.advance(context.Background(), s, &state, &cm); err == nil {
				t.Fatal("unsafe rolling retry was not blocked")
			}
			assertNoBrokerWrites(t, core, dyn)
			// Once enabled, an ordinary unready broker during rolling must be
			// observed rather than preventing convergence observation.
			unstructured.SetNestedField(s.Cluster.Object, "RollingUpdate", "spec", "override", "statefulSet", "spec", "updateStrategy", "type")
			if reason, err := c.advance(context.Background(), s, &state, &cm); err != nil || reason != "ObservingOperatorRollingConvergence" {
				t.Fatalf("already-running rolling should remain observable: %s %v", reason, err)
			}
			assertNoBrokerWrites(t, core, dyn)
		})
	}
}
