package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/policy"
	appsv1 "k8s.io/api/apps/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type Progress struct {
	AcceptedAt  time.Time                   `json:"acceptedAt,omitempty"`
	UID         string                      `json:"uid"`
	ContainerID string                      `json:"containerID"`
	Restarts    int32                       `json:"restarts"`
	Source      corev1.ResourceRequirements `json:"source"`
	Outcome     string                      `json:"outcome"`
	Attempts    int                         `json:"attempts"`
	NextAttempt time.Time                   `json:"nextAttempt,omitempty"`
	SubmittedAt time.Time                   `json:"submittedAt,omitempty"`
	Error       string                      `json:"error,omitempty"`
}
type Operation struct {
	TemplateSource     corev1.ResourceRequirements   `json:"templateSource"`
	PriorTargets       []corev1.ResourceRequirements `json:"priorTargets,omitempty"`
	Revision           int64                         `json:"revision"`
	PolicyHash         string                        `json:"policyHash"`
	ClusterUID         string                        `json:"clusterUID"`
	StatefulSetUID     string                        `json:"statefulSetUID"`
	Target             string                        `json:"target"`
	Resources          corev1.ResourceRequirements   `json:"resources"`
	Source             corev1.ResourceRequirements   `json:"source"`
	Direction          string                        `json:"direction"`
	Path               string                        `json:"path"`
	Stage              string                        `json:"stage"`
	Started            time.Time                     `json:"started"`
	EpisodeStart       time.Time                     `json:"episodeStart"`
	CompletedAt        time.Time                     `json:"completedAt,omitempty"`
	DeadlineMissed     bool                          `json:"deadlineMissed"`
	DisruptionObserved bool                          `json:"disruptionObserved"`
	Brokers            map[string]Progress           `json:"brokers"`
}
type Status struct {
	Revision      int64       `json:"revision"`
	Version       int         `json:"configVersion"`
	PolicyHash    string      `json:"policyHash"`
	Target        string      `json:"target"`
	At            time.Time   `json:"at"`
	Observation   Observation `json:"observation"`
	Decision      Decision    `json:"decision"`
	Phase         string      `json:"phase"`
	Reason        string      `json:"reason,omitempty"`
	Operation     *Operation  `json:"operation,omitempty"`
	LastOperation *Operation  `json:"lastOperation,omitempty"`
}
type Controller struct {
	Backend          *Backend
	StatusName       string
	Engine           *Engine
	Recovery         *Engine
	Log              *slog.Logger
	boot             time.Time
	recoveryBoundary boundary
	lastIdentity     string
	recoveryRequired bool
}

func NewController(b *Backend, statusName string, log *slog.Logger, at time.Time) *Controller {
	return &Controller{Backend: b, StatusName: statusName, Engine: NewEngine(b.Policy, at), Recovery: NewEngine(b.Policy, at), Log: log, boot: at}
}
func (c *Controller) load(ctx context.Context) (Status, *corev1.ConfigMap, error) {
	cm, err := c.Backend.Core.CoreV1().ConfigMaps(c.Backend.Policy.Target.Namespace).Get(ctx, c.StatusName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Status{Version: 2}, nil, nil
	}
	if err != nil {
		return Status{}, nil, err
	}
	var state Status
	if cm.Data["status.json"] == "" {
		return state, cm, fmt.Errorf("status ConfigMap exists without an operation document; refusing to overwrite")
	}
	if err = json.Unmarshal([]byte(cm.Data["status.json"]), &state); err != nil || state.Version != 2 {
		return state, cm, fmt.Errorf("malformed version-2 status; inspect before repairing")
	}
	return state, cm, nil
}
func (c *Controller) save(ctx context.Context, state Status, cm **corev1.ConfigMap, s Snapshot) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data) > 512*1024 {
		return fmt.Errorf("status exceeds bounded 512KiB document limit")
	}
	client := c.Backend.Core.CoreV1().ConfigMaps(c.Backend.Policy.Target.Namespace)
	if *cm == nil {
		object := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: c.StatusName, Namespace: c.Backend.Policy.Target.Namespace, Labels: map[string]string{"app.kubernetes.io/managed-by": "rmq-vertical-scaler", "rmq-vertical-scaler/target-uid": string(s.Cluster.GetUID())}}, Data: map[string]string{"status.json": string(data)}}
		*cm, err = client.Create(ctx, object, metav1.CreateOptions{})
	} else {
		object := (*cm).DeepCopy()
		if object.Labels["rmq-vertical-scaler/target-uid"] != string(s.Cluster.GetUID()) {
			return fmt.Errorf("status belongs to another RabbitmqCluster UID")
		}
		object.Data["status.json"] = string(data)
		*cm, err = client.Update(ctx, object, metav1.UpdateOptions{})
	}
	return err
}
func (c *Controller) Run(ctx context.Context) error {
	if c.Log == nil {
		c.Log = slog.Default()
	}
	c.Log.Info("version-2 policy selected", "mode", c.Backend.Policy.Update.Mode, "policyHash", c.Backend.Policy.Hash(), "pollingSeconds", c.Backend.Policy.PollingInterval)
	ticker := time.NewTicker(time.Duration(c.Backend.Policy.PollingInterval) * time.Second)
	defer ticker.Stop()
	for {
		stepCtx, cancel := context.WithTimeout(ctx, time.Duration(c.Backend.Policy.PollingInterval)*time.Second*9/10)
		err := c.Tick(stepCtx, time.Now())
		cancel()
		if err != nil {
			c.Log.Error("reconciliation held", "reason", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (c *Controller) Tick(ctx context.Context, at time.Time) error {
	s, err := c.Backend.Read(ctx, at)
	if err != nil {
		c.resetContinuity(at)
		return err
	}
	state, cm, err := c.load(ctx)
	if err != nil {
		c.resetContinuity(at)
		return err
	}
	s = c.Backend.Collect(ctx, s)
	if s.Identity != c.lastIdentity {
		if c.lastIdentity != "" && state.Operation != nil {
			c.recoveryRequired = true
		}
		c.Recovery.Reset(at)
		c.recoveryBoundary = boundary{}
		c.lastIdentity = s.Identity
	}
	state.Version = 2
	state.At = at
	state.PolicyHash = c.Backend.Policy.Hash()
	state.Target = c.Backend.Policy.Target.Namespace + "/" + c.Backend.Policy.Target.ServiceName
	state.Observation = s.Observation
	state.Decision = c.Engine.Evaluate(s.Observation, state.Operation != nil)
	state.Phase = "Observing"
	state.Reason = state.Decision.Reason
	op := state.Operation
	if op != nil && op.Direction == "up" && at.Sub(op.EpisodeStart) > 120*time.Second {
		op.DeadlineMissed = true
	}
	if op != nil && c.converged(s, op) {
		op.Stage = "Completed"
		op.CompletedAt = at
		state.LastOperation = op
		state.Operation = nil
		op = nil
		c.recoveryRequired = false
		c.Engine.Reset(at)
		c.Recovery.Reset(at)
		c.recoveryBoundary = boundary{}
		state.Phase = "Converged"
		state.Reason = "AllExpectedBrokersEffective"
		// The decision was evaluated while an operation was pending. A current-
		// profile candidate may cancel a downscale, but must not create a new no-op
		// operation immediately after the same target has just converged.
		if state.Decision.Target == s.Current {
			state.Decision.Target = -1
			state.Decision.Reason = "AlreadyAtRecommendedProfile"
		}
	}
	if c.Backend.Policy.Update.Mode == "Off" {
		state.Phase = "RecommendationOnly"
		if op != nil {
			state.Reason = "OffObservesAcceptedWorkWithoutNewWrites"
		}
		return c.save(ctx, state, &cm, s)
	}
	if op != nil {
		if op.ClusterUID != string(s.Cluster.GetUID()) || op.StatefulSetUID != string(s.StatefulSet.UID) {
			state.Phase = "Blocked"
			state.Reason = "OperationTargetIdentityChanged"
			c.resetContinuity(at)
			return c.save(ctx, state, &cm, s)
		}
		if reason := operationConflict(s, op); reason != "" {
			state.Phase = "Blocked"
			state.Reason = reason
			c.resetContinuity(at)
			return c.save(ctx, state, &cm, s)
		}
	}
	if op == nil && untrackedWork(s) {
		state.Phase = "Blocked"
		state.Reason = "UntrackedPendingOrMixedResourcesInspectRecovery"
		c.resetContinuity(at)
		return c.save(ctx, state, &cm, s)
	}
	// A higher stabilized target can supersede an unresolved target, including
	// a changed-policy operation, only after proving it decreases no live tuple.
	if state.Decision.Target >= 0 && (op == nil || c.higher(state.Decision.Target, op)) {
		candidate, reason := c.newOperation(ctx, s, state.Decision, op)
		if candidate == nil {
			state.Phase = "Blocked"
			state.Reason = reason
			return c.save(ctx, state, &cm, s)
		}
		if op != nil {
			op.Stage = "Superseded"
			op.CompletedAt = at
			state.LastOperation = op
		}
		revision := state.Revision
		if state.LastOperation != nil && state.LastOperation.Revision > revision {
			revision = state.LastOperation.Revision
		}
		if op != nil && op.Revision > revision {
			revision = op.Revision
		}
		if revision == math.MaxInt64 {
			return fmt.Errorf("operation revision exhausted")
		}
		candidate.Revision = revision + 1
		state.Revision = candidate.Revision
		state.Operation = candidate
		op = candidate
		if op.Direction == "down" {
			copyEngine := *c.Engine
			copyEngine.memory = map[string]memoryWindow{}
			for name, window := range c.Engine.memory {
				copyEngine.memory[name] = window
			}
			c.Recovery = &copyEngine
		}
		if err := c.save(ctx, state, &cm, s); err != nil {
			return fmt.Errorf("persist intent before writes: %w", err)
		}
	} else if op != nil {
		if op.ClusterUID != string(s.Cluster.GetUID()) || op.StatefulSetUID != string(s.StatefulSet.UID) || op.PolicyHash != c.Backend.Policy.Hash() {
			state.Phase = "Blocked"
			state.Reason = "TargetOrPolicyChangedInspectRetainedOperation"
			return c.save(ctx, state, &cm, s)
		}
		// Operations recorded before this process started require rebuilt observations.
		if (op.Direction == "down" || op.Started.Before(c.boot) || c.recoveryRequired) && !c.recoveryReady(s, op) {
			state.Phase = "Recovering"
			state.Reason = "RebuildingObservationWindow"
			return c.save(ctx, state, &cm, s)
		}
	}
	if op == nil {
		return c.save(ctx, state, &cm, s)
	}
	state.Phase = "Reconciling"
	if !s.Complete {
		state.Phase = "Blocked"
		state.Reason = "IncompleteBrokerCensus"
		return c.save(ctx, state, &cm, s)
	}
	if !policy.Equal(s.ClusterResources, op.Source) && !operationTuple(s.ClusterResources, op) {
		state.Phase = "Blocked"
		state.Reason = "ExternalResourceChange"
		return c.save(ctx, state, &cm, s)
	}
	reason, err := c.advance(ctx, s, &state, &cm)
	state.Reason = reason
	if reason == "ResizeInfeasibleTargetRetained" {
		state.Phase = "Blocked"
	}
	if err != nil {
		state.Phase = "Blocked"
		state.Reason = reason + ": " + err.Error()
	}
	if saveErr := c.save(ctx, state, &cm, s); saveErr != nil {
		return saveErr
	}
	if c.Log != nil {
		c.Log.Info("resize reconciliation", "phase", state.Phase, "reason", state.Reason, "target", op.Target, "revision", op.Revision, "path", op.Path, "deadlineMissed", op.DeadlineMissed)
	}
	return err
}
func operationTuple(tuple corev1.ResourceRequirements, op *Operation) bool {
	if policy.Equal(tuple, op.Resources) {
		return true
	}
	for _, prior := range op.PriorTargets {
		if policy.Equal(tuple, prior) {
			return true
		}
	}
	return false
}

func operationConflict(s Snapshot, op *Operation) string {
	if !policy.Equal(s.ClusterResources, op.Source) && !operationTuple(s.ClusterResources, op) {
		return "ExternalResourceChange"
	}
	if !policy.Equal(s.TemplateResources, op.Source) && !policy.Equal(s.TemplateResources, op.TemplateSource) && !operationTuple(s.TemplateResources, op) {
		return "ExternalTemplateResourceChange"
	}
	for _, b := range s.Brokers {
		progress, ok := op.Brokers[b.Name]
		if !ok {
			return "BrokerMembershipChanged"
		}
		if !policy.Equal(b.Desired, progress.Source) && !operationTuple(b.Desired, op) {
			return "ExternalPodResourceChange"
		}
	}
	return ""
}

func (c *Controller) resetContinuity(at time.Time) {
	c.Engine.Reset(at)
	c.Recovery.Reset(at)
	c.recoveryBoundary = boundary{}
	c.recoveryRequired = true
}
func untrackedWork(s Snapshot) bool {
	if !s.Complete || !policy.Equal(s.ClusterResources, s.TemplateResources) {
		return true
	}
	for _, b := range s.Brokers {
		if activeResize(b) || !b.CurrentGeneration || !policy.Equal(b.Desired, b.Effective) || !policy.Equal(b.Desired, s.ClusterResources) {
			return true
		}
	}
	return false
}

func (c *Controller) higher(index int, op *Operation) bool {
	target := c.Backend.Policy.Profiles[c.Backend.Policy.Order[index]].Resources()
	return policy.Dominates(target, op.Resources) && !policy.Equal(target, op.Resources)
}
func (c *Controller) newOperation(ctx context.Context, s Snapshot, d Decision, previous *Operation) (*Operation, string) {
	p := c.Backend.Policy
	target := p.Profiles[p.Order[d.Target]].Resources()
	if !s.Complete || !policy.Known(s.ClusterResources) {
		return nil, "IncompleteOrUnknownResources"
	}
	direction := "up"
	if s.Current >= 0 && d.Target < s.Current {
		direction = "down"
	}
	for _, b := range s.Brokers {
		if !policy.Known(b.Effective) || !policy.Known(b.Desired) {
			return nil, "UnknownEffectiveResources"
		}
		if direction == "up" && (!policy.Dominates(target, b.Desired) || !policy.Dominates(target, b.Effective)) {
			return nil, "MixedResourcesWouldDecrease"
		}
	}
	if direction == "up" && (!policy.Dominates(target, s.ClusterResources) || !policy.Dominates(target, s.TemplateResources)) {
		return nil, "PendingTemplateWouldDecrease"
	}
	path, reason := c.preflight(ctx, s, target)
	if path == "" {
		return nil, reason
	}
	revision := int64(1)
	if previous != nil {
		revision = previous.Revision + 1
	}
	episode := d.EpisodeStart
	if episode.IsZero() {
		episode = s.At
	}
	op := &Operation{Revision: revision, PolicyHash: p.Hash(), ClusterUID: string(s.Cluster.GetUID()), StatefulSetUID: string(s.StatefulSet.UID), Target: p.Order[d.Target], Resources: target, Source: s.ClusterResources, TemplateSource: s.TemplateResources, Direction: direction, Path: path, Stage: "Prepare", Started: s.At, EpisodeStart: episode, Brokers: map[string]Progress{}}
	if previous != nil {
		op.PriorTargets = append(append([]corev1.ResourceRequirements{}, previous.PriorTargets...), previous.Resources)
	}
	for _, b := range s.Brokers {
		op.Brokers[b.Name] = Progress{UID: b.UID, ContainerID: b.ContainerID, Restarts: b.Restarts, Source: b.Desired, Outcome: "NotSubmitted"}
	}
	return op, reason
}
func (c *Controller) recoveryReady(s Snapshot, op *Operation) bool {
	if !s.Complete {
		c.recoveryBoundary = boundary{}
		c.Recovery.ResetDown()
		return false
	}
	if op.Direction == "down" {
		index := -1
		for i, name := range c.Backend.Policy.Order {
			if name == op.Target {
				index = i
			}
		}
		ready, _ := c.Recovery.DownReady(s.Observation, index)
		return ready
	}
	latest := time.Time{}
	for _, signal := range s.Signals {
		if signal.Available && Fresh(s.At, signal.Timestamp, c.Backend.Policy.Behavior.Metrics.MaxAgeSeconds) && signal.Timestamp.After(latest) {
			latest = signal.Timestamp
		}
	}
	if latest.IsZero() {
		c.recoveryBoundary = boundary{}
		return false
	}
	for _, b := range s.Brokers {
		if !policy.Dominates(op.Resources, b.Desired) || !policy.Dominates(op.Resources, b.Effective) {
			c.recoveryBoundary = boundary{}
			return false
		}
	}
	r := &c.recoveryBoundary
	if r.Since.IsZero() {
		*r = boundary{Since: s.At, FirstSample: latest}
	}
	if latest.After(r.LastSample) {
		r.LastSample = latest
	}
	duration := time.Duration(c.Backend.Policy.Behavior.ScaleUp.StabilizationSeconds) * time.Second
	return s.At.Sub(r.Since) >= duration && r.LastSample.Sub(r.FirstSample) >= duration
}
func (c *Controller) preflight(ctx context.Context, s Snapshot, target corev1.ResourceRequirements) (string, string) {
	unsupported := ""
	for _, b := range s.Brokers {
		if !b.Ready {
			return "", "BrokerNotReady"
		}
		desired := b.Pod.DeepCopy()
		for i := range desired.Spec.Containers {
			if desired.Spec.Containers[i].Name == "rabbitmq" {
				desired.Spec.Containers[i].Resources = target
			}
		}
		if qos(desired) != string(b.Pod.Status.QOSClass) {
			unsupported = "QoSClassChange"
		}
		if !target.Limits.Memory().Equal(*b.Effective.Limits.Memory()) || !target.Limits.Memory().Equal(*b.Desired.Limits.Memory()) {
			unsupported = "MemoryLimitRequiresBrokerBudgetReload"
		}
		container, _ := rabbitContainer(b.Pod.Spec.Containers)
		for _, r := range container.ResizePolicy {
			if r.RestartPolicy == corev1.RestartContainer {
				a, bb := target.Requests[r.ResourceName], b.Desired.Requests[r.ResourceName]
				x, y := target.Limits[r.ResourceName], b.Desired.Limits[r.ResourceName]
				if a.Cmp(bb) != 0 || x.Cmp(y) != 0 {
					unsupported = "ContainerResizeRequiresRestart"
				}
			}
		}
	}
	if unsupported == "" {
		resources, err := c.Backend.Core.Discovery().ServerResourcesForGroupVersion("v1")
		if err != nil {
			return "", "ResizeDiscoveryUnavailable"
		}
		found := false
		for _, r := range resources.APIResources {
			found = found || r.Name == "pods/resize"
		}
		if !found {
			unsupported = "ResizeSubresourceUnsupported"
		}
	}
	if unsupported != "" {
		if !c.Backend.Policy.Update.AllowRollingFallback {
			return "", unsupported
		}
		if reason := rollingSafe(s); reason != "" {
			return "", reason
		}
		return "rolling", unsupported
	}
	review, err := c.Backend.Core.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{Namespace: c.Backend.Policy.Target.Namespace, Verb: "patch", Resource: "pods", Subresource: "resize"}}}, metav1.CreateOptions{})
	if err != nil || !review.Status.Allowed {
		return "", "ResizePermissionNotVerified"
	}
	return "inplace", "PreflightPassed"
}
func qos(p *corev1.Pod) string {
	guaranteed := true
	anyResources := false
	containers := append(append([]corev1.Container{}, p.Spec.InitContainers...), p.Spec.Containers...)
	for _, c := range containers {
		for _, r := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			request, rok := c.Resources.Requests[r]
			limit, lok := c.Resources.Limits[r]
			anyResources = anyResources || request.Sign() > 0 || limit.Sign() > 0
			guaranteed = guaranteed && rok && lok && request.Sign() > 0 && request.Cmp(limit) == 0
		}
	}
	if guaranteed {
		return string(corev1.PodQOSGuaranteed)
	}
	if anyResources {
		return string(corev1.PodQOSBurstable)
	}
	return string(corev1.PodQOSBestEffort)
}
func rollingSafe(s Snapshot) string {
	if len(s.Brokers) < 3 {
		return "RollingRequiresThreeHealthyBrokers"
	}
	container, ok := rabbitContainer(s.StatefulSet.Spec.Template.Spec.Containers)
	if !ok || container.Lifecycle == nil || container.Lifecycle.PreStop == nil || container.Lifecycle.PreStop.Exec == nil || !strings.Contains(strings.Join(container.Lifecycle.PreStop.Exec.Command, " "), "await_online_quorum_plus_one") {
		return "OperatorQuorumShutdownGuardRequired"
	}
	for _, b := range s.Brokers {
		if !b.Ready || b.Pod.Labels["skipPreStopChecks"] != "" {
			return "RollingRequiresHealthyBrokerQuorumGuards"
		}
	}
	if s.StatefulSet.Spec.Template.Labels["skipPreStopChecks"] != "" {
		return "RollingQuorumGuardDisabled"
	}
	return ""
}
func (c *Controller) patchCluster(ctx context.Context, s Snapshot, spec map[string]any) error {
	patch := map[string]any{"metadata": map[string]string{"uid": string(s.Cluster.GetUID()), "resourceVersion": s.Cluster.GetResourceVersion()}, "spec": spec}
	data, _ := json.Marshal(patch)
	_, err := c.Backend.Dynamic.Resource(clusterGVR).Namespace(c.Backend.Policy.Target.Namespace).Patch(ctx, s.Cluster.GetName(), types.MergePatchType, data, metav1.PatchOptions{})
	return err
}
func strategySpec(kind string) map[string]any {
	strategy := map[string]any{"type": kind, "rollingUpdate": nil}
	if kind == "RollingUpdate" {
		strategy["rollingUpdate"] = map[string]any{"partition": 0, "maxUnavailable": nil}
	}
	return map[string]any{"override": map[string]any{"statefulSet": map[string]any{"spec": map[string]any{"updateStrategy": strategy}}}}
}
func (c *Controller) advance(ctx context.Context, s Snapshot, state *Status, cm **corev1.ConfigMap) (string, error) {
	op := state.Operation
	if op.Path == "inplace" {
		path, reason := c.preflight(ctx, s, op.Resources)
		if path == "" {
			return reason, fmt.Errorf("in-place preflight no longer satisfied")
		}
		if path != "inplace" {
			op.Path = path
			op.Stage = "Prepare"
			return "PersistingExplicitFallbackAfterPreflightChange", c.save(ctx, *state, cm, s)
		}
	}
	// The durable target exists before strategy/resource writes. Observe actual
	// Operator-managed strategy, then CR/template resources, then submit to Pods.
	if op.Stage == "Prepare" {
		raw, _, _ := unstructuredStrategy(s)
		if raw != "OnDelete" {
			return "SettingOnDelete", c.patchCluster(ctx, s, strategySpec("OnDelete"))
		}
		if s.StatefulSet.Spec.UpdateStrategy.Type != appsv1.OnDeleteStatefulSetStrategyType {
			return "WaitingForObservedOnDelete", nil
		}
		op.Stage = "Template"
		if err := c.save(ctx, *state, cm, s); err != nil {
			return "PersistTemplateStage", err
		}
	}
	if op.Stage == "Template" {
		raw, _, _ := unstructuredStrategy(s)
		if raw != "OnDelete" || s.StatefulSet.Spec.UpdateStrategy.Type != appsv1.OnDeleteStatefulSetStrategyType {
			return "StrategyChangedBeforeResourceWrite", fmt.Errorf("expected observed OnDelete")
		}
		if !policy.Equal(s.ClusterResources, op.Resources) {
			return "SynchronizingFullResourceTuple", c.patchCluster(ctx, s, map[string]any{"resources": op.Resources})
		}
		if !policy.Equal(s.TemplateResources, op.Resources) {
			return "WaitingForOperatorTemplate", nil
		}
		op.Stage = "Submit"
		if err := c.save(ctx, *state, cm, s); err != nil {
			return "PersistSubmitStage", err
		}
	}
	if op.Path == "rolling" {
		if op.Stage == "Submit" {
			if reason := rollingSafe(s); reason != "" {
				return reason, fmt.Errorf("rolling availability precondition failed")
			}
			op.Stage = "Rolling"
			if err := c.save(ctx, *state, cm, s); err != nil {
				return "PersistRollingStage", err
			}
			return "StartingExplicitOperatorRollingUpdate", c.patchCluster(ctx, s, strategySpec("RollingUpdate"))
		}
		raw, _, _ := unstructuredStrategy(s)
		if raw != "RollingUpdate" {
			if reason := rollingSafe(s); reason != "" {
				return reason, fmt.Errorf("rolling availability precondition failed before retry")
			}
			return "RetryingExplicitRollingUpdate", c.patchCluster(ctx, s, strategySpec("RollingUpdate"))
		}
		return "ObservingOperatorRollingConvergence", nil
	}
	if s.StatefulSet.Spec.UpdateStrategy.Type != appsv1.OnDeleteStatefulSetStrategyType {
		return "StrategyChangedDuringInPlace", fmt.Errorf("expected observed OnDelete")
	}
	infeasible := false
	for _, b := range s.Brokers {
		progress, exists := op.Brokers[b.Name]
		if !exists {
			return "BrokerMembershipChanged", fmt.Errorf("expected broker ordinal not recorded")
		}
		if progress.UID != b.UID || progress.ContainerID != b.ContainerID || progress.Restarts != b.Restarts {
			op.DisruptionObserved = true
		}
		op.Brokers[b.Name] = observedProgress(b, progress, op.Resources)
		for _, condition := range b.Conditions {
			if condition.Status == corev1.ConditionTrue && condition.Reason == "Infeasible" {
				infeasible = true
			}
		}
	}
	if infeasible {
		if c.Backend.Policy.Update.AllowRollingFallback {
			if reason := rollingSafe(s); reason != "" {
				return reason, fmt.Errorf("explicit fallback unavailable")
			}
			op.Path, op.Stage = "rolling", "Submit"
			return "PersistingExplicitRollingFallback", c.save(ctx, *state, cm, s)
		}
		return "ResizeInfeasibleTargetRetained", nil
	}
	type submission struct {
		broker   Broker
		progress Progress
	}
	var jobs []submission
	var wg sync.WaitGroup
	var mu sync.Mutex
	semaphore := make(chan struct{}, 32)
	for _, broker := range s.Brokers {
		b := broker
		progress, exists := op.Brokers[b.Name]
		if !exists {
			return "BrokerMembershipChanged", fmt.Errorf("expected broker ordinal not recorded")
		}
		if policy.Equal(b.Desired, op.Resources) {
			op.Brokers[b.Name] = progress
			continue
		}
		if !policy.Equal(b.Desired, progress.Source) && !operationTuple(b.Desired, op) {
			return "ExternalPodResourceChange", fmt.Errorf("broker desired tuple changed outside operation")
		}
		if s.At.Before(progress.NextAttempt) {
			op.Brokers[b.Name] = progress
			continue
		}
		jobs = append(jobs, submission{b, progress})
	}
	for _, job := range jobs {
		b, progress := job.broker, job.progress
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			progress.AcceptedAt = time.Time{}
			progress.SubmittedAt = time.Now()
			err := c.resize(requestCtx, b, op.Resources)
			progress.Attempts++
			if err != nil {
				progress.Outcome = "SubmissionFailed"
				if apierrors.IsForbidden(err) {
					progress.Outcome = "PermissionDenied"
				}
				if apierrors.IsConflict(err) {
					progress.Outcome = "Conflict"
				}
				progress.Error = err.Error()
				progress.NextAttempt = s.At.Add(time.Duration(math.Min(60, math.Pow(2, float64(min(progress.Attempts, 6))))) * time.Second)
			} else {
				progress.Outcome = "Submitted"
				progress.AcceptedAt = time.Now()
				progress.Error = ""
				progress.NextAttempt = time.Time{}
			}
			mu.Lock()
			op.Brokers[b.Name] = progress
			mu.Unlock()
		}()
	}
	wg.Wait()
	return "ObservingConcurrentInPlaceConvergence", nil
}
func unstructuredStrategy(s Snapshot) (string, bool, error) {
	value, ok := s.Cluster.Object["spec"].(map[string]any)
	if !ok {
		return "", false, nil
	}
	for _, key := range []string{"override", "statefulSet", "spec", "updateStrategy"} {
		value, ok = value[key].(map[string]any)
		if !ok {
			return "", false, nil
		}
	}
	kind, ok := value["type"].(string)
	return kind, ok, nil
}
func (c *Controller) resize(ctx context.Context, b Broker, target corev1.ResourceRequirements) error {
	index := -1
	for i, v := range b.Pod.Spec.Containers {
		if v.Name == "rabbitmq" {
			index = i
		}
	}
	if index < 0 {
		return fmt.Errorf("rabbitmq container disappeared")
	}
	patch := []map[string]any{{"op": "test", "path": "/metadata/uid", "value": b.UID}, {"op": "test", "path": "/metadata/resourceVersion", "value": b.Pod.ResourceVersion}}
	for _, group := range []struct {
		name   string
		values corev1.ResourceList
	}{{"requests", target.Requests}, {"limits", target.Limits}} {
		for _, key := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			value := group.values[key]
			patch = append(patch, map[string]any{"op": "replace", "path": fmt.Sprintf("/spec/containers/%d/resources/%s/%s", index, group.name, key), "value": value.String()})
		}
	}
	data, _ := json.Marshal(patch)
	_, err := c.Backend.Core.CoreV1().Pods(c.Backend.Policy.Target.Namespace).Patch(ctx, b.Name, types.JSONPatchType, data, metav1.PatchOptions{}, "resize")
	return err
}
func (c *Controller) converged(s Snapshot, op *Operation) bool {
	if !s.Complete || op.ClusterUID != string(s.Cluster.GetUID()) || op.StatefulSetUID != string(s.StatefulSet.UID) || !policy.Equal(s.ClusterResources, op.Resources) || !policy.Equal(s.TemplateResources, op.Resources) {
		return false
	}
	for _, b := range s.Brokers {
		if !b.Ready || !b.CurrentGeneration || activeResize(b) || !policy.Equal(b.Desired, op.Resources) || !policy.Equal(b.Effective, op.Resources) {
			return false
		}
		_, ok := op.Brokers[b.Name]
		if !ok {
			return false
		}
	}
	if op.Path == "rolling" {
		st := s.StatefulSet
		if st.Status.ObservedGeneration < st.Generation || st.Status.CurrentRevision != st.Status.UpdateRevision || st.Status.UpdatedReplicas != int32(len(s.Brokers)) || st.Status.ReadyReplicas != int32(len(s.Brokers)) {
			return false
		}
	}
	// Completion can precede advance when a replacement starts directly at the
	// target template. Bind terminal progress to the verified live census only
	// after every broker (and rolling revision, if used) has passed the checks.
	for _, b := range s.Brokers {
		progress := op.Brokers[b.Name]
		if progress.UID != b.UID || progress.ContainerID != b.ContainerID || progress.Restarts != b.Restarts {
			op.DisruptionObserved = true
		}
		op.Brokers[b.Name] = observedProgress(b, progress, op.Resources)
	}
	return true
}

func observedProgress(b Broker, progress Progress, target corev1.ResourceRequirements) Progress {
	if progress.UID != b.UID || progress.ContainerID != b.ContainerID || progress.Restarts != b.Restarts {
		progress.UID, progress.ContainerID, progress.Restarts = b.UID, b.ContainerID, b.Restarts
		progress.Source = b.Desired
		progress.AcceptedAt, progress.SubmittedAt = time.Time{}, time.Time{}
	}
	if !policy.Equal(b.Desired, target) {
		return progress
	}
	progress.Outcome = "AcceptedAwaitingEffective"
	for _, condition := range b.Conditions {
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		if condition.Type == corev1.PodResizeInProgress {
			progress.Outcome = "InProgress"
		} else if condition.Reason == "Deferred" {
			progress.Outcome = "Deferred"
		} else if condition.Reason == "Infeasible" {
			progress.Outcome = "Infeasible"
		}
	}
	if b.Ready && b.CurrentGeneration && !activeResize(b) && policy.Equal(b.Effective, target) {
		progress.Outcome, progress.Error, progress.NextAttempt = "Effective", "", time.Time{}
	}
	return progress
}
