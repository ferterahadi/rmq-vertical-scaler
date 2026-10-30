package v2

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/policy"
)

func queueBackend(t *testing.T, handler http.HandlerFunc) *Backend {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	host, port, _ := net.SplitHostPort(u.Host)
	p := testPolicy(t)
	p.RMQ = &policy.Connection{Host: host, Port: port}
	return &Backend{Policy: p, HTTP: server.Client(), User: "monitor", Password: "not-logged"}
}
func queueTrigger(mode string, selector *policy.Selector) policy.Trigger {
	return policy.Trigger{Type: "rabbitmq", Metadata: policy.Metadata{Mode: mode, Aggregation: map[string]string{"QueueLength": "sum"}[mode], Thresholds: map[string]float64{"HIGH": 100}, Selector: selector}}
}
func TestQueuePaginationSumsTotalMessagesAcrossVhosts(t *testing.T) {
	b := queueBackend(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		vhost := "/"
		if page == "2" {
			vhost = "b"
		}
		fmt.Fprintf(w, `{"page":%s,"page_count":2,"total_count":2,"filtered_count":2,"items":[{"vhost":%q,"name":"same","messages":120,"messages_ready":100,"messages_unacknowledged":20}]}`, page, vhost)
	})
	b.Policy.Triggers = []policy.Trigger{queueTrigger("QueueLength", nil)}
	signal := b.queueSignals(context.Background(), time.Now())[0]
	if !signal.Complete || !signal.Available || signal.Value != 240 || signal.Profile != 1 {
		t.Fatalf("wrong total/scope: %+v", signal)
	}
	b.Policy.Triggers[0].Metadata.Aggregation = "max"
	signal = b.queueSignals(context.Background(), time.Now())[0]
	if signal.Value != 120 {
		t.Fatalf("max ignored: %+v", signal)
	}
}
func TestFailedPageAndDuplicateQueueAreOnlyLowerBounds(t *testing.T) {
	for _, failure := range []string{"error", "duplicate"} {
		t.Run(failure, func(t *testing.T) {
			b := queueBackend(t, func(w http.ResponseWriter, r *http.Request) {
				page := r.URL.Query().Get("page")
				if page == "2" && failure == "error" {
					w.WriteHeader(503)
					return
				}
				fmt.Fprintf(w, `{"page":%s,"page_count":2,"total_count":2,"filtered_count":2,"items":[{"vhost":"/","name":"same","messages":120}]}`, page)
			})
			b.Policy.Triggers = []policy.Trigger{queueTrigger("QueueLength", nil)}
			s := b.queueSignals(context.Background(), time.Now())[0]
			if s.Complete || !s.Available || s.Value != 120 || s.Profile != 1 {
				t.Fatalf("partial sum discarded or treated as complete: %+v", s)
			}
		})
	}
}
func TestRateSourceStaysExplicitAndRestrictedOverviewIsUnavailable(t *testing.T) {
	for _, admin := range []bool{true, false} {
		t.Run(fmt.Sprint(admin), func(t *testing.T) {
			b := queueBackend(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/queues":
					fmt.Fprint(w, `[{"name":"a","vhost":"/","messages":0,"message_stats":{"publish":50,"publish_details":{"rate":120}}},{"name":"b","vhost":"/","messages":0,"message_stats":{"publish":50,"publish_details":{"rate":120}}}]`)
				case "/api/whoami":
					tag := "management"
					if admin {
						tag = "administrator"
					}
					fmt.Fprintf(w, `{"name":"monitor","tags":[%q]}`, tag)
				case "/api/vhosts":
					fmt.Fprint(w, `[{"name":"/"}]`)
				case "/api/permissions":
					fmt.Fprint(w, `[{"user":"monitor","vhost":"/","read":".*"}]`)
				case "/api/overview":
					fmt.Fprint(w, `{"message_stats":{"publish":100,"publish_details":{"rate":120}}}`)
				default:
					w.WriteHeader(404)
				}
			})
			b.Policy.Triggers = []policy.Trigger{queueTrigger("MessageRate", nil), queueTrigger("MessageRate", &policy.Selector{QueueNameRegex: ".*"})}
			signals := b.queueSignals(context.Background(), time.Now())
			if signals[1].Value != 240 || !signals[1].Complete || signals[1].Source == signals[0].Source {
				t.Fatalf("fanout/scope lost: %+v", signals)
			}
			if admin {
				if signals[0].Value != 120 || !signals[0].Complete {
					t.Fatal(signals[0])
				}
			} else if signals[0].Available || signals[0].Complete {
				t.Fatal("restricted overview silently changed counting scope")
			}
		})
	}
}
func TestEmptyQueuesAreZeroButNonemptyMissingStatsAreIncomplete(t *testing.T) {
	for _, body := range []string{`[]`, `[{"name":"a","vhost":"/","messages":0}]`, `[{"name":"a","vhost":"/","messages":10}]`} {
		t.Run(body, func(t *testing.T) {
			b := queueBackend(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
			b.Policy.Triggers = []policy.Trigger{queueTrigger("MessageRate", &policy.Selector{QueueNameRegex: ".*"})}
			s := b.queueSignals(context.Background(), time.Now())[0]
			if body != `[]` {
				if s.Complete || s.Available {
					t.Fatal("missing statistics became a valid zero")
				}
			} else if !s.Complete || s.Value != 0 {
				t.Fatal("valid empty selection did not become zero")
			}
		})
	}
}
