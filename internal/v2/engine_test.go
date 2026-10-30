package v2

import (
	"os"
	"testing"
	"time"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/policy"
)

func testPolicy(t *testing.T) *policy.Config {
	t.Helper()
	data, err := os.ReadFile("../../examples/v2/resources.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := policy.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func engineObservation(p *policy.Config, at time.Time, current int) Observation {
	o := Observation{At: at, Identity: "brokers", Complete: true, Current: current}
	for _, name := range []string{"a", "b", "c"} {
		r := p.Profiles[p.Order[current]].Resources()
		o.Brokers = append(o.Brokers, Broker{Name: name, UID: name, ContainerID: name, Ready: true, CurrentGeneration: true, Desired: r, Effective: r, Started: at.Add(-time.Hour)})
		o.Samples = append(o.Samples, Sample{Pod: name, UID: name, ContainerID: name, Timestamp: at, Window: 10 * time.Second, CPU: 0.01, Memory: 100 * 1024 * 1024})
	}
	for i, tr := range p.Triggers {
		o.Signals = append(o.Signals, Signal{Trigger: i, Type: tr.Type, Available: true, Complete: true, Timestamp: at, Profile: 0})
	}
	return o
}
func TestResourceProjectionUsesBusiestBrokerAndAbsoluteUsage(t *testing.T) {
	p := testPolicy(t)
	at := time.Unix(10000, 0)
	o := engineObservation(p, at, 0)
	o.Samples[0].CPU = .1
	o.Samples[1].Memory = 300 * 1024 * 1024
	cpu := ResourceSignal(p, 0, o)
	memory := ResourceSignal(p, 1, o)
	if cpu.Profile != 1 || cpu.Value != 100 || memory.Profile != 1 || memory.Value < 117 {
		t.Fatalf("lost busiest broker: %+v %+v", cpu, memory)
	}
	// Resizing the request denominator must not change the projected absolute demand.
	for i := range o.Brokers {
		o.Brokers[i].Effective = p.Profiles["HIGH"].Resources()
	}
	if got := ResourceSignal(p, 0, o); got.Profile != cpu.Profile {
		t.Fatalf("projection oscillated after denominator change: %+v", got)
	}
}
func TestPartialResourceMetricsCanIncreaseButCannotDecrease(t *testing.T) {
	p := testPolicy(t)
	at := time.Unix(10000, 0)
	e := NewEngine(p, at)
	for step := 0; step <= 30; step += 15 {
		o := engineObservation(p, at.Add(time.Duration(step)*time.Second), 0)
		o.Samples = o.Samples[:1]
		o.Samples[0].CPU = .1
		o.Signals = []Signal{ResourceSignal(p, 0, o), ResourceSignal(p, 1, o)}
		d := e.Evaluate(o, false)
		if step == 30 && d.Target != 1 {
			t.Fatalf("partial valid demand failed to increase: %+v", d)
		}
	}
	o := engineObservation(p, at.Add(time.Minute), 1)
	o.Samples = o.Samples[:1]
	o.Signals = []Signal{ResourceSignal(p, 0, o), ResourceSignal(p, 1, o)}
	if d := e.Evaluate(o, false); d.Target != -1 {
		t.Fatalf("partial data permitted reduction: %+v", d)
	}
}
func TestBoundaryJitterAndDuplicateSamples(t *testing.T) {
	p := testPolicy(t)
	p.Order = []string{"LOW", "MEDIUM", "HIGH"}
	p.Profiles["MEDIUM"] = policy.Profile{Requests: policy.Amounts{CPU: "200m", Memory: "256Mi"}, Limits: policy.Amounts{CPU: "1", Memory: "1Gi"}}
	at := time.Unix(10000, 0)
	e := NewEngine(p, at)
	for i, target := range []int{1, 2, 1} {
		o := engineObservation(p, at.Add(time.Duration(i*15)*time.Second), 0)
		o.Signals[0].Profile = target
		d := e.Evaluate(o, false)
		if i == 2 && d.Target != 1 {
			t.Fatalf("lower sustained boundary lost under jitter: %+v", d)
		}
	}
	e = NewEngine(p, at)
	for _, seconds := range []int{0, 15, 30, 45} {
		o := engineObservation(p, at.Add(time.Duration(seconds)*time.Second), 0)
		o.Signals[0].Profile = 1
		o.Signals[0].Timestamp = at
		if d := e.Evaluate(o, false); d.Target != -1 {
			t.Fatalf("cached observation advanced stabilization: %+v", d)
		}
	}
}
func TestDecreaseRequiresFreshCompleteCoverageAndHeadroom(t *testing.T) {
	p := testPolicy(t)
	p.Behavior.ScaleDown.StepIntervalSeconds = 60
	at := time.Unix(10000, 0)
	e := NewEngine(p, at.Add(-time.Hour))
	for seconds := 0; seconds <= 60; seconds += 15 {
		o := engineObservation(p, at.Add(time.Duration(seconds)*time.Second), 1)
		ready, reason := e.DownReady(o, 0)
		if ready != (seconds == 60) {
			t.Fatalf("at %d: ready=%v %s", seconds, ready, reason)
		}
	}
	e = NewEngine(p, at.Add(-time.Hour))
	for seconds := 0; seconds <= 90; seconds += 15 {
		o := engineObservation(p, at.Add(time.Duration(seconds)*time.Second), 1)
		for i := range o.Samples {
			o.Samples[i].Timestamp = at
		}
		if ready, _ := e.DownReady(o, 0); ready {
			t.Fatal("cached samples filled a window")
		}
	}
	e = NewEngine(p, at.Add(-time.Hour))
	for seconds := 0; seconds <= 60; seconds += 15 {
		o := engineObservation(p, at.Add(time.Duration(seconds)*time.Second), 1)
		if seconds == 45 {
			o.Samples[1].Memory = 900 * 1024 * 1024
		}
		if ready, _ := e.DownReady(o, 0); ready {
			t.Fatal("memory peak was forgotten before a full new window")
		}
	}
}
func TestRestartAndMissingMemoryBlockQueueOnlyDecrease(t *testing.T) {
	p := testPolicy(t)
	p.Triggers = []policy.Trigger{{Type: "rabbitmq", Metadata: policy.Metadata{Mode: "QueueLength", Aggregation: "sum", Thresholds: map[string]float64{"HIGH": 10}}}}
	p.Behavior.ScaleDown.StepIntervalSeconds = 60
	at := time.Unix(10000, 0)
	e := NewEngine(p, at.Add(-time.Hour))
	o := engineObservation(p, at, 1)
	o.Samples = nil
	if ready, reason := e.DownReady(o, 0); ready || reason != "FreshMemoryRequired" {
		t.Fatalf("queue-only reduction without memory: %v %s", ready, reason)
	}
	e.Reset(at)
	o = engineObservation(p, at, 1)
	if ready, _ := e.DownReady(o, 0); ready || !e.downSince.IsZero() {
		t.Fatal("restart reused pre-restart source coverage")
	}
}

func TestResourceValidityIsIndependentAndMemoryStillGuardsQueueOnly(t *testing.T) {
	p := testPolicy(t)
	at := time.Unix(10000, 0)
	o := engineObservation(p, at, 0)
	for i := range o.Samples {
		o.Samples[i].CPU = .1
		o.Samples[i].MemoryUnavailable = true
	}
	cpu := ResourceSignal(p, 0, o)
	memory := ResourceSignal(p, 1, o)
	if !cpu.Complete || !cpu.Available || cpu.Profile != 1 || memory.Available || memory.Complete {
		t.Fatalf("CPU wrongly coupled to absent memory: %+v %+v", cpu, memory)
	}
	e := NewEngine(p, at.Add(-time.Hour))
	if ready, _ := e.DownReady(o, 0); ready || !e.downSince.IsZero() {
		t.Fatal("missing memory allowed a decrease window")
	}
	for i := range o.Samples {
		o.Samples[i].CPUUnavailable = true
		o.Samples[i].MemoryUnavailable = false
		o.Samples[i].Memory = 300 * 1024 * 1024
	}
	if signal := ResourceSignal(p, 1, o); !signal.Complete || signal.Profile != 1 {
		t.Fatalf("memory wrongly coupled to absent CPU: %+v", signal)
	}
}

func TestPendingDecreaseCanBeSupersededByCurrentHighProfile(t *testing.T) {
	p := testPolicy(t)
	at := time.Unix(10000, 0)
	e := NewEngine(p, at)
	for _, seconds := range []int{0, 15, 30} {
		o := engineObservation(p, at.Add(time.Duration(seconds)*time.Second), 1)
		o.Signals[0].Profile = 1
		d := e.Evaluate(o, true)
		if seconds == 30 && d.Target != 1 {
			t.Fatalf("current HIGH could not supersede pending LOW: %+v", d)
		}
	}
}
