package controller

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/config"
	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/metrics"
	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/scaling"
)

type fakeMetrics struct {
	ov      metrics.Overview
	ok      bool
	queues  []metrics.Queue
	waitErr error // returned by WaitForRabbitMQ when set
}

func (f *fakeMetrics) GetQueueMetrics(context.Context) (metrics.Overview, bool) { return f.ov, f.ok }
func (f *fakeMetrics) GetDetailedQueues(context.Context) []metrics.Queue        { return f.queues }
func (f *fakeMetrics) WaitForRabbitMQ(context.Context) error                    { return f.waitErr }

type trackCall struct {
	profile string
	now     int64
}

type fakeKube struct {
	current  string
	state    scaling.StabilityState
	patches  [][2]string
	tracking []trackCall
	patchErr error // returned by ApplyPatch when set
}

func (f *fakeKube) GetCurrentProfile(context.Context) string                 { return f.current }
func (f *fakeKube) GetStabilityState(context.Context) scaling.StabilityState { return f.state }
func (f *fakeKube) UpdateStabilityTracking(_ context.Context, profile string, now int64) {
	f.tracking = append(f.tracking, trackCall{profile, now})
}
func (f *fakeKube) ApplyPatch(_ context.Context, cpu, memory string) error {
	if f.patchErr != nil {
		return f.patchErr
	}
	f.patches = append(f.patches, [2]string{cpu, memory})
	return nil
}

func ctrlCfg() *config.Config {
	return &config.Config{
		ProfileNames: []string{"LOW", "MEDIUM", "HIGH", "CRITICAL"},
		Profiles: map[string]config.Profile{
			"LOW":      {CPU: "330m", Memory: "2Gi"},
			"MEDIUM":   {CPU: "800m", Memory: "3Gi"},
			"HIGH":     {CPU: "1600m", Memory: "4Gi"},
			"CRITICAL": {CPU: "2400m", Memory: "8Gi"},
		},
		QueueThresholds:          map[string]int{"MEDIUM": 2000, "HIGH": 10000, "CRITICAL": 50000},
		RateThresholds:           map[string]int{"MEDIUM": 200, "HIGH": 1000, "CRITICAL": 2000},
		ScaleUpDebounceSeconds:   30,
		ScaleDownDebounceSeconds: 120,
	}
}

func ovWith(total int, pub, del float64) metrics.Overview {
	var o metrics.Overview
	o.QueueTotals.Messages = total
	o.MessageStats.PublishDetails.Rate = pub
	o.MessageStats.DeliverGetDetails.Rate = del
	return o
}

func newCtrl(m MetricsClient, k KubeClient, now int64) *Controller {
	c := New(ctrlCfg(), m, k, log.New(io.Discard, "", 0))
	c.now = func() int64 { return now }
	return c
}

func TestApplyScaleScalesWhenStableAndDifferent(t *testing.T) {
	const now int64 = 1000
	m := &fakeMetrics{ov: ovWith(15000, 500, 300), ok: true, queues: []metrics.Queue{{Messages: 12000}}} // -> HIGH
	k := &fakeKube{current: "MEDIUM", state: scaling.StabilityState{StableProfile: "HIGH", StableSince: now - 100}}

	if err := newCtrl(m, k, now).ApplyScale(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(k.patches) != 1 || k.patches[0] != [2]string{"1600m", "4Gi"} {
		t.Errorf("patches = %v, want one HIGH patch (1600m/4Gi)", k.patches)
	}
	// debounce branch (stable) does not reset before patch; one reset after patch.
	if len(k.tracking) != 1 || k.tracking[0].profile != "HIGH" {
		t.Errorf("tracking = %v, want one HIGH update", k.tracking)
	}
}

func TestApplyScaleSkipsWhenAlreadyAtTarget(t *testing.T) {
	const now int64 = 1000
	m := &fakeMetrics{ov: ovWith(15000, 500, 300), ok: true, queues: []metrics.Queue{{Messages: 12000}}} // -> HIGH
	k := &fakeKube{current: "HIGH", state: scaling.StabilityState{StableProfile: "HIGH", StableSince: now - 100}}

	if err := newCtrl(m, k, now).ApplyScale(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(k.patches) != 0 {
		t.Errorf("patches = %v, want none (already at target)", k.patches)
	}
	// "already at target" resets tracking once.
	if len(k.tracking) != 1 || k.tracking[0].profile != "HIGH" {
		t.Errorf("tracking = %v, want one HIGH reset", k.tracking)
	}
}

func TestApplyScaleDebouncingDoesNotScaleOrReset(t *testing.T) {
	const now int64 = 1000
	m := &fakeMetrics{ov: ovWith(3000, 100, 80), ok: true, queues: []metrics.Queue{{Messages: 2500}}} // -> MEDIUM
	k := &fakeKube{current: "LOW", state: scaling.StabilityState{StableProfile: "MEDIUM", StableSince: now - 10}}

	if err := newCtrl(m, k, now).ApplyScale(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(k.patches) != 0 {
		t.Errorf("patches = %v, want none (debouncing)", k.patches)
	}
	if len(k.tracking) != 0 {
		t.Errorf("tracking = %v, want none (debounce branch does not reset)", k.tracking)
	}
}

func TestApplyScaleResetsWhenRecommendationChanged(t *testing.T) {
	const now int64 = 1000
	m := &fakeMetrics{ov: ovWith(3000, 100, 80), ok: true, queues: []metrics.Queue{{Messages: 2500}}} // -> MEDIUM
	k := &fakeKube{current: "LOW", state: scaling.StabilityState{StableProfile: "LOW", StableSince: now - 500}}

	if err := newCtrl(m, k, now).ApplyScale(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(k.patches) != 0 {
		t.Errorf("patches = %v, want none (recommendation just changed)", k.patches)
	}
	if len(k.tracking) != 1 || k.tracking[0].profile != "MEDIUM" || k.tracking[0].now != now {
		t.Errorf("tracking = %v, want one MEDIUM reset at now", k.tracking)
	}
}

func TestApplyScaleReturnsErrOnNoMetrics(t *testing.T) {
	const now int64 = 1000
	m := &fakeMetrics{ok: false} // connection error -> empty overview
	k := &fakeKube{current: "LOW"}

	if err := newCtrl(m, k, now).ApplyScale(context.Background()); err != scaling.ErrNoMetrics {
		t.Errorf("err = %v, want ErrNoMetrics", err)
	}
	if len(k.patches) != 0 {
		t.Errorf("patches = %v, want none", k.patches)
	}
}

func TestApplyScaleSwallowsPatchError(t *testing.T) {
	const now int64 = 1000
	m := &fakeMetrics{ov: ovWith(15000, 500, 300), ok: true, queues: []metrics.Queue{{Messages: 12000}}} // -> HIGH
	k := &fakeKube{
		current:  "MEDIUM",
		state:    scaling.StabilityState{StableProfile: "HIGH", StableSince: now - 100},
		patchErr: errors.New("boom"),
	}
	// v1 catches a patch failure and continues the loop; ApplyScale returns nil.
	if err := newCtrl(m, k, now).ApplyScale(context.Background()); err != nil {
		t.Errorf("err = %v, want nil (patch error swallowed)", err)
	}
}

func TestApplyScaleUnknownProfileFallsBackToFloor(t *testing.T) {
	const now int64 = 1000
	// ProfileNames lists GHOST but Profiles has no entry for it -> fallback to floor.
	cfg := &config.Config{
		ProfileNames:             []string{"LOW", "GHOST"},
		Profiles:                 map[string]config.Profile{"LOW": {CPU: "330m", Memory: "2Gi"}},
		QueueThresholds:          map[string]int{"GHOST": 100},
		RateThresholds:           map[string]int{"GHOST": 10},
		ScaleUpDebounceSeconds:   30,
		ScaleDownDebounceSeconds: 120,
	}
	m := &fakeMetrics{ov: ovWith(500, 5, 1), ok: true, queues: []metrics.Queue{{Messages: 200}}} // -> GHOST
	k := &fakeKube{current: "LOW", state: scaling.StabilityState{StableProfile: "LOW", StableSince: now - 100}}

	c := New(cfg, m, k, log.New(io.Discard, "", 0))
	c.now = func() int64 { return now }
	if err := c.ApplyScale(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
	// Fell back to LOW (the floor) and is already there -> no patch.
	if len(k.patches) != 0 {
		t.Errorf("patches = %v, want none after fallback to floor", k.patches)
	}
}

func TestNewWithNilLoggerDefaults(t *testing.T) {
	// Covers the `logger == nil` branch in New (falls back to log.Default()).
	c := New(ctrlCfg(), &fakeMetrics{}, &fakeKube{}, nil)
	if c == nil || c.logger == nil {
		t.Fatal("New returned nil controller/logger")
	}
}

func TestApplyScaleUsesRealClockByDefault(t *testing.T) {
	// Build via New without overriding c.now, so the real-clock closure runs.
	m := &fakeMetrics{ov: ovWith(100, 1, 1), ok: true, queues: []metrics.Queue{{Messages: 1}}} // -> LOW
	k := &fakeKube{current: "LOW", state: scaling.StabilityState{StableProfile: "LOW", StableSince: 0}}
	c := New(ctrlCfg(), m, k, log.New(io.Discard, "", 0))
	if err := c.ApplyScale(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
	// Already at target -> resets tracking with a real (positive) unix timestamp.
	if len(k.tracking) != 1 || k.tracking[0].now <= 0 {
		t.Errorf("tracking = %v, want one reset with a real-clock timestamp", k.tracking)
	}
}

func TestRunLogsApplyScaleError(t *testing.T) {
	m := &fakeMetrics{ok: false} // ApplyScale -> ErrNoMetrics every iteration
	c := newCtrl(m, &fakeKube{current: "LOW"}, 1000)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx); err != nil {
		t.Errorf("Run = %v, want nil (errors are logged, loop continues)", err)
	}
}

func TestRunReturnsWaitError(t *testing.T) {
	m := &fakeMetrics{waitErr: errors.New("dial failed")}
	c := newCtrl(m, &fakeKube{current: "LOW"}, 1000)
	// ctx is not cancelled, so a wait failure propagates as an error.
	if err := c.Run(context.Background()); err == nil {
		t.Error("Run = nil, want the WaitForRabbitMQ error")
	}
}

func TestRunWaitErrorWithCancelledCtxIsGraceful(t *testing.T) {
	m := &fakeMetrics{waitErr: context.Canceled}
	c := newCtrl(m, &fakeKube{current: "LOW"}, 1000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // ctx.Err() != nil -> graceful shutdown despite the wait error
	if err := c.Run(ctx); err != nil {
		t.Errorf("Run = %v, want nil (graceful shutdown)", err)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	m := &fakeMetrics{ov: ovWith(100, 5, 1), ok: true, queues: []metrics.Queue{{Messages: 10}}}
	k := &fakeKube{current: "LOW", state: scaling.StabilityState{StableProfile: "LOW", StableSince: 0}}
	c := newCtrl(m, k, 1000)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, want nil on graceful shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}
