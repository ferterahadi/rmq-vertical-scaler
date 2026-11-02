package v2

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/policy"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type collectorTransport func(*http.Request) (*http.Response, error)

func (f collectorTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func overviewWithSlowQueues(t *testing.T) (*Backend, *atomic.Int32) {
	t.Helper()
	p := testPolicy(t)
	p.RMQ = &policy.Connection{Host: "rabbit", Port: "15672"}
	var calls atomic.Int32
	client := &http.Client{Transport: collectorTransport(func(r *http.Request) (*http.Response, error) {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		if r.URL.Path == "/api/queues" {
			calls.Add(1)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		body, ok := map[string]string{
			"/api/whoami":      `{"name":"admin","tags":["administrator"]}`,
			"/api/vhosts":      `[{"name":"/"}]`,
			"/api/permissions": `[{"user":"admin","vhost":"/","read":".*"}]`,
			"/api/overview":    `{"message_stats":{"publish_details":{"rate":1000}}}`,
		}[r.URL.Path]
		if !ok {
			t.Errorf("unexpected management endpoint: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)),
			Header: http.Header{}, Request: r}, nil
	})}
	return &Backend{Policy: p, HTTP: client}, &calls
}

func TestOverviewOnlyRateSkipsQueueEnumeration(t *testing.T) {
	b, calls := overviewWithSlowQueues(t)
	b.Policy.Triggers = []policy.Trigger{queueTrigger("MessageRate", nil)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s := b.queueSignals(ctx, time.Unix(10000, 0))[0]
	if !s.Available || !s.Complete || s.Value != 1000 || s.Profile != 1 || calls.Load() != 0 {
		t.Fatalf("overview depended on unused enumeration: %+v; queue calls=%d", s, calls.Load())
	}
}

func TestOverviewRateSurvivesIndependentQueueDeadline(t *testing.T) {
	for _, selectedRate := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			t.Run(map[bool]string{false: "depth", true: "selected-rate"}[selectedRate]+map[bool]string{false: "-first", true: "-last"}[reverse], func(t *testing.T) {
				b, calls := overviewWithSlowQueues(t)
				selected := queueTrigger("QueueLength", nil)
				if selectedRate {
					selected = queueTrigger("MessageRate", &policy.Selector{QueueNameRegex: ".*"})
				}
				b.Policy.Triggers = []policy.Trigger{queueTrigger("MessageRate", nil), selected}
				overviewIndex := 0
				if reverse {
					b.Policy.Triggers[0], b.Policy.Triggers[1] = b.Policy.Triggers[1], b.Policy.Triggers[0]
					overviewIndex = 1
				}
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				signals := b.queueSignals(ctx, time.Unix(10000, 0))
				overview, queues := signals[overviewIndex], signals[1-overviewIndex]
				if !overview.Available || !overview.Complete || overview.Value != 1000 || overview.Profile != 1 {
					t.Fatalf("healthy overview lost to queue timeout: %+v", signals)
				}
				if queues.Available || queues.Complete || calls.Load() != 1 {
					t.Fatalf("timed-out queues became valid: %+v; calls=%d", signals, calls.Load())
				}
			})
		}
	}
}

func TestCollectorValidatesMetricsAtReceiptAndRetainsPollTime(t *testing.T) {
	poll := time.Unix(10000, 0)
	receipt := poll.Add(10 * time.Second)
	for _, tc := range []struct {
		name  string
		stamp time.Time
		valid bool
	}{
		{"published-during-read", poll.Add(5 * time.Second), true},
		{"actually-future", receipt.Add(time.Second), false},
		{"stale-at-receipt", poll.Add(-55 * time.Second), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, dyn := fakeBackend(t, poll)
			b.Now = func() time.Time { return receipt }
			s, err := b.Read(context.Background(), poll)
			if err != nil {
				t.Fatal(err)
			}
			metrics, err := dyn.Resource(metricsGVR).Namespace("test").List(context.Background(), metav1.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, metric := range metrics.Items {
				metric.Object["timestamp"] = tc.stamp.Format(time.RFC3339)
				if _, err := dyn.Resource(metricsGVR).Namespace("test").Update(context.Background(), &metric, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			s = b.Collect(context.Background(), s)
			if !s.At.Equal(poll) || !s.ObservedAt.Equal(receipt) {
				t.Fatalf("poll/receipt clocks conflated: poll=%s observed=%s", s.At, s.ObservedAt)
			}
			for _, signal := range s.Signals {
				if signal.Available != tc.valid || signal.Complete != tc.valid {
					t.Fatalf("receipt-time freshness incorrect: %+v", signal)
				}
			}
		})
	}
}

func TestPublishedDuringCollectionSupportsStabilizedIncrease(t *testing.T) {
	p := testPolicy(t)
	p.Behavior.ScaleUp.StabilizationSeconds = 30
	start := time.Unix(10000, 0)
	e := NewEngine(p, start)
	for second := 0; second <= 30; second += 15 {
		poll := start.Add(time.Duration(second) * time.Second)
		o := engineObservation(p, poll, 0)
		o.ObservedAt = poll.Add(3 * time.Second)
		o.Signals = nil
		for i := range o.Samples {
			o.Samples[i].Timestamp = poll.Add(2 * time.Second)
			o.Samples[i].CPU = 0.3
			o.Samples[i].Memory = 300 * 1024 * 1024
		}
		for i := range p.Triggers {
			o.Signals = append(o.Signals, ResourceSignal(p, i, o))
		}
		d := e.Evaluate(o, false)
		if d.Recommended != 1 || (second < 30 && d.Target >= 0) || (second == 30 && (d.Target != 1 || !d.EpisodeStart.Equal(start))) {
			t.Fatalf("collection-time sample lost demand or shortened poll stabilization: %ds %+v", second, d)
		}
	}
}

func TestCollectorDefaultReceiptClockAcceptsPostPollSamples(t *testing.T) {
	poll := time.Now().Add(-2 * time.Second)
	b, _, dyn := fakeBackend(t, poll)
	b.Now = nil // Exercise the production receipt clock rather than the fake clock.
	s, err := b.Read(context.Background(), poll)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Truncate(time.Second)
	metrics, err := dyn.Resource(metricsGVR).Namespace("test").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, metric := range metrics.Items {
		metric.Object["timestamp"] = stamp.Format(time.RFC3339)
		if _, err := dyn.Resource(metricsGVR).Namespace("test").Update(context.Background(), &metric, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	before := time.Now()
	s = b.Collect(context.Background(), s)
	if s.ObservedAt.Before(before) || s.ObservedAt.After(time.Now()) || !s.At.Equal(poll) {
		t.Fatalf("invalid receipt clock: %+v", s.Observation)
	}
	for _, signal := range s.Signals {
		if !signal.Available || !signal.Complete {
			t.Fatalf("post-poll sample rejected by production clock: %+v", signal)
		}
	}
}

func TestPublishedDuringCollectionPreservesMemoryDecreaseWindow(t *testing.T) {
	p := testPolicy(t)
	p.Behavior.ScaleDown.StepIntervalSeconds = 60
	start := time.Unix(10000, 0)
	e := NewEngine(p, start)
	for second := 0; second <= 75; second += 15 {
		poll := start.Add(time.Duration(second) * time.Second)
		o := engineObservation(p, poll, 1)
		o.ObservedAt = poll.Add(3 * time.Second)
		o.Signals = nil
		for i := range o.Samples {
			o.Samples[i].Timestamp = poll.Add(2 * time.Second)
		}
		for i := range p.Triggers {
			o.Signals = append(o.Signals, ResourceSignal(p, i, o))
		}
		d := e.Evaluate(o, false)
		if (second < 75 && d.Target >= 0) || (second == 75 && d.Target != 0) {
			t.Fatalf("collection-time samples reset or shortened safe memory window: %ds %+v", second, d)
		}
	}
}

func TestRecoveryUsesReceiptClockWithoutShorteningStabilization(t *testing.T) {
	for _, offset := range []int{0, 2, 4, -61} {
		t.Run((time.Duration(offset) * time.Second).String(), func(t *testing.T) {
			start := time.Unix(10000, 0)
			b, _, _ := fakeBackend(t, start)
			b.Policy.Behavior.ScaleUp.StabilizationSeconds = 30
			c := NewController(b, "status", quietLog(), start)
			op := &Operation{Direction: "up", Resources: b.Policy.Profiles["HIGH"].Resources()}
			for second := 0; second <= 30; second += 15 {
				poll := start.Add(time.Duration(second) * time.Second)
				o := engineObservation(b.Policy, poll, 0)
				o.ObservedAt = poll.Add(3 * time.Second)
				for i := range o.Signals {
					o.Signals[i].Timestamp = poll.Add(time.Duration(offset) * time.Second)
				}
				ready := c.recoveryReady(Snapshot{Observation: o}, op)
				want := second == 30 && (offset == 0 || offset == 2)
				if ready != want {
					t.Fatalf("recovery clock/window mismatch at %ds: ready=%v want=%v", second, ready, want)
				}
			}
		})
	}
}
