// Package manifests generates the Kubernetes deployment YAML, porting the
// generate logic of bin/rmq-vertical-scaler. Output is byte-for-byte compatible
// with v1: object key order is preserved (via streaming JSON parsing) because
// the emitted env-var order is part of the contract the parity gate checks.
package manifests

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/template"
)

//go:embed deployment.tmpl
var deploymentTmpl string

// deploymentTemplate is parsed once at package load; a malformed embedded
// template is a programmer error and panics here rather than per-call.
var deploymentTemplate = template.Must(template.New("deployment").Parse(deploymentTmpl))

// Flags are the raw CLI inputs (empty string means "not provided").
type Flags struct {
	Namespace   string
	ServiceName string
	Image       string
	ScalerName  string
	Output      string
}

// Summary reports the resolved configuration for human-readable CLI output.
type Summary struct {
	Namespace       string
	ServiceName     string
	Image           string
	ScalerName      string
	Output          string
	ScaleUpSeconds  string
	ScaleDnSeconds  string
	CheckInterval   string
	Profiles        []Profile
	QueueThresholds map[string]string
	RateThresholds  map[string]string
}

// Profile is one ordered scaling profile parsed from the config.
type Profile struct {
	Name   string
	CPU    string
	Memory string
	Queue  *string // formatted threshold, nil if absent
	Rate   *string
}

type namedVal struct{ Name, Value string }

var defaultProfiles = []Profile{
	{Name: "LOW", CPU: "330m", Memory: "2Gi"},
	{Name: "MEDIUM", CPU: "800m", Memory: "3Gi"},
	{Name: "HIGH", CPU: "1600m", Memory: "4Gi"},
	{Name: "CRITICAL", CPU: "2400m", Memory: "8Gi"},
}

var defaultQueueThresholds = []namedVal{{"MEDIUM", "2000"}, {"HIGH", "10000"}, {"CRITICAL", "50000"}}
var defaultRateThresholds = []namedVal{{"MEDIUM", "200"}, {"HIGH", "1000"}, {"CRITICAL", "2000"}}

type rawTop struct {
	Profiles   json.RawMessage `json:"profiles"`
	Thresholds *struct {
		Queue json.RawMessage `json:"queue"`
		Rate  json.RawMessage `json:"rate"`
	} `json:"thresholds"`
	Debounce *struct {
		ScaleUpSeconds   *json.Number `json:"scaleUpSeconds"`
		ScaleDownSeconds *json.Number `json:"scaleDownSeconds"`
	} `json:"debounce"`
	CheckInterval *json.Number `json:"checkInterval"`
	RMQ           *struct {
		Host string `json:"host"`
		Port string `json:"port"`
	} `json:"rmq"`
	Kubernetes *struct {
		Namespace      string `json:"namespace"`
		RMQServiceName string `json:"rmqServiceName"`
	} `json:"kubernetes"`
}

type tmplData struct {
	ServiceAccount  string
	Role            string
	RoleBinding     string
	ConfigMap       string
	PDB             string
	Deployment      string
	Namespace       string
	ServiceName     string
	Image           string
	RMQHost         string
	RMQPort         string
	ProfileCount    int
	ProfileNamesStr string
	EnvVars         string
	ScaleUpSeconds  string
	ScaleDnSeconds  string
	CheckInterval   string
}

// Generate renders the manifest YAML. configJSON is nil when no --config file
// was supplied. CLI flags take precedence over config-file values, which take
// precedence over the v1 defaults.
func Generate(flags Flags, configJSON []byte) (string, Summary, error) {
	var top rawTop
	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &top); err != nil {
			return "", Summary{}, fmt.Errorf("parse config: %w", err)
		}
	}

	namespace := firstNonEmpty(flags.Namespace, kubeNamespace(top), "default")
	serviceName := firstNonEmpty(flags.ServiceName, kubeServiceName(top), "rabbitmq")
	image := firstNonEmpty(flags.Image, "ferterahadi/rmq-vertical-scaler:latest")
	scalerName := firstNonEmpty(flags.ScalerName, "rmq-vertical-scaler")
	output := firstNonEmpty(flags.Output, "rmq-scaler.yaml")

	profiles, err := resolveProfiles(top.Profiles)
	if err != nil {
		return "", Summary{}, err
	}
	queueThresholds, rateThresholds, err := extractThresholds(top, profiles)
	if err != nil {
		return "", Summary{}, err
	}

	scaleUp, scaleDn := resolveDebounce(top)
	interval := resolveCheckInterval(top)
	rmqHost, rmqPort := resolveRMQ(top, serviceName, namespace)

	names := make([]string, len(profiles))
	for i, p := range profiles {
		names[i] = p.Name
	}

	data := tmplData{
		ServiceAccount:  scalerName + "-sa",
		Role:            scalerName + "-role",
		RoleBinding:     scalerName + "-binding",
		ConfigMap:       scalerName + "-config",
		PDB:             serviceName + "-pdb",
		Deployment:      scalerName,
		Namespace:       namespace,
		ServiceName:     serviceName,
		Image:           image,
		RMQHost:         rmqHost,
		RMQPort:         rmqPort,
		ProfileCount:    len(profiles),
		ProfileNamesStr: strings.Join(names, " "),
		EnvVars:         profileEnvVars(profiles) + thresholdEnvVars(queueThresholds, rateThresholds),
		ScaleUpSeconds:  scaleUp,
		ScaleDnSeconds:  scaleDn,
		CheckInterval:   interval,
	}

	var buf bytes.Buffer
	if err := deploymentTemplate.Execute(&buf, data); err != nil {
		return "", Summary{}, err
	}
	// v1's template literal has no trailing newline.
	out := strings.TrimRight(buf.String(), "\n")

	summary := Summary{
		Namespace:       namespace,
		ServiceName:     serviceName,
		Image:           image,
		ScalerName:      scalerName,
		Output:          output,
		ScaleUpSeconds:  scaleUp,
		ScaleDnSeconds:  scaleDn,
		CheckInterval:   interval,
		Profiles:        profiles,
		QueueThresholds: namedValsToMap(queueThresholds),
		RateThresholds:  namedValsToMap(rateThresholds),
	}
	return out, summary, nil
}

func resolveProfiles(raw json.RawMessage) ([]Profile, error) {
	if len(raw) == 0 {
		return defaultProfiles, nil
	}
	entries, err := orderedObject(raw)
	if err != nil {
		return nil, fmt.Errorf("parse profiles: %w", err)
	}
	profiles := make([]Profile, 0, len(entries))
	for _, e := range entries {
		var p struct {
			CPU    string       `json:"cpu"`
			Memory string       `json:"memory"`
			Queue  *json.Number `json:"queue"`
			Rate   *json.Number `json:"rate"`
		}
		if err := json.Unmarshal(e.raw, &p); err != nil {
			return nil, fmt.Errorf("parse profile %q: %w", e.name, err)
		}
		prof := Profile{Name: e.name, CPU: p.CPU, Memory: p.Memory}
		if p.Queue != nil {
			s := formatNumber(*p.Queue)
			prof.Queue = &s
		}
		if p.Rate != nil {
			s := formatNumber(*p.Rate)
			prof.Rate = &s
		}
		profiles = append(profiles, prof)
	}
	return profiles, nil
}

// extractThresholds ports the v1 function: old format (config.thresholds) wins;
// otherwise thresholds are read from the profiles; if none are found at all the
// hard-coded defaults are used.
func extractThresholds(top rawTop, profiles []Profile) ([]namedVal, []namedVal, error) {
	if top.Thresholds != nil {
		queue, err := parseThresholdMap(top.Thresholds.Queue)
		if err != nil {
			return nil, nil, fmt.Errorf("parse queue thresholds: %w", err)
		}
		rate, err := parseThresholdMap(top.Thresholds.Rate)
		if err != nil {
			return nil, nil, fmt.Errorf("parse rate thresholds: %w", err)
		}
		return queue, rate, nil
	}

	var queue, rate []namedVal
	for _, p := range profiles {
		if p.Queue != nil {
			queue = append(queue, namedVal{p.Name, *p.Queue})
		}
	}
	for _, p := range profiles {
		if p.Rate != nil {
			rate = append(rate, namedVal{p.Name, *p.Rate})
		}
	}
	if len(queue) == 0 && len(rate) == 0 {
		return defaultQueueThresholds, defaultRateThresholds, nil
	}
	return queue, rate, nil
}

func parseThresholdMap(raw json.RawMessage) ([]namedVal, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	entries, err := orderedObject(raw)
	if err != nil {
		return nil, err
	}
	out := make([]namedVal, 0, len(entries))
	for _, e := range entries {
		var n json.Number
		if err := json.Unmarshal(e.raw, &n); err != nil {
			return nil, err
		}
		out = append(out, namedVal{e.name, formatNumber(n)})
	}
	return out, nil
}

func profileEnvVars(profiles []Profile) string {
	var b strings.Builder
	for _, p := range profiles {
		fmt.Fprintf(&b, "\n            # Profile %s resources", p.Name)
		fmt.Fprintf(&b, "\n            - name: PROFILE_%s_CPU\n              value: '%s'", p.Name, p.CPU)
		fmt.Fprintf(&b, "\n            - name: PROFILE_%s_MEMORY\n              value: '%s'", p.Name, p.Memory)
	}
	return b.String()
}

func thresholdEnvVars(queue, rate []namedVal) string {
	var b strings.Builder
	for _, q := range queue {
		fmt.Fprintf(&b, "\n            # %s thresholds", q.Name)
		fmt.Fprintf(&b, "\n            - name: QUEUE_THRESHOLD_%s\n              value: '%s'", q.Name, q.Value)
	}
	for _, r := range rate {
		fmt.Fprintf(&b, "\n            - name: RATE_THRESHOLD_%s\n              value: '%s'", r.Name, r.Value)
	}
	return b.String()
}

// --- helpers ---

type orderedEntry struct {
	name string
	raw  json.RawMessage
}

// orderedObject parses a JSON object preserving key order.
func orderedObject(raw json.RawMessage) ([]orderedEntry, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected JSON object")
	}
	var out []orderedEntry
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("expected string key")
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
		out = append(out, orderedEntry{name: key, raw: val})
	}
	return out, nil
}

// formatNumber renders a JSON number the way JS string interpolation would:
// integers stay integers, fractions keep their minimal representation.
func formatNumber(n json.Number) string {
	f, err := n.Float64()
	if err != nil {
		return n.String()
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func kubeNamespace(top rawTop) string {
	if top.Kubernetes != nil {
		return top.Kubernetes.Namespace
	}
	return ""
}

func kubeServiceName(top rawTop) string {
	if top.Kubernetes != nil {
		return top.Kubernetes.RMQServiceName
	}
	return ""
}

func resolveDebounce(top rawTop) (up, down string) {
	up, down = "30", "120"
	if top.Debounce != nil {
		if top.Debounce.ScaleUpSeconds != nil {
			up = formatNumber(*top.Debounce.ScaleUpSeconds)
		}
		if top.Debounce.ScaleDownSeconds != nil {
			down = formatNumber(*top.Debounce.ScaleDownSeconds)
		}
	}
	return up, down
}

// resolveCheckInterval mirrors `config.checkInterval || 5`: a missing or zero
// value falls back to 5.
func resolveCheckInterval(top rawTop) string {
	if top.CheckInterval != nil {
		if f, err := top.CheckInterval.Float64(); err == nil && f != 0 {
			return formatNumber(*top.CheckInterval)
		}
	}
	return "5"
}

func resolveRMQ(top rawTop, serviceName, namespace string) (host, port string) {
	if top.RMQ != nil {
		return top.RMQ.Host, top.RMQ.Port
	}
	return fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace), "15672"
}

func namedValsToMap(vals []namedVal) map[string]string {
	m := make(map[string]string, len(vals))
	for _, v := range vals {
		m[v.Name] = v.Value
	}
	return m
}
