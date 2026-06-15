// Package scaling holds the pure decision logic ported from the v1
// ScalingEngine (lib/ScalingEngine.js). These functions take no I/O and accept
// an injected `now`, so the parity-critical behaviour is fully table-testable.
package scaling

import (
	"errors"
	"fmt"

	"github.com/ferterahadi/rmq-vertical-scaler/internal/config"
	"github.com/ferterahadi/rmq-vertical-scaler/internal/metrics"
)

// Metrics are the derived numbers a scaling decision is based on.
type Metrics struct {
	TotalMessages int
	MaxQueueDepth int
	MessageRate   float64
	ConsumeRate   float64
	BacklogRate   float64
}

// ErrNoMetrics mirrors v1's thrown "Connection error: Unable to fetch metrics",
// raised when the overview is empty or there are no queues.
var ErrNoMetrics = errors.New("connection error: unable to fetch metrics")

// CalculateScaleProfile derives metrics from the RabbitMQ payloads and selects
// the target profile. overviewOK is false when the overview fetch failed (v1's
// empty `{}`); like v1, an empty queue list is also treated as a skip.
func CalculateScaleProfile(ov metrics.Overview, overviewOK bool, queues []metrics.Queue, cfg *config.Config) (Metrics, string, error) {
	if !overviewOK || len(queues) == 0 {
		return Metrics{}, "", ErrNoMetrics
	}

	maxDepth := 0
	for _, q := range queues {
		if q.Messages > maxDepth {
			maxDepth = q.Messages
		}
	}

	m := Metrics{
		TotalMessages: ov.QueueTotals.Messages,
		MaxQueueDepth: maxDepth,
		MessageRate:   ov.MessageStats.PublishDetails.Rate,
		ConsumeRate:   ov.MessageStats.DeliverGetDetails.Rate,
	}
	m.BacklogRate = m.MessageRate - m.ConsumeRate

	// Start at the floor; scan high->low and take the first profile whose queue
	// OR rate threshold is exceeded. A zero/absent threshold is skipped (v1's
	// `threshold && value > threshold` falsy-guard).
	profile := cfg.ProfileNames[0]
	for i := len(cfg.ProfileNames) - 1; i > 0; i-- {
		name := cfg.ProfileNames[i]
		qt := cfg.QueueThresholds[name]
		rt := cfg.RateThresholds[name]
		if (qt != 0 && m.MaxQueueDepth > qt) || (rt != 0 && m.MessageRate > float64(rt)) {
			profile = name
			break
		}
	}
	return m, profile, nil
}

// GetProfilePriority returns the 1-based position of a profile, or 0 if unknown.
func GetProfilePriority(profile string, profileNames []string) int {
	for i, n := range profileNames {
		if n == profile {
			return i + 1
		}
	}
	return 0
}

// GenerateScalingMessage reproduces v1's tiered, position-based log line.
func GenerateScalingMessage(profile string, profileNames []string) string {
	idx := indexOf(profileNames, profile)
	count := len(profileNames)
	switch {
	case idx == 0:
		return fmt.Sprintf("✅ %s load detected - minimal resources", profile)
	case idx == count-1:
		return fmt.Sprintf("🚨 %s load detected - scaling to maximum resources", profile)
	case float64(idx) > float64(count)/2:
		return fmt.Sprintf("⚠️  %s load detected - scaling up resources", profile)
	default:
		return fmt.Sprintf("📈 %s load detected - moderate scaling", profile)
	}
}

// StabilityState is the persisted debounce tracking (from the ConfigMap).
type StabilityState struct {
	StableProfile string
	StableSince   int64 // unix seconds
}

// Stability reasons, used by the controller for faithful operational logging.
const (
	ReasonChanged    = "changed"    // recommendation differs from tracked profile
	ReasonAtTarget   = "at_target"  // already at the recommended profile
	ReasonDebouncing = "debouncing" // recommended but not stable long enough
	ReasonStable     = "stable"     // stable for the required duration
)

// StabilityDecision separates v1's mixed return-value-plus-side-effect: Stable
// is checkProfileStability's boolean result; ResetTracking is true in exactly
// the two branches where v1 called updateStabilityTracking(recommended).
// TimeStable/NeedSeconds/IsScaleUp expose the debounce-branch numbers so the
// controller can reproduce v1's log lines without recomputing them.
type StabilityDecision struct {
	Stable        bool
	ResetTracking bool
	Reason        string
	TimeStable    int64
	NeedSeconds   int
	IsScaleUp     bool
}

// CheckProfileStability is the pure debounce decision. `now` is unix seconds.
func CheckProfileStability(current, recommended string, state StabilityState, cfg *config.Config, now int64) StabilityDecision {
	// Recommendation changed from what we're tracking: reset timer, not stable.
	if state.StableProfile != recommended {
		return StabilityDecision{Stable: false, ResetTracking: true, Reason: ReasonChanged}
	}
	// Already at the recommended profile: reset timer (in case it oscillated),
	// and report stable.
	if current == recommended {
		return StabilityDecision{Stable: true, ResetTracking: true, Reason: ReasonAtTarget}
	}

	timeStable := now - state.StableSince
	isScaleUp := GetProfilePriority(recommended, cfg.ProfileNames) > GetProfilePriority(current, cfg.ProfileNames)

	need := cfg.ScaleDownDebounceSeconds
	if isScaleUp {
		need = cfg.ScaleUpDebounceSeconds
	}
	d := StabilityDecision{ResetTracking: false, TimeStable: timeStable, NeedSeconds: need, IsScaleUp: isScaleUp}
	if timeStable < int64(need) {
		d.Stable = false
		d.Reason = ReasonDebouncing
		return d
	}
	d.Stable = true
	d.Reason = ReasonStable
	return d
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
