package v3

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ferterahadi/rmq-vertical-scaler/v3/internal/policy"
)

func TestQueueDepthUsesExplicitTotalsBeforeDetailedStatisticsExist(t *testing.T) {
	b := queueBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("disable_stats") == "true" && r.URL.Query().Get("enable_queue_totals") == "true" {
			fmt.Fprint(w, `[{"name":"new-work","vhost":"/","messages":120},{"name":"new-empty","vhost":"/","messages":0}]`)
			return
		}
		// Live RabbitMQ can list newly declared queues before detailed counters exist.
		fmt.Fprint(w, `[{"name":"new-work","vhost":"/"},{"name":"new-empty","vhost":"/"}]`)
	})
	b.Policy.Triggers = []policy.Trigger{queueTrigger("QueueLength", nil)}
	signal := b.queueSignals(context.Background(), time.Now())[0]
	if !signal.Complete || !signal.Available || signal.Value != 120 || signal.Profile != 1 {
		t.Fatalf("explicit nonzero totals lost: %+v", signal)
	}
}

func TestSelectedQueueRateRetainsDetailedStatistics(t *testing.T) {
	b := queueBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("disable_stats") == "true" {
			fmt.Fprint(w, `[{"name":"work","vhost":"/","messages":150}]`)
			return
		}
		fmt.Fprint(w, `[{"name":"work","vhost":"/","messages":150,"message_stats":{"publish_details":{"rate":120}}}]`)
	})
	b.Policy.Triggers = []policy.Trigger{queueTrigger("QueueLength", nil), queueTrigger("MessageRate", &policy.Selector{QueueNameRegex: "^work$"})}
	signals := b.queueSignals(context.Background(), time.Now())
	if len(signals) != 2 || !signals[0].Complete || signals[0].Value != 150 || !signals[1].Complete || signals[1].Value != 120 {
		t.Fatalf("depth/rate counting changed: %+v", signals)
	}
}

func TestMissingExplicitQueueTotalsRemainIncomplete(t *testing.T) {
	b := queueBackend(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"name":"known","vhost":"/","messages":120},{"name":"unknown","vhost":"/"}]`)
	})
	b.Policy.Triggers = []policy.Trigger{queueTrigger("QueueLength", nil)}
	signal := b.queueSignals(context.Background(), time.Now())[0]
	if signal.Complete || !signal.Available || signal.Value != 120 || signal.Profile != 1 {
		t.Fatalf("missing totals became safe decrease evidence: %+v", signal)
	}
}
