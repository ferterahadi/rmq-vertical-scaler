package manifests

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestGenerateMatchesV1Golden asserts the generated YAML is byte-for-byte
// identical to the output captured from the v1 Node.js generator (the golden
// files in testdata/). This is the CLI half of the v2 parity gate.
func TestGenerateMatchesV1Golden(t *testing.T) {
	cases := []struct {
		name       string
		flags      Flags
		configPath string // relative to repo examples/, or "" for no config
		golden     string
	}{
		{
			name:       "production config",
			configPath: "../../examples/production-config.json",
			golden:     "testdata/production.golden.yaml",
		},
		{
			name:       "basic config",
			configPath: "../../examples/basic-config.json",
			golden:     "testdata/basic.golden.yaml",
		},
		{
			name:   "no config defaults",
			flags:  Flags{Namespace: "default", ServiceName: "rabbitmq"},
			golden: "testdata/default.golden.yaml",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var configJSON []byte
			if tc.configPath != "" {
				b, err := os.ReadFile(tc.configPath)
				if err != nil {
					t.Fatalf("read config: %v", err)
				}
				configJSON = b
			}

			got, _, err := Generate(tc.flags, configJSON)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			wantBytes, err := os.ReadFile(tc.golden)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			want := string(wantBytes)

			if got != want {
				t.Errorf("generated YAML differs from v1 golden.\n--- got (%d bytes) ---\n%s\n--- want (%d bytes) ---\n%s",
					len(got), got, len(want), want)
			}
		})
	}
}

func TestGenerateSummary(t *testing.T) {
	b, err := os.ReadFile("../../examples/production-config.json")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	_, s, err := Generate(Flags{}, b)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// production-config.json: namespace production, service rabbitmq-cluster,
	// floor profile MINIMAL, custom debounce/interval.
	if s.Namespace != "production" || s.ServiceName != "rabbitmq-cluster" {
		t.Errorf("namespace/service = %q/%q", s.Namespace, s.ServiceName)
	}
	if s.ScaleUpSeconds != "60" || s.ScaleDnSeconds != "300" || s.CheckInterval != "10" {
		t.Errorf("timing = %s/%s/%s, want 60/300/10", s.ScaleUpSeconds, s.ScaleDnSeconds, s.CheckInterval)
	}
	if len(s.Profiles) != 4 || s.Profiles[0].Name != "MINIMAL" {
		t.Errorf("profiles = %+v, want 4 starting with MINIMAL", s.Profiles)
	}
	if s.QueueThresholds["STANDARD"] != "5000" || s.RateThresholds["MAXIMUM"] != "5000" {
		t.Errorf("thresholds wrong: %v / %v", s.QueueThresholds, s.RateThresholds)
	}
}

// TestGenerateOldFormatThresholds exercises the legacy top-level "thresholds"
// block (v1's extractThresholds old-format branch) instead of per-profile values.
func TestGenerateOldFormatThresholds(t *testing.T) {
	cfg := []byte(`{
		"profiles": {
			"SMALL": {"cpu": "250m", "memory": "1Gi"},
			"BIG":   {"cpu": "2000m", "memory": "8Gi"}
		},
		"thresholds": {
			"queue": {"BIG": 9000},
			"rate":  {"BIG": 400}
		},
		"kubernetes": {"namespace": "qa", "rmqServiceName": "rabbit"}
	}`)
	yaml, s, err := Generate(Flags{}, cfg)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if s.QueueThresholds["BIG"] != "9000" || s.RateThresholds["BIG"] != "400" {
		t.Errorf("thresholds = %v / %v, want BIG 9000/400", s.QueueThresholds, s.RateThresholds)
	}
	for _, want := range []string{
		"QUEUE_THRESHOLD_BIG\n              value: '9000'",
		"RATE_THRESHOLD_BIG\n              value: '400'",
		"PROFILE_SMALL_CPU\n              value: '250m'",
		"PROFILE_NAMES\n              value: 'SMALL BIG'",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("generated YAML missing %q", want)
		}
	}
}

func TestGenerateQueueOnlyOldFormat(t *testing.T) {
	// thresholds.rate absent -> parseThresholdMap returns nil for rate (empty-raw branch).
	cfg := []byte(`{
		"profiles": {"A": {"cpu": "1", "memory": "1Gi"}},
		"thresholds": {"queue": {"A": 5}}
	}`)
	_, s, err := Generate(Flags{}, cfg)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if s.QueueThresholds["A"] != "5" || len(s.RateThresholds) != 0 {
		t.Errorf("queue/rate = %v / %v, want queue A=5, rate empty", s.QueueThresholds, s.RateThresholds)
	}
}

func TestGenerateErrors(t *testing.T) {
	cases := []struct {
		name    string
		cfg     string
		wantErr string
	}{
		{"invalid json", `{bad`, "parse config"},
		{"profiles not object", `{"profiles": "x"}`, "parse profiles"},
		{"bad profile value", `{"profiles": {"A": 5}}`, "parse profile"},
		{"bad queue threshold map", `{"profiles":{"A":{"cpu":"1","memory":"1Gi"}},"thresholds":{"queue":"x"}}`, "parse queue thresholds"},
		{"bad queue threshold value", `{"profiles":{"A":{"cpu":"1","memory":"1Gi"}},"thresholds":{"queue":{"A":"nan"}}}`, "parse queue thresholds"},
		{"bad rate threshold map", `{"profiles":{"A":{"cpu":"1","memory":"1Gi"}},"thresholds":{"queue":{"A":1},"rate":"x"}}`, "parse rate thresholds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Generate(Flags{}, []byte(tc.cfg))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestOrderedObjectErrors(t *testing.T) {
	if _, err := orderedObject([]byte(`[]`)); err == nil {
		t.Error("want error for non-object")
	}
	if _, err := orderedObject([]byte(``)); err == nil {
		t.Error("want error for empty input")
	}
	if _, err := orderedObject([]byte(`{"a":`)); err == nil {
		t.Error("want error for truncated value")
	}
}

func TestFormatNumberInvalidFallsBackToString(t *testing.T) {
	if got := formatNumber(json.Number("abc")); got != "abc" {
		t.Errorf("formatNumber(abc) = %q, want abc (raw fallback)", got)
	}
	if got := formatNumber(json.Number("2000")); got != "2000" {
		t.Errorf("formatNumber(2000) = %q, want 2000", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", ""); got != "" {
		t.Errorf("firstNonEmpty(all empty) = %q, want empty", got)
	}
	if got := firstNonEmpty("", "b", "c"); got != "b" {
		t.Errorf("firstNonEmpty = %q, want b", got)
	}
}

// TestCLIFlagsOverrideConfig verifies CLI flags take precedence over the config
// file (v1's "CLI takes precedence" merge).
func TestCLIFlagsOverrideConfig(t *testing.T) {
	b, err := os.ReadFile("../../examples/production-config.json")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	_, s, err := Generate(Flags{Namespace: "staging", ServiceName: "rmq-x", Image: "custom:1", ScalerName: "sc"}, b)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if s.Namespace != "staging" || s.ServiceName != "rmq-x" || s.Image != "custom:1" || s.ScalerName != "sc" {
		t.Errorf("flag override failed: %+v", s)
	}
}

func TestDefaultImage(t *testing.T) {
	cases := []struct {
		version string
		want    string
	}{
		{"", "ferterahadi/rmq-vertical-scaler:2"},
		{"dev", "ferterahadi/rmq-vertical-scaler:2"},
		{"(devel)", "ferterahadi/rmq-vertical-scaler:2"},
		{"2.1.0", "ferterahadi/rmq-vertical-scaler:2.1.0"},
		{"v2.1.0", "ferterahadi/rmq-vertical-scaler:2.1.0"},
		{"v2.1.1-0.20260704120000-abcdef123456", "ferterahadi/rmq-vertical-scaler:2"}, // pseudo-version
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			if got := defaultImage(tc.version); got != tc.want {
				t.Errorf("defaultImage(%q) = %q, want %q", tc.version, got, tc.want)
			}
		})
	}
}

func TestGenerateUsesVersionedDefaultImage(t *testing.T) {
	_, s, err := Generate(Flags{Version: "2.1.0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Image != "ferterahadi/rmq-vertical-scaler:2.1.0" {
		t.Errorf("Image = %q, want versioned default", s.Image)
	}
	// Explicit --image always wins.
	_, s, err = Generate(Flags{Version: "2.1.0", Image: "custom:1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Image != "custom:1" {
		t.Errorf("Image = %q, want custom:1", s.Image)
	}
}
