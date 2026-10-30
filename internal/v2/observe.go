package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/policy"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

var clusterGVR = schema.GroupVersionResource{Group: "rabbitmq.com", Version: "v1beta1", Resource: "rabbitmqclusters"}
var metricsGVR = schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}

type Snapshot struct {
	Observation
	Cluster           *unstructured.Unstructured  `json:"-"`
	StatefulSet       *appsv1.StatefulSet         `json:"-"`
	ClusterResources  corev1.ResourceRequirements `json:"clusterDesired"`
	TemplateResources corev1.ResourceRequirements `json:"templateDesired"`
}
type Backend struct {
	Policy   *policy.Config
	Core     kubernetes.Interface
	Dynamic  dynamic.Interface
	HTTP     *http.Client
	User     string
	Password string
}

func owner(meta metav1.Object, kind string, uid types.UID) bool {
	for _, r := range meta.GetOwnerReferences() {
		if r.Controller != nil && *r.Controller && r.Kind == kind && r.UID == uid {
			return true
		}
	}
	return false
}
func rabbitContainer(containers []corev1.Container) (corev1.Container, bool) {
	for _, c := range containers {
		if c.Name == "rabbitmq" {
			return c, true
		}
	}
	return corev1.Container{}, false
}
func decodeResources(o map[string]any) (corev1.ResourceRequirements, error) {
	var r corev1.ResourceRequirements
	b, err := json.Marshal(o)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	return r, err
}
func (b *Backend) Read(ctx context.Context, at time.Time) (Snapshot, error) {
	s := Snapshot{Observation: Observation{At: at, Current: -1}}
	cr, err := b.Dynamic.Resource(clusterGVR).Namespace(b.Policy.Target.Namespace).Get(ctx, b.Policy.Target.ServiceName, metav1.GetOptions{})
	if err != nil {
		return s, fmt.Errorf("read RabbitmqCluster: %w", err)
	}
	s.Cluster = cr
	crResources, found, err := unstructured.NestedMap(cr.Object, "spec", "resources")
	if err != nil || !found {
		return s, fmt.Errorf("cluster resources unavailable")
	}
	s.ClusterResources, err = decodeResources(crResources)
	if err != nil {
		return s, err
	}
	expected, found, err := unstructured.NestedInt64(cr.Object, "spec", "replicas")
	if err != nil || !found || expected < 1 {
		return s, fmt.Errorf("expected replica count unavailable")
	}
	sets, err := b.Core.AppsV1().StatefulSets(b.Policy.Target.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return s, err
	}
	for i := range sets.Items {
		st := &sets.Items[i]
		if owner(st, "RabbitmqCluster", cr.GetUID()) {
			if s.StatefulSet != nil {
				return s, fmt.Errorf("multiple owned StatefulSets")
			}
			s.StatefulSet = st
		}
	}
	st := s.StatefulSet
	if st == nil || st.Spec.Replicas == nil || int64(*st.Spec.Replicas) != expected {
		return s, fmt.Errorf("owned StatefulSet replica count unavailable or changed")
	}
	container, ok := rabbitContainer(st.Spec.Template.Spec.Containers)
	if !ok {
		return s, fmt.Errorf("RabbitMQ template container unavailable")
	}
	s.TemplateResources = container.Resources
	pods, err := b.Core.CoreV1().Pods(b.Policy.Target.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return s, err
	}
	owned := map[string]corev1.Pod{}
	for _, p := range pods.Items {
		if owner(&p, "StatefulSet", st.UID) {
			owned[p.Name] = p
		}
	}
	start := int32(0)
	if st.Spec.Ordinals != nil {
		start = st.Spec.Ordinals.Start
	}
	s.Complete = len(owned) == int(expected)
	ids := []string{string(cr.GetUID()), string(st.UID), fmt.Sprint(expected), fmt.Sprint(start)}
	for i := int32(0); int64(i) < expected; i++ {
		name := fmt.Sprintf("%s-%d", st.Name, start+i)
		pod, ok := owned[name]
		if !ok {
			s.Complete = false
			continue
		}
		c, ok := rabbitContainer(pod.Spec.Containers)
		if !ok {
			s.Complete = false
			continue
		}
		broker := Broker{Pod: pod, Name: name, UID: string(pod.UID), Desired: c.Resources, CurrentGeneration: pod.Status.ObservedGeneration >= pod.Generation}
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == "rabbitmq" {
				broker.ContainerID = cs.ContainerID
				broker.Restarts = cs.RestartCount
				broker.Ready = cs.Ready
				if cs.Resources != nil {
					broker.Effective = *cs.Resources
				}
				if cs.State.Running != nil {
					broker.Started = cs.State.Running.StartedAt.Time
				}
			}
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodResizePending || condition.Type == corev1.PodResizeInProgress {
				broker.Conditions = append(broker.Conditions, condition)
			}
		}
		if pod.DeletionTimestamp != nil || broker.ContainerID == "" || broker.Started.IsZero() {
			s.Complete = false
		}
		s.Brokers = append(s.Brokers, broker)
		ids = append(ids, name+"/"+broker.UID+"/"+broker.ContainerID)
	}
	sort.Strings(ids)
	s.Identity = strings.Join(ids, "|")
	if !s.Complete {
		s.Reason = "IncompleteBrokerCensus"
	}
	current := b.Policy.Index(s.ClusterResources)
	if !policy.Equal(s.ClusterResources, s.TemplateResources) {
		current = -1
	}
	for _, broker := range s.Brokers {
		if !policy.Equal(s.ClusterResources, broker.Desired) || !policy.Equal(s.ClusterResources, broker.Effective) || !broker.Ready || !broker.CurrentGeneration || activeResize(broker) {
			current = -1
		}
	}
	if s.Complete {
		s.Current = current
	}
	return s, nil
}
func activeResize(b Broker) bool {
	for _, c := range b.Conditions {
		if c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
func (b *Backend) resourceSamples(ctx context.Context, s Snapshot) ([]Sample, error) {
	list, err := b.Dynamic.Resource(metricsGVR).Namespace(b.Policy.Target.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	type metric struct {
		Metadata   metav1.ObjectMeta `json:"metadata"`
		Timestamp  metav1.Time       `json:"timestamp"`
		Window     metav1.Duration   `json:"window"`
		Containers []struct {
			Name  string              `json:"name"`
			Usage corev1.ResourceList `json:"usage"`
		} `json:"containers"`
	}
	brokers := map[string]Broker{}
	for _, v := range s.Brokers {
		brokers[v.Name] = v
	}
	var samples []Sample
	for _, item := range list.Items {
		broker, ok := brokers[item.GetName()]
		if !ok {
			continue
		}
		var m metric
		data, _ := json.Marshal(item.Object)
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		for _, c := range m.Containers {
			if c.Name == "rabbitmq" {
				cpu, cpuOK := c.Usage[corev1.ResourceCPU]
				mem, memOK := c.Usage[corev1.ResourceMemory]
				if !cpuOK && !memOK {
					continue
				}
				samples = append(samples, Sample{CPUUnavailable: !cpuOK, MemoryUnavailable: !memOK, Pod: m.Metadata.Name, UID: broker.UID, ContainerID: broker.ContainerID, Timestamp: m.Timestamp.Time, Window: m.Window.Duration, CPU: cpu.AsApproximateFloat64(), Memory: mem.AsApproximateFloat64()})
			}
		}
	}
	return samples, nil
}
func (b *Backend) Collect(ctx context.Context, s Snapshot) Snapshot {
	timeout := time.Duration(b.Policy.PollingInterval) * time.Second * 4 / 5
	if timeout > 10*time.Second {
		timeout = 10 * time.Second
	}
	var wg sync.WaitGroup
	var samples []Sample
	var signals []Signal
	var sampleErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		samples, sampleErr = b.resourceSamples(c, s)
	}()
	if b.Policy.NeedsRabbitMQ() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			signals = b.queueSignals(c, s.At)
		}()
	}
	wg.Wait()
	s.Samples = samples
	for i, t := range b.Policy.Triggers {
		if t.Type == "cpu" || t.Type == "memory" {
			signal := ResourceSignal(b.Policy, i, s.Observation)
			if sampleErr != nil {
				signal.Reason = "ResourceMetricsUnavailable"
			}
			signals = append(signals, signal)
		}
	}
	sort.Slice(signals, func(i, j int) bool { return signals[i].Trigger < signals[j].Trigger })
	s.Signals = signals
	return s
}
func (b *Backend) get(ctx context.Context, path string, out any) error {
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(b.Policy.RMQ.Host, b.Policy.RMQ.Port)}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String()+path, nil)
	if err != nil {
		return err
	}
	request.SetBasicAuth(b.User, b.Password)
	client := b.HTTP
	if client == nil {
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("management request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("management status %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(out)
}

type queue struct {
	Name         string   `json:"name"`
	Vhost        string   `json:"vhost"`
	Messages     *float64 `json:"messages"`
	MessageStats struct {
		Publish *float64 `json:"publish"`
		Details struct {
			Rate *float64 `json:"rate"`
		} `json:"publish_details"`
	} `json:"message_stats"`
}

func validValue(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
func selected(q queue, s *policy.Selector) bool {
	if s == nil {
		return true
	}
	if len(s.Vhosts) > 0 {
		found := false
		for _, v := range s.Vhosts {
			found = found || v == q.Vhost
		}
		if !found {
			return false
		}
	}
	return s.QueueNameRegex == "" || regexp.MustCompile(s.QueueNameRegex).MatchString(q.Name)
}
func (b *Backend) queueSignals(ctx context.Context, at time.Time) []Signal {
	// Queue lists are scoped to this API user's visibility. For overview publish
	// rates, require verified global visibility rather than silently mixing scopes.
	allQueues := map[string]queue{}
	complete := true
	available := false
	pages := 0
	expectedCount := -1
	for page := 1; page <= 10000; page++ {
		var raw json.RawMessage
		if err := b.get(ctx, fmt.Sprintf("/api/queues?page=%d&page_size=500&pagination=true", page), &raw); err != nil {
			complete = false
			break
		}
		var batch []queue
		if len(raw) > 0 && raw[0] == '[' {
			if json.Unmarshal(raw, &batch) != nil {
				complete = false
				break
			}
			pages = page
		} else {
			var result struct {
				Items     []queue `json:"items"`
				PageCount int     `json:"page_count"`
				Page      int     `json:"page"`
				Total     int     `json:"total_count"`
				Filtered  int     `json:"filtered_count"`
			}
			if json.Unmarshal(raw, &result) != nil || result.Page != page || result.PageCount < 0 || result.PageCount > 10000 {
				complete = false
				break
			}
			if page == 1 {
				pages = result.PageCount
				expectedCount = result.Total
			} else if pages != result.PageCount || expectedCount != result.Total {
				complete = false
			}
			if result.Filtered != result.Total {
				complete = false
			}
			batch = result.Items
		}
		available = true
		for _, q := range batch {
			key := q.Vhost + "\x00" + q.Name
			if _, exists := allQueues[key]; exists {
				complete = false
				continue
			}
			allQueues[key] = q
		}
		if page >= pages {
			break
		}
	}
	if expectedCount >= 0 && expectedCount != len(allQueues) {
		complete = false
	}
	var out []Signal
	var overview struct {
		MessageStats struct {
			Publish *float64 `json:"publish"`
			Details struct {
				Rate *float64 `json:"rate"`
			} `json:"publish_details"`
		} `json:"message_stats"`
	}
	overviewOK := false
	overviewRead := false
	for i, t := range b.Policy.Triggers {
		if t.Type != "rabbitmq" {
			continue
		}
		s := Signal{Trigger: i, Type: t.Metadata.Mode, Timestamp: at, Profile: 0, Source: "RabbitMQ selected queues", Available: available, Complete: complete}
		if t.Metadata.Mode == "MessageRate" && t.Metadata.Selector == nil {
			s.Source = "RabbitMQ overview cluster ingress"
			if !overviewRead {
				overviewRead = true
				overviewOK = b.overviewVisible(ctx) && b.get(ctx, "/api/overview", &overview) == nil
			}
			s.Available = overviewOK
			s.Complete = overviewOK
			if overviewOK && overview.MessageStats.Details.Rate != nil && validValue(*overview.MessageStats.Details.Rate) {
				s.Value = *overview.MessageStats.Details.Rate
			} else {
				s.Available = false
				s.Complete = false
				s.Reason = "OverviewPublishRateUnavailableOrScopeUnknown"
			}
		} else {
			validSelected := 0
			for _, q := range allQueues {
				if !selected(q, t.Metadata.Selector) {
					continue
				}
				var v *float64
				if t.Metadata.Mode == "QueueLength" {
					v = q.Messages
				} else {
					v = q.MessageStats.Details.Rate

				}
				if v == nil || !validValue(*v) {
					s.Complete = false
					continue
				}
				validSelected++
				if t.Metadata.Mode == "QueueLength" && t.Metadata.Aggregation == "max" {
					s.Value = math.Max(s.Value, *v)
				} else {
					s.Value += *v
				}
			}
			if !s.Complete && validSelected == 0 {
				s.Available = false
			}
			if !s.Complete {
				s.Reason = "IncompleteQueueEnumerationOrStatistics"
			}
		}
		if !validValue(s.Value) {
			s.Available = false
			s.Complete = false
			s.Reason = "InvalidAggregate"
		}
		for j, name := range b.Policy.Order {
			if threshold, ok := t.Metadata.Thresholds[name]; ok && s.Value > threshold {
				s.Profile = j
			}
		}
		out = append(out, s)
	}
	return out
}
func (b *Backend) overviewVisible(ctx context.Context) bool {
	var who struct {
		Name string `json:"name"`
		Tags any    `json:"tags"`
	}
	if b.get(ctx, "/api/whoami", &who) != nil {
		return false
	}
	administrator := false
	switch tags := who.Tags.(type) {
	case []any:
		for _, tag := range tags {
			administrator = administrator || tag == "administrator"
		}
	case string:
		for _, tag := range strings.Split(tags, ",") {
			administrator = administrator || strings.TrimSpace(tag) == "administrator"
		}
	}
	if !administrator {
		return false
	}
	var vhosts []struct {
		Name string `json:"name"`
	}
	var permissions []struct {
		User  string `json:"user"`
		Vhost string `json:"vhost"`
		Read  string `json:"read"`
	}
	if b.get(ctx, "/api/vhosts", &vhosts) != nil || b.get(ctx, "/api/permissions", &permissions) != nil {
		return false
	}
	for _, v := range vhosts {
		found := false
		for _, p := range permissions {
			if p.User == who.Name && p.Vhost == v.Name && p.Read == ".*" {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
