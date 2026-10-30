package manifests

import (
	"os"
	"strings"
	"testing"
)

func TestVersion2PolicyIsMountedWithoutLegacyAutoFields(t *testing.T) {
	data, err := os.ReadFile("../../examples/v2/resources.json")
	if err != nil {
		t.Fatal(err)
	}
	out, s, err := Generate(Flags{Version: "2.3.0"}, data)
	if err != nil {
		t.Fatal(err)
	}
	if s.ConfigVersion != 2 || s.UpdateMode != "Off" {
		t.Fatalf("wrong dispatch: %+v", s)
	}
	for _, must := range []string{`"Recreate"`, `"--config"`, `"checksum/policy"`, `"policy.json"`, `"STATUS_CONFIG_MAP"`} {
		if !strings.Contains(out, must) {
			t.Fatalf("missing %s", must)
		}
	}
	for _, absent := range []string{`"PROFILE_NAMES"`, `"SCALE_MODE"`, `"RMQ_PASS"`, `"pods/resize"`, `"stable_since"`, `"name": "rmq-vertical-scaler-status"`} {
		if strings.Contains(out, absent) {
			t.Fatalf("unwanted %s", absent)
		}
	}
	if _, _, err = Generate(Flags{Namespace: "other"}, data); err == nil {
		t.Fatal("conflicting target overwritten")
	}
}

func TestVersion2DoesNotDefaultToAnOlderImage(t *testing.T) {
	data, err := os.ReadFile("../../examples/v2/resources.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"", "dev", "2.2.1"} {
		if _, _, err := Generate(Flags{Version: version}, data); err == nil {
			t.Fatalf("unsupported default image for %s", version)
		}
	}
	out, _, err := Generate(Flags{Version: "2.3.1"}, data)
	if err != nil || !strings.Contains(out, "ferterahadi/rmq-vertical-scaler:2.3.1") {
		t.Fatalf("release image not pinned: %v", err)
	}
}
