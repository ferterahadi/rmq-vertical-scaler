package v3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
)

func diagnosticRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid JSON log: %v: %s", err, line)
		}
		if record["msg"] == "controller observation" {
			records = append(records, record)
		}
	}
	return records
}

func TestDebugLogsSilentStatusPaths(t *testing.T) {
	for _, tc := range []struct {
		name, phase, reason string
		configure           func(*Backend)
	}{
		{"off", "RecommendationOnly", "SustainedIncrease", func(b *Backend) {}},
		{"stabilizing", "Observing", "HoldOrStabilizing", func(b *Backend) {
			b.Policy.Update.Mode = "Auto"
			b.Policy.Behavior.ScaleUp.StabilizationSeconds = 30
		}},
		{"blocked", "Blocked", "MemoryLimitRequiresBrokerBudgetReload", func(b *Backend) {
			b.Policy.Update.Mode = "Auto"
			high := b.Policy.Profiles["HIGH"]
			high.Limits.Memory = "1536Mi"
			b.Policy.Profiles["HIGH"] = high
		}},
	} {
		for _, level := range []slog.Level{slog.LevelInfo, slog.LevelDebug} {
			t.Run(tc.name+"/"+level.String(), func(t *testing.T) {
				at := time.Unix(10000, 0)
				b, core, dyn := fakeBackend(t, at)
				tc.configure(b)
				b.User, b.Password = "private-user", "private-password"
				var buf bytes.Buffer
				logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level}))
				c := NewController(b, "status", logger, at)
				if err := tickAt(c, at); err != nil {
					t.Fatal(err)
				}
				assertNoBrokerWrites(t, core, dyn)
				records := diagnosticRecords(t, &buf)
				if level == slog.LevelInfo {
					if len(records) != 0 {
						t.Fatal("per-poll diagnostics enabled by default")
					}
					return
				}
				if len(records) != 1 {
					t.Fatalf("want one summary per poll, got %d: %s", len(records), buf.String())
				}
				r := records[0]
				if r["phase"] != tc.phase || r["reason"] != tc.reason || r["currentProfile"] != "LOW" || r["recommendedProfile"] != "HIGH" || r["reconciliationFailed"] != false {
					t.Fatalf("wrong diagnostics: %v", r)
				}
				if len(r["signals"].([]any)) != 2 || r["target"] != "test/rabbit" {
					t.Fatalf("missing signals/target: %v", r)
				}
				for _, sensitive := range []string{b.User, b.Password, "containerID", "policyHash", "\"spec\""} {
					if strings.Contains(buf.String(), sensitive) {
						t.Fatalf("diagnostics leaked %s", sensitive)
					}
				}
			})
		}
	}
}

func TestDebugLogsRecoveryAndFailedPersistence(t *testing.T) {
	at := time.Unix(10000, 0)
	b, core, _ := fakeBackend(t, at)
	b.Policy.Update.Mode = "Auto"
	c := NewController(b, "status", quietLog(), at)
	if err := tickAt(c, at); err != nil {
		t.Fatal(err)
	}
	// Restart while the retained increase is unresolved. The observation window
	// must rebuild even though the process can read the prior operation.
	b.Policy.Behavior.ScaleUp.StabilizationSeconds = 30
	// Preserve the operation's policy hash: only the boot time needs to differ.
	state, cm, err := c.load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state.Operation.PolicyHash = b.Policy.Hash()
	s, err := b.Read(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.save(context.Background(), state, &cm, s); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c = NewController(b, "status", logger, at.Add(time.Second))
	if err := tickAt(c, at.Add(15*time.Second)); err != nil {
		t.Fatal(err)
	}
	records := diagnosticRecords(t, &buf)
	if len(records) != 1 || records[0]["phase"] != "Recovering" || records[0]["recoveryReason"] != "ObservingRecoveryIncrease" {
		t.Fatalf("missing underlying recovery reason: %s", buf.String())
	}
	buf.Reset()
	core.PrependReactor("update", "configmaps", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("injected persistence failure")
	})
	if err := tickAt(c, at.Add(30*time.Second)); err == nil {
		t.Fatal("persistence error swallowed")
	}
	records = diagnosticRecords(t, &buf)
	if len(records) != 1 || records[0]["reconciliationFailed"] != true {
		t.Fatalf("failed save logged as successful: %s", buf.String())
	}
}

func TestDiagnosticsReadRecoveryWindowsWithoutAdvancingThem(t *testing.T) {
	at := time.Unix(10000, 0)
	b, _, _ := fakeBackend(t, at)
	var buf bytes.Buffer
	c := NewController(b, "status", slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), at)
	c.Recovery.downSince = at.Add(-45 * time.Second)
	c.Recovery.memory["a"] = memoryWindow{First: at.Add(-30 * time.Second), Last: at, Peak: 123}
	before := *c.Recovery
	before.memory = maps.Clone(c.Recovery.memory)
	before.up = maps.Clone(c.Recovery.up)
	o := engineObservation(b.Policy, at, 1)
	o.Signals[0].Timestamp = at.Add(-61 * time.Second)
	state := Status{At: at, Observation: o, Phase: "Recovering", Reason: "RebuildingObservationWindow", Decision: Decision{Target: -1}, Operation: &Operation{Direction: "down", Target: "LOW"}}
	c.logObservation(context.Background(), state, "ObservingMemoryCoverage", nil)
	if !reflect.DeepEqual(before, *c.Recovery) {
		t.Fatal("diagnostic logging mutated the recovery window")
	}
	records := diagnosticRecords(t, &buf)
	r := records[0]
	window := r["scaleDownWindow"].(map[string]any)
	coverage := window["memoryCoverage"].([]any)[0].(map[string]any)
	if window["elapsedSeconds"] != float64(45) || coverage["sampleCoverageSeconds"] != float64(30) || r["eligibleProfile"] != nil {
		t.Fatalf("incorrect window/profile: %v", r)
	}
	signal := r["signals"].([]any)[0].(map[string]any)
	if signal["fresh"] != false || signal["ageSeconds"] != float64(61) {
		t.Fatalf("stale signal not explained: %v", signal)
	}
}
