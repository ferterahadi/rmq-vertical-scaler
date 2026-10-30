// Package v2 implements the opt-in version-2 controller. The legacy controller
// never uses these observations, timers, or convergence rules.
package v2

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/policy"
	corev1 "k8s.io/api/core/v1"
)

type Sample struct {
	CPUUnavailable    bool          `json:"cpuUnavailable,omitempty"`
	MemoryUnavailable bool          `json:"memoryUnavailable,omitempty"`
	Pod               string        `json:"pod"`
	UID               string        `json:"uid"`
	ContainerID       string        `json:"containerID"`
	Timestamp         time.Time     `json:"timestamp"`
	Window            time.Duration `json:"window"`
	CPU               float64       `json:"cpuCores"`
	Memory            float64       `json:"memoryBytes"`
}
type Signal struct {
	Available bool      `json:"available"`
	Trigger   int       `json:"trigger"`
	Type      string    `json:"type"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
	Complete  bool      `json:"complete"`
	Reason    string    `json:"reason,omitempty"`
	Value     float64   `json:"value"`
	Profile   int       `json:"profile"`
	Saturated bool      `json:"saturated,omitempty"`
}
type Broker struct {
	Pod               corev1.Pod                  `json:"-"`
	Name              string                      `json:"name"`
	UID               string                      `json:"uid"`
	ContainerID       string                      `json:"containerID"`
	Restarts          int32                       `json:"restarts"`
	Ready             bool                        `json:"ready"`
	CurrentGeneration bool                        `json:"currentGeneration"`
	Desired           corev1.ResourceRequirements `json:"desired"`
	Effective         corev1.ResourceRequirements `json:"effective"`
	Conditions        []corev1.PodCondition       `json:"resizeConditions,omitempty"`
	Started           time.Time                   `json:"started"`
}
type Observation struct {
	At       time.Time `json:"at"`
	Identity string    `json:"identity"`
	Complete bool      `json:"complete"`
	Reason   string    `json:"reason,omitempty"`
	Brokers  []Broker  `json:"brokers"`
	Samples  []Sample  `json:"samples"`
	Signals  []Signal  `json:"signals"`
	Current  int       `json:"currentProfileIndex"`
}
type Decision struct {
	Recommended  int       `json:"recommendedProfileIndex"`
	Target       int       `json:"eligibleProfileIndex"`
	Reason       string    `json:"reason"`
	EpisodeStart time.Time `json:"episodeStart,omitempty"`
}
type boundary struct {
	Since       time.Time
	FirstSample time.Time
	LastSample  time.Time
}
type memoryWindow struct {
	First time.Time
	Last  time.Time
	Peak  float64
}
type Engine struct {
	Policy     *policy.Config
	identity   string
	up         map[int]boundary
	downSince  time.Time
	downTarget int
	memory     map[string]memoryWindow
	boot       time.Time
}

func NewEngine(p *policy.Config, boot time.Time) *Engine {
	return &Engine{Policy: p, up: map[int]boundary{}, memory: map[string]memoryWindow{}, downTarget: -1, boot: boot}
}
func (e *Engine) ResetDown() {
	e.downSince = time.Time{}
	e.downTarget = -1
	e.memory = map[string]memoryWindow{}
}
func (e *Engine) Reset(at time.Time) { e.up = map[int]boundary{}; e.ResetDown(); e.boot = at }

func Fresh(at, stamp time.Time, maxAge int) bool {
	age := at.Sub(stamp)
	return !stamp.IsZero() && age >= 0 && age <= time.Duration(maxAge)*time.Second
}

// ResourceSignal projects absolute usage onto candidate requests. It avoids
// multiplying an already changed request denominator by a previous ratio.
func ResourceSignal(p *policy.Config, index int, o Observation) Signal {
	t := p.Triggers[index]
	s := Signal{Trigger: index, Type: t.Type, Source: "metrics.k8s.io", Profile: 0, Complete: o.Complete}
	target, _ := strconv.ParseFloat(t.Metadata.Value, 64)
	samples := map[string]Sample{}
	for _, v := range o.Samples {
		if _, exists := samples[v.Pod]; exists {
			s.Complete = false
			s.Reason = "DuplicateResourceMetrics"
			return s
		}
		samples[v.Pod] = v
	}
	validCount := 0
	for _, b := range o.Brokers {
		m, ok := samples[b.Name]
		if !ok || m.UID != b.UID || m.ContainerID != b.ContainerID || !Fresh(o.At, m.Timestamp, p.Behavior.Metrics.MaxAgeSeconds) || m.Timestamp.Before(b.Started) || m.Window <= 0 || m.Timestamp.Add(-m.Window).Before(b.Started) || !policy.Known(b.Effective) {
			s.Complete = false
			continue
		}
		if (t.Type == "cpu" && m.CPUUnavailable) || (t.Type == "memory" && m.MemoryUnavailable) {
			s.Complete = false
			continue
		}
		usage := m.CPU
		request := b.Effective.Requests.Cpu().AsApproximateFloat64()
		key := corev1.ResourceCPU
		if t.Type == "memory" {
			usage = m.Memory
			request = b.Effective.Requests.Memory().AsApproximateFloat64()
			key = corev1.ResourceMemory
		}
		if math.IsNaN(usage) || math.IsInf(usage, 0) || usage < 0 || request <= 0 {
			s.Complete = false
			continue
		}
		validCount++
		s.Value = math.Max(s.Value, 100*usage/request)
		selected := len(p.Order) - 1
		fits := false
		for i, name := range p.Order {
			r := p.Profiles[name].Resources()
			q := r.Requests[key]
			if 100*usage/q.AsApproximateFloat64() <= target {
				selected = i
				fits = true
				break
			}
		}
		if selected > s.Profile {
			s.Profile = selected
		}
		s.Saturated = s.Saturated || !fits
		if s.Timestamp.IsZero() || m.Timestamp.Before(s.Timestamp) {
			s.Timestamp = m.Timestamp
		}
	}
	s.Available = validCount > 0
	s.Complete = s.Complete && validCount == len(o.Brokers) && len(o.Brokers) > 0
	if !s.Complete {
		s.Reason = "IncompleteOrStaleResourceMetrics"
	}
	return s
}

// Evaluate calculates recommendations and permission independently of I/O and
// update mode. The caller enforces Off and pending-operation policy.
func (e *Engine) Evaluate(o Observation, pending bool) Decision {
	if o.Identity != e.identity {
		e.Reset(o.At)
		e.identity = o.Identity
	}
	d := Decision{Recommended: -1, Target: -1, Reason: "NoValidSignals"}
	all := o.Complete && len(o.Signals) == len(e.Policy.Triggers)
	for _, s := range o.Signals {
		valid := s.Available && Fresh(o.At, s.Timestamp, e.Policy.Behavior.Metrics.MaxAgeSeconds)
		all = all && s.Complete && valid
		if valid && s.Profile > d.Recommended {
			d.Recommended = s.Profile
		}
	}
	if !o.Complete {
		e.up = map[int]boundary{}
		e.ResetDown()
		d.Reason = "IncompleteBrokerCensus"
		return d
	}
	// A boundary survives recommendation jitter only while some valid signal
	// continues to support it. Duplicate source samples cannot advance it.
	for i := 1; i < len(e.Policy.Order); i++ {
		stamp := time.Time{}
		for _, s := range o.Signals {
			if s.Available && s.Profile >= i && Fresh(o.At, s.Timestamp, e.Policy.Behavior.Metrics.MaxAgeSeconds) && s.Timestamp.After(stamp) {
				stamp = s.Timestamp
			}
		}
		if stamp.IsZero() {
			delete(e.up, i)
			continue
		}
		b, exists := e.up[i]
		if !exists {
			b = boundary{Since: o.At, FirstSample: stamp}
		}
		if stamp.After(b.LastSample) {
			b.LastSample = stamp
		}
		e.up[i] = b
	}
	if d.Recommended < 0 {
		e.ResetDown()
		return d
	}
	minimum := o.Current
	// Pending downward intent may not have changed live resources yet. A fresh
	// sustained recommendation for the current profile must still supersede it.
	if pending {
		minimum--
	}
	for i := d.Recommended; i > minimum && i > 0; i-- {
		b, ok := e.up[i]
		if !ok {
			continue
		}
		duration := time.Duration(e.Policy.Behavior.ScaleUp.StabilizationSeconds) * time.Second
		if o.At.Sub(b.Since) >= duration && b.LastSample.Sub(b.FirstSample) >= duration {
			target := e.Policy.Profiles[e.Policy.Order[i]].Resources()
			safe := o.Complete && len(o.Brokers) > 0
			for _, broker := range o.Brokers {
				safe = safe && policy.Dominates(target, broker.Desired) && policy.Dominates(target, broker.Effective)
			}
			if safe {
				d.Target = i
				d.Reason = "SustainedIncrease"
				d.EpisodeStart = b.Since
				e.ResetDown()
				return d
			}
			d.Reason = "UnknownOrMixedResources"
		}
	}
	if pending {
		e.ResetDown()
		d.Reason = "OperationUnresolved"
		return d
	}
	if o.Current <= 0 || d.Recommended >= o.Current || !all {
		e.ResetDown()
		if !all {
			d.Reason = "IncompleteSignalsBlockDecrease"
		} else {
			d.Reason = "HoldOrStabilizing"
		}
		return d
	}
	target := o.Current - 1
	ready, reason := e.DownReady(o, target)
	d.Reason = reason
	if ready {
		d.Target = target
		d.Reason = "SafeOneStepDecrease"
	}
	return d
}

// DownReady also serves retained downward operations after restart. The caller
// may collect this recovery window while the retained operation is unresolved.
func (e *Engine) DownReady(o Observation, target int) (bool, string) {
	if !o.Complete || target < 0 || target >= len(e.Policy.Order) {
		e.ResetDown()
		return false, "IncompleteBrokerCensus"
	}
	if len(o.Signals) != len(e.Policy.Triggers) {
		e.ResetDown()
		return false, "IncompleteSignalsBlockDecrease"
	}
	for _, s := range o.Signals {
		if !s.Complete || s.Profile > target || !Fresh(o.At, s.Timestamp, e.Policy.Behavior.Metrics.MaxAgeSeconds) {
			e.ResetDown()
			return false, "SignalsBlockDecrease"
		}
	}
	if e.downTarget != target {
		e.ResetDown()
		e.downTarget = target
	}
	if len(o.Samples) != len(o.Brokers) || len(o.Brokers) == 0 {
		e.ResetDown()
		return false, "FreshMemoryRequired"
	}
	samples := map[string]Sample{}
	for _, s := range o.Samples {
		samples[s.Pod] = s
	}
	r := e.Policy.Profiles[e.Policy.Order[target]].Resources()
	ceiling := r.Limits.Memory().AsApproximateFloat64() * float64(100-e.Policy.Behavior.ScaleDown.MemoryHeadroomPercent) / 100
	for _, b := range o.Brokers {
		s, ok := samples[b.Name]
		if !ok || !b.Ready || s.MemoryUnavailable || s.Window <= 0 || s.Timestamp.Add(-s.Window).Before(b.Started) || !s.Timestamp.After(e.boot) || s.Timestamp.Add(-s.Window).Before(e.boot) || !Fresh(o.At, s.Timestamp, e.Policy.Behavior.Metrics.MaxAgeSeconds) || s.UID != b.UID || s.ContainerID != b.ContainerID || s.Timestamp.Before(b.Started) || s.Memory < 0 || math.IsNaN(s.Memory) || math.IsInf(s.Memory, 0) {
			e.ResetDown()
			return false, "FreshMemoryRequired"
		}
		w := e.memory[b.Name]
		if !w.Last.IsZero() && s.Timestamp.Sub(w.Last) > time.Duration(e.Policy.Behavior.Metrics.MaxAgeSeconds)*time.Second {
			e.ResetDown()
			return false, "MemoryCoverageGap"
		}
		if s.Memory > ceiling {
			e.ResetDown()
			return false, "MemoryHeadroomRequired"
		}
		if w.First.IsZero() {
			w.First = s.Timestamp
		}
		if s.Timestamp.After(w.Last) {
			w.Last = s.Timestamp
			w.Peak = math.Max(w.Peak, s.Memory)
		}
		e.memory[b.Name] = w
	}
	if e.downSince.IsZero() {
		e.downSince = o.At
	}
	duration := time.Duration(e.Policy.Behavior.ScaleDown.StepIntervalSeconds) * time.Second
	if o.At.Sub(e.downSince) < duration {
		return false, "ObservingDecrease"
	}
	for _, b := range o.Brokers {
		w := e.memory[b.Name]
		if w.Last.Sub(w.First) < duration || w.Peak > ceiling {
			return false, "ObservingMemoryCoverage"
		}
	}
	return true, fmt.Sprintf("SafeDecreaseTo%s", e.Policy.Order[target])
}
