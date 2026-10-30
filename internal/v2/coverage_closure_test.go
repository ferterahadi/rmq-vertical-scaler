package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/policy"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestClosureInfeasibleRetainsTargetWithoutImplicitFallback(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, dyn := fakeBackend(t, at)
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
	s.Brokers[0].Conditions = []corev1.PodCondition{{Type: corev1.PodResizePending, Status: corev1.ConditionTrue, Reason: "Infeasible"}}
	state := Status{Version: 2, Operation: op}
	var cm *corev1.ConfigMap
	for i := 0; i < 3; i++ {
		reason, err := c.advance(context.Background(), s, &state, &cm)
		if err != nil || reason != "ResizeInfeasibleTargetRetained" || state.Operation != op || op.Path != "inplace" {
			t.Fatalf("infeasible target was lost or implicitly rolled: %s %v %+v", reason, err, op)
		}
	}
	assertNoBrokerWrites(t, core, dyn)
}

func TestClosurePartialQueuePagesCannotDecreaseWithHealthyMemory(t *testing.T) {
	at := time.Unix(10000, 0)
	var failPage atomic.Bool
	failPage.Store(true)
	queues := queueBackend(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "2" && failPage.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintf(w, `{"page":%s,"page_count":2,"total_count":2,"filtered_count":2,"items":[{"vhost":"/","name":"queue-%s","messages":0}]}`, page, page)
	})
	b, core, dyn := fakeBackend(t, at)
	b.Policy.RMQ, b.HTTP = queues.Policy.RMQ, queues.HTTP
	b.Policy.Triggers = []policy.Trigger{queueTrigger("QueueLength", nil)}
	b.Policy.Update.Mode = "Auto"
	b.Policy.Behavior.ScaleDown.StepIntervalSeconds = 60
	c := NewController(b, "status", quietLog(), at)
	s, err := b.Read(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	high := b.Policy.Profiles["HIGH"].Resources()
	raw, _ := json.Marshal(high)
	var rr map[string]any
	json.Unmarshal(raw, &rr)
	cr := s.Cluster.DeepCopy()
	cr.Object["spec"].(map[string]any)["resources"] = rr
	dyn.Resource(clusterGVR).Namespace("test").Update(context.Background(), cr, metav1.UpdateOptions{})
	st := s.StatefulSet.DeepCopy()
	st.Spec.Template.Spec.Containers[0].Resources = high
	core.AppsV1().StatefulSets("test").Update(context.Background(), st, metav1.UpdateOptions{})
	for _, broker := range s.Brokers {
		pod := broker.Pod.DeepCopy()
		pod.Spec.Containers[0].Resources = high
		pod.Status.ContainerStatuses[0].Resources = high.DeepCopy()
		core.CoreV1().Pods("test").Update(context.Background(), pod, metav1.UpdateOptions{})
	}
	core.ClearActions()
	dyn.ClearActions()
	for second := 0; second <= 90; second += 15 {
		stamp := at.Add(time.Duration(second) * time.Second)
		metrics, _ := dyn.Resource(metricsGVR).Namespace("test").List(context.Background(), metav1.ListOptions{})
		for _, metric := range metrics.Items {
			metric.Object["timestamp"] = stamp.Format(time.RFC3339)
			dyn.Resource(metricsGVR).Namespace("test").Update(context.Background(), &metric, metav1.UpdateOptions{})
		}
		if err := c.Tick(context.Background(), stamp); err != nil {
			t.Fatal(err)
		}
		state, _, err := c.load(context.Background())
		if err != nil || state.Operation != nil || state.Observation.Signals[0].Complete || len(state.Observation.Samples) != 3 {
			t.Fatalf("partial queues were accepted or healthy memory missing: %+v %v", state, err)
		}
	}
	assertNoBrokerWrites(t, core, dyn)
	// Restoring complete pages must permit progress after a new signal window;
	// this prevents the rejection test passing because the fixture can never downscale.
	failPage.Store(false)
	for second := 105; second <= 180; second += 15 {
		stamp := at.Add(time.Duration(second) * time.Second)
		metrics, _ := dyn.Resource(metricsGVR).Namespace("test").List(context.Background(), metav1.ListOptions{})
		for _, metric := range metrics.Items {
			metric.Object["timestamp"] = stamp.Format(time.RFC3339)
			dyn.Resource(metricsGVR).Namespace("test").Update(context.Background(), &metric, metav1.UpdateOptions{})
		}
		if err := c.Tick(context.Background(), stamp); err != nil {
			t.Fatal(err)
		}
		if second < 165 {
			state, _, err := c.load(context.Background())
			if err != nil || state.Operation != nil {
				t.Fatalf("restored pages borrowed the incomplete-signal interval at %ds: %+v %v", second, state, err)
			}
		}
	}
	state, _, err := c.load(context.Background())
	if err != nil || state.Operation == nil || state.Operation.Direction != "down" {
		t.Fatalf("restored pages failed to authorize a fresh downward operation: %+v %v", state, err)
	}
}

func TestClosureFilteredRateFlowsThroughControllerWithExactScope(t *testing.T) {
	at := time.Unix(10000, 0)
	q := queueBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/queues":
			fmt.Fprint(w, `[{"name":"selected-a","vhost":"/","message_stats":{"publish_details":{"rate":12}}},{"name":"selected-b","vhost":"/","message_stats":{"publish_details":{"rate":8}}},{"name":"excluded","vhost":"/","message_stats":{"publish_details":{"rate":99}}}]`)
		case "/api/whoami":
			fmt.Fprint(w, `{"name":"monitor","tags":["administrator"]}`)
		case "/api/vhosts":
			fmt.Fprint(w, `[{"name":"/"}]`)
		case "/api/permissions":
			fmt.Fprint(w, `[{"user":"monitor","vhost":"/","read":".*"}]`)
		case "/api/overview":
			fmt.Fprint(w, `{"message_stats":{"publish_details":{"rate":7}}}`)
		default:
			w.WriteHeader(404)
		}
	})
	b, core, dyn := fakeBackend(t, at)
	b.Policy.RMQ, b.HTTP, b.User = q.Policy.RMQ, q.HTTP, "monitor"
	b.Policy.Triggers = []policy.Trigger{queueTrigger("MessageRate", &policy.Selector{QueueNameRegex: "^selected-"}), queueTrigger("MessageRate", nil)}
	for i := range b.Policy.Triggers {
		b.Policy.Triggers[i].Metadata.Thresholds["HIGH"] = 10
	}
	c := NewController(b, "status", quietLog(), at)
	if err := c.Tick(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	state, _, err := c.load(context.Background())
	if err != nil || state.Observation.Signals[0].Value != 20 || state.Observation.Signals[1].Value != 7 || state.Decision.Recommended != 1 {
		t.Fatalf("controller lost filtered sum or mixed overview: %+v %v", state, err)
	}
	assertNoBrokerWrites(t, core, dyn)
}

func TestClosureCollectorCachedStaleAndOldContainerSamplesCannotStabilize(t *testing.T) {
	for _, mode := range []string{"cached", "stale", "invalid-window", "old-container"} {
		t.Run(mode, func(t *testing.T) {
			at := time.Unix(10000, 0)
			b, core, dyn := fakeBackend(t, at)
			b.Policy.Update.Mode = "Auto"
			b.Policy.Behavior.ScaleUp.StabilizationSeconds = 30
			stamp := at
			if mode == "stale" {
				stamp = at.Add(-2 * time.Minute)
			}
			if mode == "old-container" {
				pods, _ := core.CoreV1().Pods("test").List(context.Background(), metav1.ListOptions{})
				for _, item := range pods.Items {
					item.Status.ContainerStatuses[0].State.Running.StartedAt = metav1.NewTime(at.Add(-5 * time.Second))
					core.CoreV1().Pods("test").Update(context.Background(), &item, metav1.UpdateOptions{})
				}
				stamp = at.Add(-20 * time.Second)
			}
			metrics, _ := dyn.Resource(metricsGVR).Namespace("test").List(context.Background(), metav1.ListOptions{})
			for _, metric := range metrics.Items {
				metric.Object["timestamp"] = stamp.Format(time.RFC3339)
				if mode == "invalid-window" {
					metric.Object["window"] = "0s"
				}
				dyn.Resource(metricsGVR).Namespace("test").Update(context.Background(), &metric, metav1.UpdateOptions{})
			}
			core.ClearActions()
			dyn.ClearActions()
			c := NewController(b, "status", quietLog(), at)
			for second := 0; second <= 90; second += 15 {
				if err := c.Tick(context.Background(), at.Add(time.Duration(second)*time.Second)); err != nil {
					t.Fatal(err)
				}
				state, _, err := c.load(context.Background())
				if err != nil || state.Operation != nil || state.Decision.Target >= 0 {
					t.Fatalf("%s samples authorized a mutation: %+v %v", mode, state, err)
				}
			}
			assertNoBrokerWrites(t, core, dyn)
			// A fresh sample stream restores real eligibility: this positive
			// control detects fixtures that could never recommend an increase.
			for second := 105; second <= 150; second += 15 {
				now := at.Add(time.Duration(second) * time.Second)
				metrics, _ := dyn.Resource(metricsGVR).Namespace("test").List(context.Background(), metav1.ListOptions{})
				for _, metric := range metrics.Items {
					metric.Object["timestamp"] = now.Format(time.RFC3339)
					metric.Object["window"] = "10s"
					dyn.Resource(metricsGVR).Namespace("test").Update(context.Background(), &metric, metav1.UpdateOptions{})
				}
				if err := c.Tick(context.Background(), now); err != nil {
					t.Fatal(err)
				}
			}
			state, _, err := c.load(context.Background())
			if err != nil || state.Operation == nil || state.Operation.Direction != "up" {
				t.Fatalf("fresh samples did not restore an upward operation: %+v %v", state, err)
			}
		})
	}
}
