package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// execute runs the root command with args, capturing stdout/stderr into buf.
func execute(args ...string) (string, error) {
	root := rootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestGenerateWritesManifest(t *testing.T) {
	out := filepath.Join(t.TempDir(), "scaler.yaml")
	stdout, err := execute("generate",
		"--config", "../../examples/basic-config.json",
		"--output", out)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatalf("output not written: %v", readErr)
	}
	if !strings.Contains(string(b), "kind: Deployment") {
		t.Error("generated file missing Deployment")
	}
	// printSummary went to the command writer (covers printSummary + orNA).
	for _, want := range []string{"Scaling Profiles", "Generated " + out, "LOW:", "MEDIUM:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("summary missing %q\n%s", want, stdout)
		}
	}
}

func TestGenerateNoConfigUsesDefaults(t *testing.T) {
	out := filepath.Join(t.TempDir(), "scaler.yaml")
	if _, err := execute("generate", "--namespace", "default", "--service-name", "rabbitmq", "--output", out); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output not written: %v", err)
	}
}

func TestGenerateProductionConfigShowsNA(t *testing.T) {
	// production-config's floor profile is MINIMAL (not "LOW"), so it has no
	// thresholds -> printSummary renders "N/A" (covers orNA's empty branch).
	out := filepath.Join(t.TempDir(), "scaler.yaml")
	stdout, err := execute("generate", "--config", "../../examples/production-config.json", "--output", out)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(stdout, "N/A") {
		t.Errorf("summary missing N/A for the threshold-less floor profile\n%s", stdout)
	}
}

func TestGenerateWriteError(t *testing.T) {
	// Output path inside a non-existent directory -> os.WriteFile fails.
	out := filepath.Join(t.TempDir(), "nodir", "scaler.yaml")
	_, err := execute("generate", "--namespace", "default", "--service-name", "rabbitmq", "--output", out)
	if err == nil || !strings.Contains(err.Error(), "failed to write") {
		t.Errorf("err = %v, want write error", err)
	}
}

func TestGenerateConfigNotFound(t *testing.T) {
	_, err := execute("generate", "--config", "/no/such/file.json")
	if err == nil || !strings.Contains(err.Error(), "failed to load config file") {
		t.Errorf("err = %v, want load-config error", err)
	}
}

func TestGenerateBadConfig(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := execute("generate", "--config", bad)
	if err == nil || !strings.Contains(err.Error(), "failed to generate manifests") {
		t.Errorf("err = %v, want generate error", err)
	}
}

func TestRunFailsOutsideCluster(t *testing.T) {
	// runCmd builds the in-cluster client; outside a pod InClusterConfig fails.
	// Redirect os.Stdout (runCmd's logger writes the banner there) to keep output clean.
	old := os.Stdout
	devnull, _ := os.Open(os.DevNull)
	os.Stdout = devnull
	defer func() { os.Stdout = old; devnull.Close() }()

	_, err := execute("run")
	if err == nil || !strings.Contains(err.Error(), "kubernetes") {
		t.Errorf("err = %v, want kubernetes-client init error", err)
	}
}
