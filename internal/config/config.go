// Package config loads the scaler's runtime configuration from environment
// variables. It is a faithful port of the v1 Node.js ConfigManager
// (lib/ConfigManager.js): the same variable names, the same defaults, and the
// same derived structures (profiles, thresholds, cpu->profile map).
package config

import (
	"os"
	"strconv"
	"strings"
)

// Profile is the CPU/memory resource request for a single scaling profile.
type Profile struct {
	CPU    string
	Memory string
}

// Config holds the fully-resolved configuration. It mirrors the fields the v1
// ConfigManager exposed so behaviour stays identical.
type Config struct {
	// RabbitMQ management API connection.
	RMQHost string
	RMQPort string
	RMQUser string
	RMQPass string

	// Kubernetes object names the controller operates on.
	RMQServiceName string
	Namespace      string
	ConfigMapName  string

	// Scaling profiles, in priority order (index 0 is the floor).
	ProfileNames []string
	Profiles     map[string]Profile

	// Per-profile trigger thresholds. The floor profile (ProfileNames[0]) has
	// no thresholds, matching v1.
	QueueThresholds map[string]int
	RateThresholds  map[string]int

	// Debounce / loop timing.
	ScaleUpDebounceSeconds   int
	ScaleDownDebounceSeconds int
	CheckIntervalSeconds     int

	// CPUToProfile reverse-maps a CPU request string to its profile name. When
	// two profiles share a CPU value the later one wins (v1 insertion-order
	// semantics).
	CPUToProfile map[string]string
}

// Defaults mirror lib/ConfigManager.js exactly.
const (
	defaultProfileNames = "LOW MEDIUM HIGH CRITICAL"
	defaultProfileCPU   = "1000m"
	defaultProfileMem   = "2Gi"
	defaultQueueThresh  = 1000
	defaultRateThresh   = 100
	defaultScaleUpSecs  = 30
	defaultScaleDnSecs  = 120
	defaultIntervalSecs = 5
)

// Load reads the configuration from the process environment. It never returns
// an error: missing/invalid values fall back to the v1 defaults, just like the
// Node.js implementation's `||`-style coalescing and parseInt fallbacks.
func Load() *Config {
	c := &Config{
		RMQHost:        os.Getenv("RMQ_HOST"), // no default in v1 (may be empty)
		RMQPort:        os.Getenv("RMQ_PORT"),
		RMQUser:        getenv("RMQ_USER", "guest"),
		RMQPass:        getenv("RMQ_PASS", "guest"),
		RMQServiceName: getenv("RMQ_SERVICE_NAME", "rmq"),
		Namespace:      getenv("NAMESPACE", "prod"),
		ConfigMapName:  getenv("CONFIG_MAP_NAME", "rmq-config"),

		ScaleUpDebounceSeconds:   getint("DEBOUNCE_SCALE_UP_SECONDS", defaultScaleUpSecs),
		ScaleDownDebounceSeconds: getint("DEBOUNCE_SCALE_DOWN_SECONDS", defaultScaleDnSecs),
		CheckIntervalSeconds:     getint("CHECK_INTERVAL_SECONDS", defaultIntervalSecs),

		Profiles:        map[string]Profile{},
		QueueThresholds: map[string]int{},
		RateThresholds:  map[string]int{},
		CPUToProfile:    map[string]string{},
	}

	c.ProfileNames = strings.Split(getenv("PROFILE_NAMES", defaultProfileNames), " ")

	for i, name := range c.ProfileNames {
		c.Profiles[name] = Profile{
			CPU:    getenv("PROFILE_"+name+"_CPU", defaultProfileCPU),
			Memory: getenv("PROFILE_"+name+"_MEMORY", defaultProfileMem),
		}
		// The first profile is the floor and has no thresholds.
		if i > 0 {
			c.QueueThresholds[name] = getint("QUEUE_THRESHOLD_"+name, defaultQueueThresh)
			c.RateThresholds[name] = getint("RATE_THRESHOLD_"+name, defaultRateThresh)
		}
	}

	// Build the reverse map in profile-name order so that, like v1's
	// Object.entries iteration, a later profile sharing a CPU value wins.
	for _, name := range c.ProfileNames {
		c.CPUToProfile[c.Profiles[name].CPU] = name
	}

	return c
}

// getenv returns the env var, or def when it is unset or empty. This matches
// the v1 `process.env.X || 'default'` idiom where the empty string is falsy.
func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// getint parses an integer env var, falling back to def when unset, empty, or
// unparseable. v1 used `parseInt(process.env.X || 'default')`; an unparseable
// value yielded NaN, and the tests accept either NaN or the default — we choose
// the default for well-defined behaviour.
func getint(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
