package policy

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, e := os.ReadFile("../../examples/v2/resources.json")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestDefaultsAndStrictBoundary(t *testing.T) {
	b := fixture(t)
	var raw map[string]any
	json.Unmarshal(b, &raw)
	delete(raw, "pollingInterval")
	delete(raw, "behavior")
	delete(raw, "updatePolicy")
	data, _ := json.Marshal(raw)
	p, e := Parse(data)
	if e != nil {
		t.Fatal(e)
	}
	if p.PollingInterval != 15 || p.Update.Mode != "Off" || p.Behavior.ScaleDown.StepIntervalSeconds != 300 || p.Triggers[0].Metadata.Value != "60" || p.Triggers[1].Metadata.Value != "75" {
		t.Fatalf("wrong defaults: %+v", p)
	}
	delete(raw, "configVersion")
	data, _ = json.Marshal(raw)
	if _, e = Parse(data); e == nil {
		t.Fatal("unversioned new policy accepted")
	}
	if _, e = Version(data); e == nil {
		t.Fatal("unversioned new policy dispatched to legacy")
	}
}
func TestValidationRejectsUnsafeAndMalformedPolicy(t *testing.T) {
	tests := map[string]func(map[string]any){
		"unknown-version": func(m map[string]any) { m["configVersion"] = 3 },
		"string-version":  func(m map[string]any) { m["configVersion"] = "2" },
		"unknown-key":     func(m map[string]any) { m["trigers"] = []any{} },
		"null":            func(m map[string]any) { m["behavior"] = nil },
		"too-fast-down": func(m map[string]any) {
			m["behavior"].(map[string]any)["scaleDown"].(map[string]any)["stepIntervalSeconds"] = 59
		},
		"fractional-poll": func(m map[string]any) { m["pollingInterval"] = 1.5 },
		"request-over-limit": func(m map[string]any) {
			m["profiles"].(map[string]any)["HIGH"].(map[string]any)["requests"].(map[string]any)["cpu"] = "3"
		},
		"hidden-decrease": func(m map[string]any) {
			m["profiles"].(map[string]any)["HIGH"].(map[string]any)["limits"].(map[string]any)["memory"] = "768Mi"
		},
		"sub-millicore": func(m map[string]any) {
			m["profiles"].(map[string]any)["LOW"].(map[string]any)["requests"].(map[string]any)["cpu"] = "0.0001"
		},
		"bad-container": func(m map[string]any) {
			m["triggers"].([]any)[0].(map[string]any)["metadata"].(map[string]any)["containerName"] = "sidecar"
		},
		"nan-target": func(m map[string]any) {
			m["triggers"].([]any)[0].(map[string]any)["metadata"].(map[string]any)["value"] = "NaN"
		},
		"string-bool": func(m map[string]any) { m["updatePolicy"].(map[string]any)["allowRollingFallback"] = "false" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			var m map[string]any
			json.Unmarshal(fixture(t), &m)
			change(m)
			b, _ := json.Marshal(m)
			if _, err := Parse(b); err == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
	duplicate := strings.Replace(string(fixture(t)), `"configVersion": 2`, `"configVersion": 2, "configVersion": 2`, 1)
	if _, e := Parse([]byte(duplicate)); e == nil {
		t.Fatal("duplicate JSON key accepted")
	}
}
func TestEveryShippedPolicyValidates(t *testing.T) {
	for _, name := range []string{"queues", "resources", "combined"} {
		b, e := os.ReadFile("../../examples/v2/" + name + ".json")
		if e != nil {
			t.Fatal(e)
		}
		p, e := Parse(b)
		if e != nil {
			t.Fatalf("%s: %v", name, e)
		}
		if p.Update.Mode != "Off" {
			t.Fatal("unsafe example default")
		}
	}
}

func TestStructuralSchemaRejectsCaseAliasesBeforeAuto(t *testing.T) {
	data := strings.Replace(string(fixture(t)), `"mode": "Off"`, `"mode": "Off", "Mode": "Auto"`, 1)
	if _, err := Parse([]byte(data)); err == nil {
		t.Fatal("case alias silently enabled Auto")
	}
	public, err := os.ReadFile("../../schema/config-v2-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(public) != string(Schema) {
		t.Fatal("published and runtime schemas differ")
	}
}
