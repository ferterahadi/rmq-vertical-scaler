// Package policy defines the opt-in version-2 policy. Legacy env configuration
// deliberately remains in internal/config.
package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"
)

type Amounts struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}
type Profile struct {
	Requests Amounts `json:"requests"`
	Limits   Amounts `json:"limits"`
}
type Selector struct {
	Vhosts         []string `json:"vhosts,omitempty"`
	QueueNameRegex string   `json:"queueNameRegex,omitempty"`
}
type Metadata struct {
	Mode          string             `json:"mode,omitempty"`
	Aggregation   string             `json:"aggregation,omitempty"`
	Thresholds    map[string]float64 `json:"thresholds,omitempty"`
	Selector      *Selector          `json:"selector,omitempty"`
	ContainerName string             `json:"containerName,omitempty"`
	Value         string             `json:"value,omitempty"`
}
type Trigger struct {
	Type        string   `json:"type"`
	MetricType  string   `json:"metricType,omitempty"`
	Aggregation string   `json:"aggregation,omitempty"`
	Metadata    Metadata `json:"metadata"`
}
type UpdatePolicy struct {
	Mode                 string `json:"mode"`
	AllowRollingFallback bool   `json:"allowRollingFallback"`
}
type Behavior struct {
	ScaleUp struct {
		StabilizationSeconds int `json:"stabilizationSeconds"`
	} `json:"scaleUp"`
	ScaleDown struct {
		StepIntervalSeconds   int `json:"stepIntervalSeconds"`
		MemoryHeadroomPercent int `json:"memoryHeadroomPercent"`
	} `json:"scaleDown"`
	Metrics struct {
		MaxAgeSeconds int `json:"maxAgeSeconds"`
	} `json:"metrics"`
}
type Target struct {
	Namespace   string `json:"namespace"`
	ServiceName string `json:"rmqServiceName"`
}
type Connection struct {
	Host string `json:"host"`
	Port string `json:"port"`
}
type Config struct {
	Schema          string             `json:"$schema,omitempty"`
	Version         int                `json:"configVersion"`
	Order           []string           `json:"profileOrder"`
	Profiles        map[string]Profile `json:"profiles"`
	Triggers        []Trigger          `json:"triggers"`
	PollingInterval int                `json:"pollingInterval"`
	Update          UpdatePolicy       `json:"updatePolicy"`
	Behavior        Behavior           `json:"behavior"`
	Target          Target             `json:"kubernetes"`
	RMQ             *Connection        `json:"rmq,omitempty"`
}

func Defaults() Config {
	c := Config{Version: 2, PollingInterval: 15, Update: UpdatePolicy{Mode: "Off"}}
	c.Behavior.ScaleUp.StabilizationSeconds = 30
	c.Behavior.ScaleDown.StepIntervalSeconds = 300
	c.Behavior.ScaleDown.MemoryHeadroomPercent = 20
	c.Behavior.Metrics.MaxAgeSeconds = 60
	return c
}

// Version also rejects unknown versions, so a misspelled new policy cannot
// quietly select the legacy generator.
func Version(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return 0, fmt.Errorf("parse config: %w", err)
	}
	if raw, ok := top["configVersion"]; ok {
		var v int
		if err := json.Unmarshal(raw, &v); err != nil || v != 2 {
			return 0, fmt.Errorf("configVersion must be integer 2")
		}
		return v, nil
	}
	for _, key := range []string{"profileOrder", "triggers", "updatePolicy", "behavior", "pollingInterval"} {
		if _, ok := top[key]; ok {
			return 0, fmt.Errorf("%s requires configVersion: 2", key)
		}
	}
	return 0, nil
}

func Parse(data []byte) (*Config, error) {
	v, err := Version(data)
	if err != nil {
		return nil, err
	}
	if v != 2 {
		return nil, fmt.Errorf("configVersion: explicit integer 2 required")
	}
	if err := uniqueJSON(data); err != nil {
		return nil, err
	}
	if err := validateStructure(data); err != nil {
		return nil, err
	}
	c := Defaults()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func uniqueJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var read func(string) error
	read = func(path string) error {
		t, err := d.Token()
		if err != nil {
			return err
		}
		if t == nil {
			return fmt.Errorf("%s: null is not a policy value", path)
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		if delim == '{' {
			seen := map[string]bool{}
			for d.More() {
				t, err := d.Token()
				if err != nil {
					return err
				}
				key := t.(string)
				if seen[key] {
					return fmt.Errorf("%s.%s: duplicate key", path, key)
				}
				seen[key] = true
				if err := read(path + "." + key); err != nil {
					return err
				}
			}
		} else if delim == '[' {
			for i := 0; d.More(); i++ {
				if err := read(fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		} else {
			return fmt.Errorf("%s: unexpected delimiter", path)
		}
		_, err = d.Token()
		return err
	}
	if err := read("policy"); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("policy: trailing JSON")
	}
	return nil
}

func (p Profile) Resources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(p.Requests.CPU), corev1.ResourceMemory: resource.MustParse(p.Requests.Memory)}, Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(p.Limits.CPU), corev1.ResourceMemory: resource.MustParse(p.Limits.Memory)}}
}
func Equal(a, b corev1.ResourceRequirements) bool {
	for _, pair := range [][2]corev1.ResourceList{{a.Requests, b.Requests}, {a.Limits, b.Limits}} {
		for _, key := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			av, aok := pair[0][key]
			bv, bok := pair[1][key]
			if !aok || !bok || av.Cmp(bv) != 0 {
				return false
			}
		}
	}
	return true
}

// Dominates compares all four fields, never CPU alone.
func Dominates(a, b corev1.ResourceRequirements) bool {
	for _, pair := range [][2]corev1.ResourceList{{a.Requests, b.Requests}, {a.Limits, b.Limits}} {
		for _, key := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			av, aok := pair[0][key]
			bv, bok := pair[1][key]
			if !aok || !bok || av.Cmp(bv) < 0 {
				return false
			}
		}
	}
	return true
}
func Known(r corev1.ResourceRequirements) bool {
	for _, group := range []corev1.ResourceList{r.Requests, r.Limits} {
		for _, key := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			q, ok := group[key]
			if !ok || q.Sign() <= 0 {
				return false
			}
		}
	}
	return true
}

func (c *Config) Validate() error {
	fail := func(path, msg string) error { return fmt.Errorf("%s: %s", path, msg) }
	if c.Version != 2 {
		return fail("configVersion", "must be 2")
	}
	if len(c.Order) == 0 || len(c.Order) > 64 || len(c.Order) != len(c.Profiles) {
		return fail("profileOrder", "must list every profile once (1 to 64 profiles)")
	}
	seen := map[string]bool{}
	var previous corev1.ResourceRequirements
	for i, name := range c.Order {
		p, ok := c.Profiles[name]
		if !ok || seen[name] || !regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,62}$`).MatchString(name) {
			return fail("profileOrder", "invalid, duplicate or missing profile "+name)
		}
		seen[name] = true
		for _, group := range []struct {
			name string
			a    Amounts
		}{{"requests", p.Requests}, {"limits", p.Limits}} {
			for _, field := range []struct{ name, value string }{{"cpu", group.a.CPU}, {"memory", group.a.Memory}} {
				q, err := resource.ParseQuantity(field.value)
				if err != nil || q.Sign() <= 0 {
					return fail("profiles."+name+"."+group.name+"."+field.name, "must be a positive Kubernetes quantity")
				}
				ceiling := resource.MustParse("1Ei")
				if field.name == "cpu" {
					ceiling = resource.MustParse("1000000")
					rounded := resource.NewMilliQuantity(q.MilliValue(), resource.DecimalSI)
					if q.Cmp(*rounded) != 0 {
						return fail("profiles."+name, "CPU precision must be whole millicores")
					}
				}
				if q.Cmp(ceiling) > 0 {
					return fail("profiles."+name, "quantity exceeds supported arithmetic bounds")
				}
			}
		}
		r := p.Resources()
		for _, key := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			request, limit := r.Requests[key], r.Limits[key]
			if request.Cmp(limit) > 0 {
				return fail("profiles."+name, "requests exceed limits")
			}
		}
		if i > 0 && (!Dominates(r, previous) || Equal(r, previous)) {
			return fail("profiles."+name, "must increase at least one field and decrease none")
		}
		previous = r
	}
	if len(c.Triggers) == 0 || len(c.Triggers) > 32 {
		return fail("triggers", "requires 1 to 32 triggers")
	}
	for i := range c.Triggers {
		t := &c.Triggers[i]
		m := &t.Metadata
		path := fmt.Sprintf("triggers[%d]", i)
		switch t.Type {
		case "cpu", "memory":
			if t.MetricType != "Utilization" || (t.Aggregation != "" && t.Aggregation != "max") {
				return fail(path, "requires metricType Utilization and aggregation max")
			}
			t.Aggregation = "max"
			if m.ContainerName == "" {
				m.ContainerName = "rabbitmq"
			}
			if m.ContainerName != "rabbitmq" {
				return fail(path+".metadata.containerName", "must target rabbitmq")
			}
			if m.Value == "" {
				if t.Type == "cpu" {
					m.Value = "60"
				} else {
					m.Value = "75"
				}
			}
			v, err := strconv.ParseFloat(m.Value, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v > 1000000 {
				return fail(path+".metadata.value", "must be a positive finite percentage (up to 1000000)")
			}
			if m.Mode != "" || m.Aggregation != "" || m.Thresholds != nil || m.Selector != nil {
				return fail(path, "RabbitMQ metadata is not valid for utilization")
			}
		case "rabbitmq":
			if t.MetricType != "" || t.Aggregation != "" || m.Value != "" || m.ContainerName != "" {
				return fail(path, "utilization fields are not valid for RabbitMQ")
			}
			if m.Mode != "QueueLength" && m.Mode != "MessageRate" {
				return fail(path+".metadata.mode", "must be QueueLength or MessageRate")
			}
			if m.Mode == "QueueLength" {
				if m.Aggregation == "" {
					m.Aggregation = "sum"
				}
				if m.Aggregation != "sum" && m.Aggregation != "max" {
					return fail(path, "queue aggregation must be sum or max")
				}
			} else if m.Aggregation != "" {
				return fail(path, "MessageRate has fixed source-dependent aggregation")
			}
			if len(m.Thresholds) == 0 {
				return fail(path+".metadata.thresholds", "requires non-floor profile thresholds")
			}
			last := float64(0)
			for _, name := range c.Order[1:] {
				if v, ok := m.Thresholds[name]; ok {
					if math.IsNaN(v) || math.IsInf(v, 0) || v <= last || v > 1e15 {
						return fail(path, "thresholds must be positive finite and strictly increasing")
					}
					last = v
				}
			}
			for name := range m.Thresholds {
				if !seen[name] || name == c.Order[0] {
					return fail(path, "threshold refers to floor or unknown profile "+name)
				}
			}
			if m.Selector != nil {
				vs := map[string]bool{}
				for _, v := range m.Selector.Vhosts {
					if v == "" || vs[v] {
						return fail(path, "selector.vhosts must contain distinct nonempty names")
					}
					vs[v] = true
				}
				if _, err := regexp.Compile(m.Selector.QueueNameRegex); err != nil {
					return fail(path+".metadata.selector.queueNameRegex", err.Error())
				}
			}
		default:
			return fail(path+".type", "unsupported trigger")
		}
	}
	if c.Update.Mode != "Off" && c.Update.Mode != "Auto" {
		return fail("updatePolicy.mode", "must be Off or Auto")
	}
	if c.PollingInterval < 1 || c.PollingInterval > 3600 {
		return fail("pollingInterval", "must be 1 to 3600 integer seconds")
	}
	if c.Behavior.ScaleUp.StabilizationSeconds < 0 || c.Behavior.ScaleUp.StabilizationSeconds > 86400 {
		return fail("behavior.scaleUp.stabilizationSeconds", "must be 0 to 86400")
	}
	if c.Behavior.ScaleDown.StepIntervalSeconds < 60 || c.Behavior.ScaleDown.StepIntervalSeconds > 86400 {
		return fail("behavior.scaleDown.stepIntervalSeconds", "must be 60 to 86400")
	}
	if c.Behavior.ScaleDown.MemoryHeadroomPercent < 0 || c.Behavior.ScaleDown.MemoryHeadroomPercent >= 100 {
		return fail("behavior.scaleDown.memoryHeadroomPercent", "must be 0 to 99")
	}
	if c.Behavior.Metrics.MaxAgeSeconds < 1 || c.Behavior.Metrics.MaxAgeSeconds > 3600 {
		return fail("behavior.metrics.maxAgeSeconds", "must be 1 to 3600")
	}
	if len(validation.IsDNS1123Label(c.Target.Namespace)) > 0 || len(validation.IsDNS1123Subdomain(c.Target.ServiceName)) > 0 {
		return fail("kubernetes", "requires valid namespace and rmqServiceName")
	}
	if c.NeedsRabbitMQ() {
		if c.RMQ == nil || strings.TrimSpace(c.RMQ.Host) == "" {
			return fail("rmq.host", "required by RabbitMQ triggers")
		}
		n, err := strconv.Atoi(c.RMQ.Port)
		if err != nil || n < 1 || n > 65535 {
			return fail("rmq.port", "must be a port string from 1 to 65535")
		}
	}
	return nil
}
func (c *Config) NeedsRabbitMQ() bool {
	for _, t := range c.Triggers {
		if t.Type == "rabbitmq" {
			return true
		}
	}
	return false
}
func (c *Config) Hash() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (c *Config) Index(r corev1.ResourceRequirements) int {
	for i, name := range c.Order {
		if Equal(c.Profiles[name].Resources(), r) {
			return i
		}
	}
	return -1
}
func RejectLegacyEnv() error {
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "PROFILE_NAMES" || key == "PROFILE_COUNT" || strings.HasPrefix(key, "PROFILE_") || strings.HasPrefix(key, "QUEUE_THRESHOLD_") || strings.HasPrefix(key, "RATE_THRESHOLD_") || strings.HasPrefix(key, "DEBOUNCE_") || key == "CHECK_INTERVAL_SECONDS" || key == "SCALE_MODE" || key == "RMQ_HOST" || key == "RMQ_PORT" || key == "RMQ_SERVICE_NAME" || key == "CONFIG_MAP_NAME" || key == "NAMESPACE" {
			return fmt.Errorf("legacy environment %s conflicts with --config", key)
		}
	}
	return nil
}
