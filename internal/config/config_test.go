package config

import (
	"reflect"
	"testing"
)

// envKeys are every variable the config reads; clearEnv neutralises them so a
// test starts from the v1 defaults regardless of the ambient environment.
var envKeys = []string{
	"PROFILE_NAMES", "RMQ_USER", "RMQ_PASS", "RMQ_SERVICE_NAME", "NAMESPACE",
	"CONFIG_MAP_NAME", "RMQ_HOST", "RMQ_PORT",
	"DEBOUNCE_SCALE_UP_SECONDS", "DEBOUNCE_SCALE_DOWN_SECONDS", "CHECK_INTERVAL_SECONDS",
	"PROFILE_LOW_CPU", "PROFILE_LOW_MEMORY", "PROFILE_MEDIUM_CPU", "PROFILE_MEDIUM_MEMORY",
	"PROFILE_HIGH_CPU", "PROFILE_HIGH_MEMORY", "PROFILE_CRITICAL_CPU", "PROFILE_CRITICAL_MEMORY",
	"QUEUE_THRESHOLD_MEDIUM", "QUEUE_THRESHOLD_HIGH", "QUEUE_THRESHOLD_CRITICAL",
	"RATE_THRESHOLD_MEDIUM", "RATE_THRESHOLD_HIGH", "RATE_THRESHOLD_CRITICAL",
	"PROFILE_MED_CPU", "PROFILE_MED_MEMORY", "QUEUE_THRESHOLD_MED", "RATE_THRESHOLD_MED",
	"PROFILE_TINY_CPU", "PROFILE_SMALL_CPU", "PROFILE_BIG_CPU", "PROFILE_HUGE_CPU",
	"SCALE_MODE",
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range envKeys {
		t.Setenv(k, "") // empty == unset for our coalescing helpers
	}
}

func TestDefaults(t *testing.T) {
	clearEnv(t)
	c := Load()

	if c.RMQUser != "guest" || c.RMQPass != "guest" {
		t.Errorf("user/pass = %q/%q, want guest/guest", c.RMQUser, c.RMQPass)
	}
	if c.RMQServiceName != "rmq" {
		t.Errorf("RMQServiceName = %q, want rmq", c.RMQServiceName)
	}
	if c.Namespace != "prod" {
		t.Errorf("Namespace = %q, want prod", c.Namespace)
	}
	if c.ConfigMapName != "rmq-config" {
		t.Errorf("ConfigMapName = %q, want rmq-config", c.ConfigMapName)
	}
	if c.ScaleUpDebounceSeconds != 30 || c.ScaleDownDebounceSeconds != 120 {
		t.Errorf("debounce = %d/%d, want 30/120", c.ScaleUpDebounceSeconds, c.ScaleDownDebounceSeconds)
	}
	if c.CheckIntervalSeconds != 5 {
		t.Errorf("interval = %d, want 5", c.CheckIntervalSeconds)
	}
	if !reflect.DeepEqual(c.ProfileNames, []string{"LOW", "MEDIUM", "HIGH", "CRITICAL"}) {
		t.Errorf("ProfileNames = %v", c.ProfileNames)
	}
}

func TestEnvOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("RMQ_USER", "admin")
	t.Setenv("RMQ_PASS", "secret123")
	t.Setenv("RMQ_SERVICE_NAME", "my-rabbit")
	t.Setenv("NAMESPACE", "staging")
	t.Setenv("CONFIG_MAP_NAME", "rabbit-config")
	t.Setenv("RMQ_HOST", "rabbit.example.com")
	t.Setenv("RMQ_PORT", "15672")

	c := Load()
	if c.RMQUser != "admin" || c.RMQPass != "secret123" {
		t.Errorf("user/pass = %q/%q", c.RMQUser, c.RMQPass)
	}
	if c.RMQServiceName != "my-rabbit" || c.Namespace != "staging" || c.ConfigMapName != "rabbit-config" {
		t.Errorf("names = %q/%q/%q", c.RMQServiceName, c.Namespace, c.ConfigMapName)
	}
	if c.RMQHost != "rabbit.example.com" || c.RMQPort != "15672" {
		t.Errorf("host/port = %q/%q", c.RMQHost, c.RMQPort)
	}
}

func TestDefaultProfiles(t *testing.T) {
	clearEnv(t)
	c := Load()
	want := map[string]Profile{
		"LOW":      {CPU: "1000m", Memory: "2Gi"},
		"MEDIUM":   {CPU: "1000m", Memory: "2Gi"},
		"HIGH":     {CPU: "1000m", Memory: "2Gi"},
		"CRITICAL": {CPU: "1000m", Memory: "2Gi"},
	}
	if !reflect.DeepEqual(c.Profiles, want) {
		t.Errorf("Profiles = %v", c.Profiles)
	}
}

func TestCustomProfileResources(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROFILE_LOW_CPU", "500m")
	t.Setenv("PROFILE_LOW_MEMORY", "1Gi")
	t.Setenv("PROFILE_MEDIUM_CPU", "1000m")
	t.Setenv("PROFILE_MEDIUM_MEMORY", "2Gi")
	t.Setenv("PROFILE_HIGH_CPU", "2000m")
	t.Setenv("PROFILE_HIGH_MEMORY", "4Gi")
	t.Setenv("PROFILE_CRITICAL_CPU", "4000m")
	t.Setenv("PROFILE_CRITICAL_MEMORY", "8Gi")

	c := Load()
	want := map[string]Profile{
		"LOW":      {CPU: "500m", Memory: "1Gi"},
		"MEDIUM":   {CPU: "1000m", Memory: "2Gi"},
		"HIGH":     {CPU: "2000m", Memory: "4Gi"},
		"CRITICAL": {CPU: "4000m", Memory: "8Gi"},
	}
	if !reflect.DeepEqual(c.Profiles, want) {
		t.Errorf("Profiles = %v", c.Profiles)
	}
}

func TestDefaultThresholds(t *testing.T) {
	clearEnv(t)
	c := Load()
	wantQ := map[string]int{"MEDIUM": 1000, "HIGH": 1000, "CRITICAL": 1000}
	wantR := map[string]int{"MEDIUM": 100, "HIGH": 100, "CRITICAL": 100}
	if !reflect.DeepEqual(c.QueueThresholds, wantQ) {
		t.Errorf("QueueThresholds = %v", c.QueueThresholds)
	}
	if !reflect.DeepEqual(c.RateThresholds, wantR) {
		t.Errorf("RateThresholds = %v", c.RateThresholds)
	}
	// First profile must have no thresholds.
	if _, ok := c.QueueThresholds["LOW"]; ok {
		t.Error("LOW should not have a queue threshold")
	}
	if _, ok := c.RateThresholds["LOW"]; ok {
		t.Error("LOW should not have a rate threshold")
	}
}

func TestCustomThresholds(t *testing.T) {
	clearEnv(t)
	t.Setenv("QUEUE_THRESHOLD_MEDIUM", "2000")
	t.Setenv("QUEUE_THRESHOLD_HIGH", "10000")
	t.Setenv("QUEUE_THRESHOLD_CRITICAL", "50000")
	t.Setenv("RATE_THRESHOLD_MEDIUM", "200")
	t.Setenv("RATE_THRESHOLD_HIGH", "1000")
	t.Setenv("RATE_THRESHOLD_CRITICAL", "5000")

	c := Load()
	wantQ := map[string]int{"MEDIUM": 2000, "HIGH": 10000, "CRITICAL": 50000}
	wantR := map[string]int{"MEDIUM": 200, "HIGH": 1000, "CRITICAL": 5000}
	if !reflect.DeepEqual(c.QueueThresholds, wantQ) {
		t.Errorf("QueueThresholds = %v", c.QueueThresholds)
	}
	if !reflect.DeepEqual(c.RateThresholds, wantR) {
		t.Errorf("RateThresholds = %v", c.RateThresholds)
	}
}

func TestDebounceAndInterval(t *testing.T) {
	clearEnv(t)
	t.Setenv("DEBOUNCE_SCALE_UP_SECONDS", "60")
	t.Setenv("DEBOUNCE_SCALE_DOWN_SECONDS", "300")
	t.Setenv("CHECK_INTERVAL_SECONDS", "10")

	c := Load()
	if c.ScaleUpDebounceSeconds != 60 || c.ScaleDownDebounceSeconds != 300 || c.CheckIntervalSeconds != 10 {
		t.Errorf("got %d/%d/%d, want 60/300/10",
			c.ScaleUpDebounceSeconds, c.ScaleDownDebounceSeconds, c.CheckIntervalSeconds)
	}
}

func TestCPUToProfileMap(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROFILE_LOW_CPU", "330m")
	t.Setenv("PROFILE_MEDIUM_CPU", "800m")
	t.Setenv("PROFILE_HIGH_CPU", "1600m")
	t.Setenv("PROFILE_CRITICAL_CPU", "2400m")

	c := Load()
	want := map[string]string{"330m": "LOW", "800m": "MEDIUM", "1600m": "HIGH", "2400m": "CRITICAL"}
	if !reflect.DeepEqual(c.CPUToProfile, want) {
		t.Errorf("CPUToProfile = %v", c.CPUToProfile)
	}
}

func TestCPUToProfileMapDuplicates(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROFILE_LOW_CPU", "1000m")
	t.Setenv("PROFILE_MEDIUM_CPU", "1000m")
	t.Setenv("PROFILE_HIGH_CPU", "2000m")
	t.Setenv("PROFILE_CRITICAL_CPU", "2000m")

	c := Load()
	// Later profile wins when CPU values collide (v1 insertion-order semantics).
	if c.CPUToProfile["1000m"] != "MEDIUM" {
		t.Errorf("CPUToProfile[1000m] = %q, want MEDIUM", c.CPUToProfile["1000m"])
	}
	if c.CPUToProfile["2000m"] != "CRITICAL" {
		t.Errorf("CPUToProfile[2000m] = %q, want CRITICAL", c.CPUToProfile["2000m"])
	}
}

func TestCustomProfileNamesMap(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROFILE_NAMES", "TINY SMALL BIG HUGE")
	t.Setenv("PROFILE_TINY_CPU", "100m")
	t.Setenv("PROFILE_SMALL_CPU", "500m")
	t.Setenv("PROFILE_BIG_CPU", "2000m")
	t.Setenv("PROFILE_HUGE_CPU", "8000m")

	c := Load()
	want := map[string]string{"100m": "TINY", "500m": "SMALL", "2000m": "BIG", "8000m": "HUGE"}
	if !reflect.DeepEqual(c.CPUToProfile, want) {
		t.Errorf("CPUToProfile = %v", c.CPUToProfile)
	}
}

func TestCompleteConfiguration(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROFILE_NAMES", "LOW MED HIGH")
	t.Setenv("RMQ_HOST", "rabbit.prod.example.com")
	t.Setenv("RMQ_PORT", "15672")
	t.Setenv("RMQ_USER", "prod_user")
	t.Setenv("RMQ_PASS", "prod_pass")
	t.Setenv("RMQ_SERVICE_NAME", "prod-rabbit")
	t.Setenv("NAMESPACE", "production")
	t.Setenv("CONFIG_MAP_NAME", "prod-rabbit-config")
	t.Setenv("PROFILE_LOW_CPU", "250m")
	t.Setenv("PROFILE_LOW_MEMORY", "512Mi")
	t.Setenv("PROFILE_MED_CPU", "1000m")
	t.Setenv("PROFILE_MED_MEMORY", "2Gi")
	t.Setenv("PROFILE_HIGH_CPU", "4000m")
	t.Setenv("PROFILE_HIGH_MEMORY", "8Gi")
	t.Setenv("QUEUE_THRESHOLD_MED", "5000")
	t.Setenv("QUEUE_THRESHOLD_HIGH", "20000")
	t.Setenv("RATE_THRESHOLD_MED", "500")
	t.Setenv("RATE_THRESHOLD_HIGH", "2000")
	t.Setenv("DEBOUNCE_SCALE_UP_SECONDS", "45")
	t.Setenv("DEBOUNCE_SCALE_DOWN_SECONDS", "180")
	t.Setenv("CHECK_INTERVAL_SECONDS", "15")

	c := Load()
	if !reflect.DeepEqual(c.ProfileNames, []string{"LOW", "MED", "HIGH"}) {
		t.Errorf("ProfileNames = %v", c.ProfileNames)
	}
	if !reflect.DeepEqual(c.Profiles, map[string]Profile{
		"LOW":  {CPU: "250m", Memory: "512Mi"},
		"MED":  {CPU: "1000m", Memory: "2Gi"},
		"HIGH": {CPU: "4000m", Memory: "8Gi"},
	}) {
		t.Errorf("Profiles = %v", c.Profiles)
	}
	if !reflect.DeepEqual(c.QueueThresholds, map[string]int{"MED": 5000, "HIGH": 20000}) {
		t.Errorf("QueueThresholds = %v", c.QueueThresholds)
	}
	if !reflect.DeepEqual(c.RateThresholds, map[string]int{"MED": 500, "HIGH": 2000}) {
		t.Errorf("RateThresholds = %v", c.RateThresholds)
	}
	if c.ScaleUpDebounceSeconds != 45 || c.ScaleDownDebounceSeconds != 180 || c.CheckIntervalSeconds != 15 {
		t.Errorf("timing = %d/%d/%d", c.ScaleUpDebounceSeconds, c.ScaleDownDebounceSeconds, c.CheckIntervalSeconds)
	}
	if !reflect.DeepEqual(c.CPUToProfile, map[string]string{"250m": "LOW", "1000m": "MED", "4000m": "HIGH"}) {
		t.Errorf("CPUToProfile = %v", c.CPUToProfile)
	}
}

func TestSingleProfile(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROFILE_NAMES", "SINGLE")
	c := Load()
	if !reflect.DeepEqual(c.ProfileNames, []string{"SINGLE"}) {
		t.Errorf("ProfileNames = %v", c.ProfileNames)
	}
	if !reflect.DeepEqual(c.Profiles, map[string]Profile{"SINGLE": {CPU: "1000m", Memory: "2Gi"}}) {
		t.Errorf("Profiles = %v", c.Profiles)
	}
	if len(c.QueueThresholds) != 0 || len(c.RateThresholds) != 0 {
		t.Errorf("single profile should have no thresholds, got %v/%v", c.QueueThresholds, c.RateThresholds)
	}
}

func TestInvalidNumbersFallBackToDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("DEBOUNCE_SCALE_UP_SECONDS", "not-a-number")
	t.Setenv("CHECK_INTERVAL_SECONDS", "invalid")
	t.Setenv("QUEUE_THRESHOLD_MEDIUM", "abc")

	c := Load()
	if c.ScaleUpDebounceSeconds != 30 {
		t.Errorf("ScaleUpDebounceSeconds = %d, want 30 (default)", c.ScaleUpDebounceSeconds)
	}
	if c.CheckIntervalSeconds != 5 {
		t.Errorf("CheckIntervalSeconds = %d, want 5 (default)", c.CheckIntervalSeconds)
	}
	if c.QueueThresholds["MEDIUM"] != 1000 {
		t.Errorf("QueueThresholds[MEDIUM] = %d, want 1000 (default)", c.QueueThresholds["MEDIUM"])
	}
}

func TestScaleModeDefaultsToAuto(t *testing.T) {
	clearEnv(t)
	c := Load()
	if c.ScaleMode != ScaleModeAuto {
		t.Errorf("ScaleMode = %q, want %q", c.ScaleMode, ScaleModeAuto)
	}
}

func TestScaleModeAcceptsValidValues(t *testing.T) {
	clearEnv(t)
	for env, want := range map[string]string{
		"auto":    ScaleModeAuto,
		"inplace": ScaleModeInPlace,
		"rolling": ScaleModeRolling,
		"InPlace": ScaleModeInPlace, // case-insensitive
		"ROLLING": ScaleModeRolling,
	} {
		t.Setenv("SCALE_MODE", env)
		if c := Load(); c.ScaleMode != want {
			t.Errorf("SCALE_MODE=%q: ScaleMode = %q, want %q", env, c.ScaleMode, want)
		}
	}
}

func TestScaleModeInvalidFallsBackToAuto(t *testing.T) {
	clearEnv(t)
	t.Setenv("SCALE_MODE", "blue-green")
	if c := Load(); c.ScaleMode != ScaleModeAuto {
		t.Errorf("ScaleMode = %q, want %q (fallback)", c.ScaleMode, ScaleModeAuto)
	}
}

func TestMissingHostPort(t *testing.T) {
	clearEnv(t)
	c := Load()
	if c.RMQHost != "" || c.RMQPort != "" {
		t.Errorf("host/port = %q/%q, want empty", c.RMQHost, c.RMQPort)
	}
}
