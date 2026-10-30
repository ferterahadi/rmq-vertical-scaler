package manifests

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func chartPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../../charts/rmq-vertical-scaler")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func helmOutput(t *testing.T, extra ...string) ([]byte, error) {
	t.Helper()
	binary, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("Helm not installed")
	}
	args := []string{"template", "rvs", chartPath(t), "--namespace", "production", "-f", filepath.Join(chartPath(t), "values-v2.example.yaml")}
	return exec.Command(binary, append(args, extra...)...).CombinedOutput()
}
func TestHelmVersion2IsolationAndRecreate(t *testing.T) {
	output, err := helmOutput(t)
	if err != nil {
		t.Fatalf("helm: %v %s", err, output)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(output)))
	var deployment, role map[string]any
	configMaps := 0
	for {
		var doc map[string]any
		if err := decoder.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if doc == nil {
			continue
		}
		switch doc["kind"] {
		case "Deployment":
			deployment = doc
		case "Role":
			role = doc
		case "ConfigMap":
			configMaps++
		}
	}
	if deployment == nil || role == nil || configMaps != 1 {
		t.Fatal("missing v2 objects or templated runtime status")
	}
	spec := deployment["spec"].(map[string]any)
	if spec["strategy"].(map[string]any)["type"] != "Recreate" || spec["replicas"] != 1 {
		t.Fatal("unsafe scaler rollout")
	}
	for _, absent := range []string{"PROFILE_NAMES", "SCALE_MODE", "RMQ_PASS", "pods/resize", "stable_since"} {
		if strings.Contains(string(output), absent) {
			t.Fatalf("resource-only Off leaked %s", absent)
		}
	}
	if !strings.Contains(string(output), "checksum/policy") || !strings.Contains(string(output), "--config") {
		t.Fatal("policy changes do not restart the new runtime")
	}
}
func TestHelmVersion2RejectsEmptyMixedAndContradictoryTargets(t *testing.T) {
	for _, args := range [][]string{{"--set-json", "policy={}"}, {"--set", "scaling.mode=inplace"}, {"--set", "checkInterval=30"}, {"--set", "policy.kubernetes.namespace=other"}, {"--set", "policy.kubernetes.rmqServiceName=other"}} {
		if output, err := helmOutput(t, args...); err == nil {
			t.Fatalf("invalid Helm configuration rendered: %v %s", args, output)
		}
	}
}
func TestLegacyChartDefaultsSnapshotMatchesInheritedValues(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(chartPath(t), "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err = yaml.Unmarshal(data, &values); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(chartPath(t), "files/legacy-policy-defaults.json"))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err = json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	selected := map[string]any{}
	for key := range snapshot {
		selected[key] = values[key]
	}
	data, err = json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	var normalized map[string]any
	json.Unmarshal(data, &normalized)
	if !reflect.DeepEqual(normalized, snapshot) {
		t.Fatal("legacy chart defaults snapshot drifted from values.yaml")
	}
}
