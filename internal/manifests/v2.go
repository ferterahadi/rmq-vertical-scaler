package manifests

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/policy"
	"k8s.io/apimachinery/pkg/util/validation"
)

// GenerateV2 emits JSON documents (valid YAML) to avoid quoting policy/selector
// values through a text template. Runtime status is intentionally not emitted.
func GenerateV2(flags Flags, data []byte) (string, Summary, error) {
	p, err := policy.Parse(data)
	if err != nil {
		return "", Summary{}, err
	}
	if flags.Namespace != "" && flags.Namespace != p.Target.Namespace {
		return "", Summary{}, fmt.Errorf("--namespace conflicts with policy.kubernetes.namespace")
	}
	if flags.ServiceName != "" && flags.ServiceName != p.Target.ServiceName {
		return "", Summary{}, fmt.Errorf("--service-name conflicts with policy.kubernetes.rmqServiceName")
	}
	name := firstNonEmpty(flags.ScalerName, "rmq-vertical-scaler")
	if len(name) > 45 || len(validation.IsDNS1123Label(name)) > 0 {
		return "", Summary{}, fmt.Errorf("scaler name must be a DNS label up to 45 characters")
	}
	ns := p.Target.Namespace
	object := func(api, kind, n string, fields map[string]any) map[string]any {
		o := map[string]any{"apiVersion": api, "kind": kind, "metadata": map[string]any{"name": n, "namespace": ns}}
		for k, v := range fields {
			o[k] = v
		}
		return o
	}
	rules := []any{
		map[string]any{"apiGroups": []string{"rabbitmq.com"}, "resources": []string{"rabbitmqclusters"}, "resourceNames": []string{p.Target.ServiceName}, "verbs": []string{"get"}},
		map[string]any{"apiGroups": []string{"apps"}, "resources": []string{"statefulsets"}, "verbs": []string{"get", "list"}},
		map[string]any{"apiGroups": []string{""}, "resources": []string{"pods"}, "verbs": []string{"get", "list"}},
		map[string]any{"apiGroups": []string{"metrics.k8s.io"}, "resources": []string{"pods"}, "verbs": []string{"get", "list"}},
		map[string]any{"apiGroups": []string{""}, "resources": []string{"configmaps"}, "resourceNames": []string{name + "-status"}, "verbs": []string{"get", "update"}},
		map[string]any{"apiGroups": []string{""}, "resources": []string{"configmaps"}, "verbs": []string{"create"}},
	}
	if p.Update.Mode == "Auto" {
		rules = append(rules, map[string]any{"apiGroups": []string{"rabbitmq.com"}, "resources": []string{"rabbitmqclusters"}, "resourceNames": []string{p.Target.ServiceName}, "verbs": []string{"patch"}}, map[string]any{"apiGroups": []string{""}, "resources": []string{"pods/resize"}, "verbs": []string{"patch"}})
	}
	policyBytes, _ := json.Marshal(p)
	env := []any{map[string]any{"name": "STATUS_CONFIG_MAP", "value": name + "-status"}}
	if p.NeedsRabbitMQ() {
		for _, item := range []struct{ name, key string }{{"RMQ_USER", "username"}, {"RMQ_PASS", "password"}} {
			env = append(env, map[string]any{"name": item.name, "valueFrom": map[string]any{"secretKeyRef": map[string]string{"name": p.Target.ServiceName + "-default-user", "key": item.key}}})
		}
	}
	image := flags.Image
	if image == "" {
		match := releaseVersion.FindStringSubmatch(flags.Version)
		if match == nil {
			return "", Summary{}, fmt.Errorf("version-2 generation from an unreleased build requires --image pointing to a build with policy support")
		}
		parts := strings.Split(match[1], ".")
		major, _ := strconv.Atoi(parts[0])
		minor, _ := strconv.Atoi(parts[1])
		if major < 2 || (major == 2 && minor < 3) {
			return "", Summary{}, fmt.Errorf("version-2 policy requires a 2.3-or-newer image; supply --image explicitly")
		}
		image = defaultImage(flags.Version)
	}
	container := map[string]any{"name": "scaler", "image": image, "args": []string{"run", "--config", "/etc/rmq-scaler/policy.json"}, "env": env, "volumeMounts": []any{map[string]any{"name": "policy", "mountPath": "/etc/rmq-scaler", "readOnly": true}}, "resources": map[string]any{"requests": map[string]string{"cpu": "75m", "memory": "128Mi"}, "limits": map[string]string{"cpu": "200m", "memory": "512Mi"}}}
	objects := []map[string]any{
		object("v1", "ServiceAccount", name+"-sa", nil),
		object("rbac.authorization.k8s.io/v1", "Role", name+"-role", map[string]any{"rules": rules}),
		object("rbac.authorization.k8s.io/v1", "RoleBinding", name+"-binding", map[string]any{"roleRef": map[string]string{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": name + "-role"}, "subjects": []any{map[string]string{"kind": "ServiceAccount", "name": name + "-sa", "namespace": ns}}}),
		object("v1", "ConfigMap", name+"-policy", map[string]any{"data": map[string]string{"policy.json": string(policyBytes)}}),
		object("apps/v1", "Deployment", name, map[string]any{"spec": map[string]any{"replicas": 1, "strategy": map[string]string{"type": "Recreate"}, "selector": map[string]any{"matchLabels": map[string]string{"app": name}}, "template": map[string]any{"metadata": map[string]any{"labels": map[string]string{"app": name}, "annotations": map[string]string{"checksum/policy": p.Hash()}}, "spec": map[string]any{"serviceAccountName": name + "-sa", "containers": []any{container}, "volumes": []any{map[string]any{"name": "policy", "configMap": map[string]string{"name": name + "-policy"}}}}}}}),
	}
	if !flags.NoPDB {
		objects = append(objects, object("policy/v1", "PodDisruptionBudget", p.Target.ServiceName+"-pdb", map[string]any{"spec": map[string]any{"minAvailable": 2, "selector": map[string]any{"matchLabels": map[string]string{"app.kubernetes.io/name": p.Target.ServiceName}}}}))
	}
	var docs []string
	for _, o := range objects {
		b, err := json.MarshalIndent(o, "", "  ")
		if err != nil {
			return "", Summary{}, err
		}
		docs = append(docs, string(b))
	}
	s := Summary{ConfigVersion: 2, UpdateMode: p.Update.Mode, Namespace: ns, ServiceName: p.Target.ServiceName, Image: image, ScalerName: name, Output: firstNonEmpty(flags.Output, "rmq-scaler.yaml"), ScaleUpSeconds: fmt.Sprint(p.Behavior.ScaleUp.StabilizationSeconds), ScaleDnSeconds: fmt.Sprint(p.Behavior.ScaleDown.StepIntervalSeconds), CheckInterval: fmt.Sprint(p.PollingInterval), QueueThresholds: map[string]string{}, RateThresholds: map[string]string{}}
	for _, name := range p.Order {
		r := p.Profiles[name]
		s.Profiles = append(s.Profiles, Profile{Name: name, CPU: r.Requests.CPU, Memory: r.Requests.Memory, LimitCPU: r.Limits.CPU, LimitMemory: r.Limits.Memory})
	}
	return strings.Join(docs, "\n---\n"), s, nil
}
