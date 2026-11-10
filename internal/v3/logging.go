package v3

import (
	"context"
	"log/slog"
	"time"
)

// logObservation only reads the result and existing engine windows. In
// particular it must not call Evaluate or DownReady, which advance/reset timers.
func (c *Controller) logObservation(ctx context.Context, state Status, recoveryReason string, tickErr error) {
	if c.Log == nil || !c.Log.Enabled(ctx, slog.LevelDebug) {
		return
	}
	p := c.Backend.Policy
	o := state.Observation
	args := []any{
		"target", state.Target, "statusConfigMap", c.StatusName,
		"pollAt", o.At, "observedAt", o.freshnessTime(), "mode", p.Update.Mode,
		"phase", state.Phase, "reason", state.Reason,
		"decisionReason", state.Decision.Reason,
		"currentProfile", c.profileName(o.Current),
		"recommendedProfile", c.profileName(state.Decision.Recommended),
		"eligibleProfile", c.profileName(state.Decision.Target),
		"observationComplete", o.Complete, "observationReason", o.Reason,
		"pollingSeconds", p.PollingInterval,
		"reconciliationFailed", tickErr != nil,
	}
	if recoveryReason != "" {
		args = append(args, "recoveryReason", recoveryReason)
	}
	signals := make([]map[string]any, 0, len(o.Signals))
	for _, s := range o.Signals {
		signal := map[string]any{
			"trigger": s.Trigger, "type": s.Type, "value": s.Value,
			"profile": c.profileName(s.Profile), "available": s.Available,
			"complete": s.Complete, "reason": s.Reason,
			"fresh": Fresh(o.freshnessTime(), s.Timestamp, p.Behavior.Metrics.MaxAgeSeconds),
		}
		if !s.Timestamp.IsZero() {
			signal["ageSeconds"] = o.freshnessTime().Sub(s.Timestamp).Seconds()
		}
		signals = append(signals, signal)
	}
	args = append(args, "signals", signals, "metricsMaxAgeSeconds", p.Behavior.Metrics.MaxAgeSeconds)

	// Recovery uses a separate engine; report its window while an operation waits.
	engine := c.Engine
	if state.Phase == "Recovering" {
		engine = c.Recovery
	}
	down := map[string]any{
		"requiredSeconds": p.Behavior.ScaleDown.StepIntervalSeconds,
		"started":         !engine.downSince.IsZero(),
		"elapsedSeconds":  elapsedSeconds(o.At, engine.downSince),
	}
	coverage := make([]map[string]any, 0, len(o.Brokers))
	for _, b := range o.Brokers {
		w := engine.memory[b.Name]
		coverage = append(coverage, map[string]any{
			"broker": b.Name, "sampleCoverageSeconds": elapsedSeconds(w.Last, w.First),
			"peakMemoryBytes": w.Peak,
		})
	}
	down["memoryCoverage"] = coverage
	args = append(args, "scaleDownWindow", down)
	up := make([]map[string]any, 0, len(c.Engine.up))
	for i, name := range p.Order {
		if w, ok := c.Engine.up[i]; ok {
			up = append(up, map[string]any{
				"profile": name, "elapsedSeconds": elapsedSeconds(o.At, w.Since),
				"sampleCoverageSeconds": elapsedSeconds(w.LastSample, w.FirstSample),
			})
		}
	}
	args = append(args, "scaleUpRequiredSeconds", p.Behavior.ScaleUp.StabilizationSeconds, "scaleUpWindows", up)
	if state.Phase == "Recovering" && state.Operation != nil && state.Operation.Direction == "up" {
		w := c.recoveryBoundary
		args = append(args, "recoveryUpWindow", map[string]any{
			"elapsedSeconds":        elapsedSeconds(o.At, w.Since),
			"sampleCoverageSeconds": elapsedSeconds(w.LastSample, w.FirstSample),
		})
	}
	if op := state.Operation; op != nil {
		outcomes := make(map[string]string, len(op.Brokers))
		for name, progress := range op.Brokers {
			outcomes[name] = progress.Outcome
		}
		args = append(args, "operation", map[string]any{
			"revision": op.Revision, "target": op.Target, "direction": op.Direction,
			"stage": op.Stage, "path": op.Path, "brokerOutcomes": outcomes,
		})
	}
	if op := state.LastOperation; op != nil && op.CompletedAt.Equal(state.At) {
		args = append(args, "finishedOperation", map[string]any{
			"revision": op.Revision, "target": op.Target, "stage": op.Stage,
			"completedAt": op.CompletedAt, "disruptionObserved": op.DisruptionObserved,
		})
	}
	c.Log.DebugContext(ctx, "controller observation", args...)
}

// Null distinguishes an unknown/mixed current profile or no eligible target
// from a real policy profile name.
func (c *Controller) profileName(index int) any {
	if index < 0 || index >= len(c.Backend.Policy.Order) {
		return nil
	}
	return c.Backend.Policy.Order[index]
}

func elapsedSeconds(end, start time.Time) float64 {
	if start.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Seconds()
}
