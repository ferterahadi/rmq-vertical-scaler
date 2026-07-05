package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRejectsBadConfigs(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		wantSub string // substring the error must contain
	}{
		{
			name:    "invalid JSON",
			config:  `{"profiles":`,
			wantSub: "parse config",
		},
		{
			name:    "unknown top-level key (typo)",
			config:  `{"profiles":{"LOW":{"cpu":"330m","memory":"2Gi"}},"debounse":{"scaleUpSeconds":30,"scaleDownSeconds":120}}`,
			wantSub: "debounse",
		},
		{
			name:    "profile missing memory",
			config:  `{"profiles":{"LOW":{"cpu":"330m"}}}`,
			wantSub: "memory",
		},
		{
			name:    "profile missing cpu",
			config:  `{"profiles":{"LOW":{"memory":"2Gi"}}}`,
			wantSub: "cpu",
		},
		{
			name:    "lowercase profile key",
			config:  `{"profiles":{"low":{"cpu":"330m","memory":"2Gi"}}}`,
			wantSub: "low",
		},
		{
			name:    "memory suffix on cpu",
			config:  `{"profiles":{"LOW":{"cpu":"2Gi","memory":"2Gi"}}}`,
			wantSub: "cpu",
		},
		{
			name:    "memory without unit",
			config:  `{"profiles":{"LOW":{"cpu":"330m","memory":"2"}}}`,
			wantSub: "memory",
		},
		{
			name:    "missing profiles entirely",
			config:  `{"checkInterval":5}`,
			wantSub: "profiles",
		},
		{
			name:    "unknown profile field",
			config:  `{"profiles":{"LOW":{"cpu":"330m","memory":"2Gi","memry":"3Gi"}}}`,
			wantSub: "memry",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate([]byte(tc.config))
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestValidateAcceptsGoodConfigs(t *testing.T) {
	cases := []struct {
		name   string
		config string
	}{
		{
			name:   "minimal",
			config: `{"profiles":{"LOW":{"cpu":"330m","memory":"2Gi"}}}`,
		},
		{
			name:   "with $schema annotation",
			config: `{"$schema":"https://example.com/s.json","profiles":{"LOW":{"cpu":"330m","memory":"2Gi"}}}`,
		},
		{
			name:   "legacy thresholds block",
			config: `{"profiles":{"LOW":{"cpu":"330m","memory":"2Gi"},"HIGH":{"cpu":"2","memory":"4Gi"}},"thresholds":{"queue":{"HIGH":10000},"rate":{"HIGH":1000}}}`,
		},
		{
			name:   "partial debounce",
			config: `{"profiles":{"LOW":{"cpu":"330m","memory":"2Gi"}},"debounce":{"scaleUpSeconds":60}}`,
		},
		{
			name:   "partial kubernetes",
			config: `{"profiles":{"LOW":{"cpu":"330m","memory":"2Gi"}},"kubernetes":{"namespace":"prod"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate([]byte(tc.config)); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

// Every shipped example must validate against the shipped schema.
func TestValidateAcceptsShippedExamples(t *testing.T) {
	matches, err := filepath.Glob("../examples/*.json")
	if err != nil || len(matches) == 0 {
		t.Fatalf("glob examples: %v (found %d)", err, len(matches))
	}
	for _, path := range matches {
		t.Run(filepath.Base(path), func(t *testing.T) {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := Validate(b); err != nil {
				t.Errorf("Validate(%s) = %v, want nil", path, err)
			}
		})
	}
}
