package scaling

import (
	"strings"
	"testing"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/config"
	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/metrics"
)

// testCfg mirrors the mock config used by the v1 ScalingEngine tests.
func testCfg() *config.Config {
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

func ov(total int, pub, del float64) metrics.Overview {
	var o metrics.Overview
	o.QueueTotals.Messages = total
	o.MessageStats.PublishDetails.Rate = pub
	o.MessageStats.DeliverGetDetails.Rate = del
	return o
}

func qs(depths ...int) []metrics.Queue {
	out := make([]metrics.Queue, len(depths))
	for i, d := range depths {
		out[i] = metrics.Queue{Messages: d}
	}
	return out
}

func TestCalculateScaleProfile(t *testing.T) {
	cfg := testCfg()
	cases := []struct {
		name      string
		ov        metrics.Overview
		queues    []metrics.Queue
		want      string
		wantDepth int
		wantRate  float64
	}{
		{"LOW minimal load", ov(100, 50, 45), qs(50, 50), "LOW", 50, 50},
		{"MEDIUM via queue", ov(3000, 100, 80), qs(2500, 500), "MEDIUM", 2500, 100},
		{"MEDIUM via rate", ov(500, 250, 200), qs(300, 200), "MEDIUM", 300, 250},
		{"HIGH load", ov(15000, 500, 300), qs(12000, 3000), "HIGH", 12000, 500},
		{"CRITICAL load", ov(60000, 2500, 1000), qs(55000, 5000), "CRITICAL", 55000, 2500},
		{"highest applicable wins", ov(60000, 1500, 500), qs(55000), "CRITICAL", 55000, 1500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, profile, err := CalculateScaleProfile(tc.ov, true, tc.queues, cfg)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if profile != tc.want {
				t.Errorf("profile = %q, want %q", profile, tc.want)
			}
			if m.MaxQueueDepth != tc.wantDepth {
				t.Errorf("maxQueueDepth = %d, want %d", m.MaxQueueDepth, tc.wantDepth)
			}
			if m.MessageRate != tc.wantRate {
				t.Errorf("messageRate = %v, want %v", m.MessageRate, tc.wantRate)
			}
		})
	}
}

func TestCalculateScaleProfileThresholdBoundary(t *testing.T) {
	cfg := testCfg()
	// Exactly at the MEDIUM queue threshold (2000) must NOT trigger (strict >).
	_, profile, err := CalculateScaleProfile(ov(0, 0, 0), true, qs(2000), cfg)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if profile != "LOW" {
		t.Errorf("at-threshold depth=2000 -> %q, want LOW (strict >)", profile)
	}
	// One above the threshold triggers MEDIUM.
	_, profile, _ = CalculateScaleProfile(ov(0, 0, 0), true, qs(2001), cfg)
	if profile != "MEDIUM" {
		t.Errorf("depth=2001 -> %q, want MEDIUM", profile)
	}
}

func TestCalculateScaleProfileBacklog(t *testing.T) {
	cfg := testCfg()
	m, _, err := CalculateScaleProfile(ov(1000, 100, 60), true, qs(1000), cfg)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if m.BacklogRate != 40 {
		t.Errorf("backlog = %v, want 40", m.BacklogRate)
	}
}

func TestCalculateScaleProfilePartialMetrics(t *testing.T) {
	cfg := testCfg()
	// Overview with only queue_totals (no message_stats) -> zero rates.
	o := metrics.Overview{}
	o.QueueTotals.Messages = 500
	m, _, err := CalculateScaleProfile(o, true, qs(300, 200), cfg)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if m.MessageRate != 0 || m.ConsumeRate != 0 || m.BacklogRate != 0 {
		t.Errorf("rates = %v/%v/%v, want 0/0/0", m.MessageRate, m.ConsumeRate, m.BacklogRate)
	}
}

func TestCalculateScaleProfileNoMetrics(t *testing.T) {
	cfg := testCfg()
	// overviewOK=false (connection error)
	if _, _, err := CalculateScaleProfile(metrics.Overview{}, false, qs(100), cfg); err != ErrNoMetrics {
		t.Errorf("err = %v, want ErrNoMetrics", err)
	}
	// empty queue list
	if _, _, err := CalculateScaleProfile(ov(100, 1, 1), true, nil, cfg); err != ErrNoMetrics {
		t.Errorf("err = %v, want ErrNoMetrics", err)
	}
}

func TestGetProfilePriority(t *testing.T) {
	names := []string{"LOW", "MEDIUM", "HIGH", "CRITICAL"}
	for profile, want := range map[string]int{"LOW": 1, "MEDIUM": 2, "HIGH": 3, "CRITICAL": 4, "UNKNOWN": 0, "": 0} {
		if got := GetProfilePriority(profile, names); got != want {
			t.Errorf("priority(%q) = %d, want %d", profile, got, want)
		}
	}
}

func TestGenerateScalingMessage(t *testing.T) {
	names := []string{"LOW", "MEDIUM", "HIGH", "CRITICAL"}
	cases := map[string]string{
		"LOW":      "minimal resources",
		"MEDIUM":   "moderate scaling",
		"HIGH":     "moderate scaling", // index 2, not > count/2 (==2)
		"CRITICAL": "maximum resources",
	}
	for profile, substr := range cases {
		msg := GenerateScalingMessage(profile, names)
		if !strings.Contains(msg, profile) || !strings.Contains(msg, substr) {
			t.Errorf("message(%q) = %q, want to contain %q and %q", profile, msg, profile, substr)
		}
	}
	// Unknown profile still produces a string.
	if GenerateScalingMessage("WAT", names) == "" {
		t.Error("unknown profile produced empty message")
	}
}

func TestGenerateScalingMessageScaleUpTier(t *testing.T) {
	// With 5 profiles, index 3 and 4: index 4 is max; index 3 > 5/2 -> "scaling up".
	names := []string{"A", "B", "C", "D", "E"}
	if msg := GenerateScalingMessage("D", names); !strings.Contains(msg, "scaling up resources") {
		t.Errorf("message(D) = %q, want 'scaling up resources'", msg)
	}
	if msg := GenerateScalingMessage("E", names); !strings.Contains(msg, "maximum resources") {
		t.Errorf("message(E) = %q, want 'maximum resources'", msg)
	}
}

func TestCheckProfileStability(t *testing.T) {
	cfg := testCfg()
	const now int64 = 1_000_000

	cases := []struct {
		name        string
		current     string
		recommended string
		state       StabilityState
		wantStable  bool
		wantReset   bool
	}{
		{"recommendation changed", "LOW", "HIGH",
			StabilityState{StableProfile: "MEDIUM", StableSince: now - 60}, false, true},
		{"already at recommended", "MEDIUM", "MEDIUM",
			StabilityState{StableProfile: "MEDIUM", StableSince: now - 60}, true, true},
		{"scale-up before debounce", "LOW", "MEDIUM",
			StabilityState{StableProfile: "MEDIUM", StableSince: now - 15}, false, false},
		{"scale-up after debounce", "LOW", "MEDIUM",
			StabilityState{StableProfile: "MEDIUM", StableSince: now - 35}, true, false},
		{"scale-up exactly at debounce", "LOW", "MEDIUM",
			StabilityState{StableProfile: "MEDIUM", StableSince: now - 30}, true, false},
		{"scale-down before debounce", "MEDIUM", "LOW",
			StabilityState{StableProfile: "LOW", StableSince: now - 60}, false, false},
		{"scale-down after debounce", "MEDIUM", "LOW",
			StabilityState{StableProfile: "LOW", StableSince: now - 125}, true, false},
		{"scale-down exactly at debounce", "MEDIUM", "LOW",
			StabilityState{StableProfile: "LOW", StableSince: now - 120}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := CheckProfileStability(tc.current, tc.recommended, tc.state, cfg, now)
			if d.Stable != tc.wantStable {
				t.Errorf("Stable = %v, want %v", d.Stable, tc.wantStable)
			}
			if d.ResetTracking != tc.wantReset {
				t.Errorf("ResetTracking = %v, want %v", d.ResetTracking, tc.wantReset)
			}
		})
	}
}

func TestCheckProfileStabilityOscillation(t *testing.T) {
	cfg := testCfg()
	const now int64 = 1_000_000

	// MEDIUM->HIGH, only 10s stable -> not stable, but tracking is current.
	d := CheckProfileStability("MEDIUM", "HIGH", StabilityState{StableProfile: "HIGH", StableSince: now - 10}, cfg, now)
	if d.Stable || d.ResetTracking {
		t.Errorf("step1: %+v, want not stable, no reset", d)
	}
	// Recommendation drops to MEDIUM and we're already at MEDIUM -> stable, reset.
	d = CheckProfileStability("MEDIUM", "MEDIUM", StabilityState{StableProfile: "MEDIUM", StableSince: now - 5}, cfg, now)
	if !d.Stable || !d.ResetTracking {
		t.Errorf("step2: %+v, want stable + reset", d)
	}
	// Recommendation back to HIGH but tracking still says MEDIUM -> changed -> reset, not stable.
	d = CheckProfileStability("MEDIUM", "HIGH", StabilityState{StableProfile: "MEDIUM", StableSince: now - 5}, cfg, now)
	if d.Stable || !d.ResetTracking {
		t.Errorf("step3: %+v, want not stable + reset", d)
	}
}
