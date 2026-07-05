// Package controller wires config, metrics, the scaling engine, and the
// Kubernetes client into the control loop. It is a port of v1's
// RabbitMQVerticalScaler.js (applyScale + main) and index.js (signal handling),
// preserving the same step order and log lines.
package controller

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/config"
	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/metrics"
	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/scaling"
)

// KubeClient is the subset of the Kubernetes client the controller needs.
// Defining it here (consumer side) keeps the controller unit-testable with
// fakes and free of a client-go dependency.
type KubeClient interface {
	GetCurrentProfile(ctx context.Context) string
	GetStabilityState(ctx context.Context) scaling.StabilityState
	UpdateStabilityTracking(ctx context.Context, profile string, now int64)
	ApplyPatch(ctx context.Context, cpu, memory string) error

	// In-place resize (v2.2.0). EffectiveScaleMode resolves SCALE_MODE against
	// cluster capability; the rest only run when it resolves to inplace.
	EffectiveScaleMode() string
	EnsureOnDeleteStrategy(ctx context.Context) error
	ResizePods(ctx context.Context, cpu, memory string) error
	SetRollingUpdateStrategy(ctx context.Context) error
	ResignalWatermark(ctx context.Context, memory string)
}

// MetricsClient is the subset of the metrics collector the controller needs.
type MetricsClient interface {
	GetQueueMetrics(ctx context.Context) (metrics.Overview, bool)
	GetDetailedQueues(ctx context.Context) []metrics.Queue
	WaitForRabbitMQ(ctx context.Context) error
}

// Controller runs the scaling loop.
type Controller struct {
	cfg     *config.Config
	metrics MetricsClient
	kube    KubeClient
	logger  *log.Logger
	now     func() int64 // unix seconds; injectable for tests
	mode    string       // effective scale mode, resolved lazily from kube
}

// New builds a Controller with a real wall-clock.
func New(cfg *config.Config, m MetricsClient, k KubeClient, logger *log.Logger) *Controller {
	if logger == nil {
		logger = log.Default()
	}
	return &Controller{
		cfg:     cfg,
		metrics: m,
		kube:    k,
		logger:  logger,
		now:     func() int64 { return time.Now().Unix() },
	}
}

// ApplyScale performs one scaling evaluation, mirroring v1's applyScale order.
// It returns ErrNoMetrics when metrics are unavailable (the loop logs and
// continues); a normal early-return (not stable / already at target) yields nil.
func (c *Controller) ApplyScale(ctx context.Context) error {
	c.logger.Println("🔍 Analyzing RabbitMQ metrics...")

	ov, ok := c.metrics.GetQueueMetrics(ctx)
	queues := c.metrics.GetDetailedQueues(ctx)
	m, profile, err := scaling.CalculateScaleProfile(ov, ok, queues, c.cfg)
	if err != nil {
		c.logger.Println("[WARNING] Unable to get overview and queues due to connection error, skipping scaling")
		return err
	}

	c.logger.Printf("📊 Queue Depth: %d | Total: %d | Publish: %g/s | Consume: %g/s | Backlog: %g/s",
		m.MaxQueueDepth, m.TotalMessages, m.MessageRate, m.ConsumeRate, m.BacklogRate)

	currentProfile := c.kube.GetCurrentProfile(ctx)
	c.logger.Printf("📍 Current profile: %s", currentProfile)

	resources, exists := c.cfg.Profiles[profile]
	if !exists {
		c.logger.Printf("❓ Unknown profile '%s', defaulting to %s", profile, c.cfg.ProfileNames[0])
		profile = c.cfg.ProfileNames[0]
		resources = c.cfg.Profiles[profile]
	}

	c.logger.Println(scaling.GenerateScalingMessage(profile, c.cfg.ProfileNames))

	// Always evaluate stability so the timer resets when the recommendation
	// changes, exactly as v1 does.
	now := c.now()
	state := c.kube.GetStabilityState(ctx)
	c.logger.Printf("🔍 Checking profile stability: current=%s, recommended=%s", currentProfile, profile)
	decision := scaling.CheckProfileStability(currentProfile, profile, state, c.cfg, now)
	c.logStability(currentProfile, profile, state, decision)

	if decision.ResetTracking {
		c.kube.UpdateStabilityTracking(ctx, profile, now)
	}
	if !decision.Stable {
		c.logger.Println("⏸️  Profile not stable long enough yet")
		return nil
	}

	// Skip if already at the target profile.
	if currentProfile == profile {
		c.logger.Printf("⏸️  Scaling skipped - already at %s profile", profile)
		return nil
	}

	c.logger.Printf("⚙️  Applying: CPU=%s, Memory=%s", resources.CPU, resources.Memory)
	if err := c.applyResources(ctx, resources); err != nil {
		// v1 catches and continues the loop.
		c.logger.Printf("❌ Scaling failed: %v", err)
		return nil
	}
	// Reset stability tracking since we just scaled.
	c.kube.UpdateStabilityTracking(ctx, profile, c.now())
	return nil
}

// applyResources applies a profile through the path the effective scale mode
// selects. Rolling is the v2.0.0 behaviour: one CR patch, the operator rolls
// pods. In-place resizes the pods directly (no restart) and then patches the
// CR too, keeping it the source of truth — under the OnDelete override that
// patch does not roll anything. An Infeasible resize in auto mode reverts the
// override and falls back to a rolling scale for this action.
func (c *Controller) applyResources(ctx context.Context, r config.Profile) error {
	if c.scaleMode() != config.ScaleModeInPlace {
		return c.kube.ApplyPatch(ctx, r.CPU, r.Memory)
	}

	if err := c.kube.EnsureOnDeleteStrategy(ctx); err != nil {
		return err
	}
	if err := c.kube.ResizePods(ctx, r.CPU, r.Memory); err != nil {
		if c.cfg.ScaleMode == config.ScaleModeAuto && errors.Is(err, scaling.ErrResizeInfeasible) {
			c.logger.Println("↩️  In-place resize infeasible on node; falling back to a rolling scale")
			if serr := c.kube.SetRollingUpdateStrategy(ctx); serr != nil {
				return serr
			}
			return c.kube.ApplyPatch(ctx, r.CPU, r.Memory)
		}
		return err
	}
	if err := c.kube.ApplyPatch(ctx, r.CPU, r.Memory); err != nil {
		return err
	}
	// Pods were resized live; RabbitMQ only reads total memory at boot, so
	// optionally tell it about the new size (best-effort, flag-gated).
	if c.cfg.WatermarkResignal {
		c.kube.ResignalWatermark(ctx, r.Memory)
	}
	return nil
}

// scaleMode resolves the effective mode once and caches it for the loop's
// lifetime (capability discovery doesn't change under a running process).
func (c *Controller) scaleMode() string {
	if c.mode == "" {
		c.mode = c.kube.EffectiveScaleMode()
	}
	return c.mode
}

// logStability reproduces v1's per-branch stability log lines.
func (c *Controller) logStability(current, recommended string, state scaling.StabilityState, d scaling.StabilityDecision) {
	switch d.Reason {
	case scaling.ReasonChanged:
		c.logger.Printf("🔄 Profile recommendation changed from %s to %s", state.StableProfile, recommended)
	case scaling.ReasonAtTarget:
		c.logger.Printf("✅ Already at recommended profile: %s", recommended)
	case scaling.ReasonDebouncing:
		remaining := int64(d.NeedSeconds) - d.TimeStable
		kind := "Scale-down"
		if d.IsScaleUp {
			kind = "Scale-up"
		}
		c.logger.Printf("⏳ %s debounce: %s stable for %ds, need %ds (%ds remaining)",
			kind, recommended, d.TimeStable, d.NeedSeconds, remaining)
	case scaling.ReasonStable:
		c.logger.Println("✅ Profile has been stable for required duration")
	}
}

// Run waits for RabbitMQ, then evaluates scaling every CheckIntervalSeconds
// until ctx is cancelled (SIGTERM/SIGINT).
func (c *Controller) Run(ctx context.Context) error {
	c.logger.Println("🚀 RabbitMQ Vertical Scaler (Go)")

	if err := c.metrics.WaitForRabbitMQ(ctx); err != nil {
		if ctx.Err() != nil {
			c.logger.Println("📴 Shutting down gracefully")
			return nil
		}
		return err
	}

	interval := time.Duration(c.cfg.CheckIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := c.ApplyScale(ctx); err != nil {
			c.logger.Printf("Error in scaling loop: %v", err)
		} else {
			c.logger.Println("---")
		}

		select {
		case <-ctx.Done():
			c.logger.Println("📴 Shutting down gracefully")
			return nil
		case <-ticker.C:
		}
	}
}
