package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/examples"
	"github.com/ferterahadi/rmq-vertical-scaler/v2/schema"
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
	if err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Errorf("err = %v, want parse-config error", err)
	}
}

func TestGenerateRejectsInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.json")
	// "debounse" is a typo'd key; LOW is missing memory.
	bad := `{"profiles":{"LOW":{"cpu":"330m"}},"debounse":{"scaleUpSeconds":30}}`
	if err := os.WriteFile(cfgPath, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "out.yaml")

	cmd := rootCmd()
	cmd.SetArgs([]string{"generate", "--config", cfgPath, "--output", outPath})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("generate with invalid config succeeded, want validation error")
	}
	for _, want := range []string{"debounse", "memory"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Errorf("output file %s was written despite invalid config", outPath)
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

func TestPickVersion(t *testing.T) {
	cases := []struct {
		name             string
		ldflags, buildBI string
		want             string
	}{
		{"ldflags wins", "2.1.0", "v9.9.9", "2.1.0"},
		{"buildinfo when dev", "dev", "v2.1.0", "v2.1.0"},
		{"devel buildinfo ignored", "dev", "(devel)", ""},
		{"both empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickVersion(tc.ldflags, tc.buildBI); got != tc.want {
				t.Errorf("pickVersion(%q, %q) = %q, want %q", tc.ldflags, tc.buildBI, got, tc.want)
			}
		})
	}
}

func TestInitWritesScaffold(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "my-config.json")

	cmd := rootCmd()
	cmd.SetArgs([]string{"init", "--output", out})
	cmd.SetOut(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, examples.TemplateConfig) {
		t.Error("scaffold content differs from embedded template")
	}
	if err := schema.Validate(b); err != nil {
		t.Errorf("scaffold does not validate: %v", err)
	}
}

func TestInitRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "my-config.json")
	if err := os.WriteFile(out, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := rootCmd()
	cmd.SetArgs([]string{"init", "--output", out})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("init over an existing file succeeded, want error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error %q does not mention 'already exists'", err.Error())
	}
	b, _ := os.ReadFile(out)
	if string(b) != "keep me" {
		t.Error("existing file was clobbered")
	}
}

func TestInitOpenFileErrorNotExist(t *testing.T) {
	// A parent directory that doesn't exist produces a non-IsExist OpenFile
	// error, exercising the generic "write %s: %w" branch (as opposed to the
	// "already exists" branch covered by TestInitRefusesOverwrite).
	out := filepath.Join(t.TempDir(), "missing-parent", "my-config.json")

	cmd := rootCmd()
	cmd.SetArgs([]string{"init", "--output", out})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("init with a missing parent directory succeeded, want error")
	}
	if strings.Contains(err.Error(), "already exists") {
		t.Errorf("error %q should not claim the file already exists", err.Error())
	}
	if !strings.Contains(err.Error(), "write") {
		t.Errorf("error %q does not mention the write failure", err.Error())
	}
}
