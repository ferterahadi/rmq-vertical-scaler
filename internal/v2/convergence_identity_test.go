package v2

import (
	"context"
	"testing"
	"time"
)

func TestConvergenceRecordsVerifiedReplacementIdentity(t *testing.T) {
	at := time.Unix(10000, 0)
	b, _, _ := fakeBackend(t, at)
	c := NewController(b, "status", quietLog(), at)
	s, err := b.Read(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	op, reason := c.newOperation(context.Background(), s, Decision{Target: 1}, nil)
	if op == nil {
		t.Fatal(reason)
	}
	s.ClusterResources, s.TemplateResources = op.Resources, op.Resources
	for i := range s.Brokers {
		s.Brokers[i].Desired, s.Brokers[i].Effective = op.Resources, op.Resources
	}
	broker := &s.Brokers[1]
	prior := op.Brokers[broker.Name]
	prior.AcceptedAt, prior.SubmittedAt = at, at
	prior.Error = "old broker denial"
	prior.NextAttempt = at.Add(time.Minute)
	op.Brokers[broker.Name] = prior
	broker.UID, broker.ContainerID, broker.Restarts = "replacement-uid", "replacement-container", 0
	if !c.converged(s, op) {
		t.Fatal("verified effective replacement should converge")
	}
	got := op.Brokers[broker.Name]
	if got.UID != broker.UID || got.ContainerID != broker.ContainerID || got.Restarts != broker.Restarts ||
		!got.AcceptedAt.IsZero() || !got.SubmittedAt.IsZero() || !got.NextAttempt.IsZero() || got.Error != "" ||
		got.Outcome != "Effective" || !op.DisruptionObserved {
		t.Fatalf("terminal state attributes replacement to stale identity/submission: %+v", got)
	}
}
