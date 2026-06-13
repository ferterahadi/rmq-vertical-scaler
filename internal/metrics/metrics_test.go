package metrics

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func quietLogger() *log.Logger { return log.New(io.Discard, "", 0) }

func TestGetQueueMetricsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/overview" {
			t.Errorf("path = %q, want /api/overview", r.URL.Path)
		}
		u, p, ok := r.BasicAuth()
		if !ok || u != "guest" || p != "guest" {
			t.Errorf("auth = %q/%q ok=%v, want guest/guest", u, p, ok)
		}
		io.WriteString(w, `{"queue_totals":{"messages":500},
			"message_stats":{"publish_details":{"rate":10.5},"deliver_get_details":{"rate":8.2}}}`)
	}))
	defer srv.Close()

	c := newWithBaseURL(srv.URL, "guest", "guest", quietLogger())
	ov, ok := c.GetQueueMetrics(context.Background())
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if ov.QueueTotals.Messages != 500 {
		t.Errorf("messages = %d, want 500", ov.QueueTotals.Messages)
	}
	if ov.MessageStats.PublishDetails.Rate != 10.5 {
		t.Errorf("publish rate = %v, want 10.5", ov.MessageStats.PublishDetails.Rate)
	}
	if ov.MessageStats.DeliverGetDetails.Rate != 8.2 {
		t.Errorf("deliver rate = %v, want 8.2", ov.MessageStats.DeliverGetDetails.Rate)
	}
}

func TestGetQueueMetricsErrorReturnsNotOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newWithBaseURL(srv.URL, "guest", "guest", quietLogger())
	ov, ok := c.GetQueueMetrics(context.Background())
	if ok {
		t.Error("ok = true, want false on 5xx")
	}
	if ov != (Overview{}) {
		t.Errorf("overview = %+v, want zero value", ov)
	}
}

func TestGetDetailedQueuesSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/queues" {
			t.Errorf("path = %q, want /api/queues", r.URL.Path)
		}
		io.WriteString(w, `[{"name":"q1","messages":100},{"name":"q2","messages":200},{"name":"q3","messages":50}]`)
	}))
	defer srv.Close()

	c := newWithBaseURL(srv.URL, "guest", "guest", quietLogger())
	queues := c.GetDetailedQueues(context.Background())
	if len(queues) != 3 {
		t.Fatalf("len = %d, want 3", len(queues))
	}
	if queues[1].Messages != 200 || queues[1].Name != "q2" {
		t.Errorf("queues[1] = %+v", queues[1])
	}
}

func TestGetDetailedQueuesErrorReturnsEmpty(t *testing.T) {
	c := newWithBaseURL("http://127.0.0.1:1", "guest", "guest", quietLogger()) // unreachable
	queues := c.GetDetailedQueues(context.Background())
	if len(queues) != 0 {
		t.Errorf("len = %d, want 0 on error", len(queues))
	}
}

func TestWaitForRabbitMQReadyImmediately(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := newWithBaseURL(srv.URL, "guest", "guest", quietLogger())
	if err := c.WaitForRabbitMQ(context.Background()); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

func TestWaitForRabbitMQRetriesThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := newWithBaseURL(srv.URL, "guest", "guest", quietLogger())
	c.retryDelay = time.Millisecond // speed up the test
	if err := c.WaitForRabbitMQ(context.Background()); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestNewBuildsBaseURL(t *testing.T) {
	c := New("rabbit.example.com", "15672", "u", "p", nil) // nil logger -> log.Default()
	if c.baseURL != "http://rabbit.example.com:15672" {
		t.Errorf("baseURL = %q, want http://rabbit.example.com:15672", c.baseURL)
	}
}

func TestGetRequestBuildErrorReturnsNotOK(t *testing.T) {
	// Control characters make http.NewRequestWithContext fail to parse the URL.
	c := newWithBaseURL("http://\x7f-bad-host", "u", "p", quietLogger())
	if _, ok := c.GetQueueMetrics(context.Background()); ok {
		t.Error("ok = true, want false on request-build error")
	}
}

func TestWaitForRabbitMQLogsAfterTenAttempts(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 11 { // fail the first 11 attempts
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := newWithBaseURL(srv.URL, "u", "p", quietLogger())
	c.retryDelay = time.Millisecond
	if err := c.WaitForRabbitMQ(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 12 {
		t.Errorf("calls = %d, want 12 (exercises the >10 attempts branch)", got)
	}
}

func TestWaitForRabbitMQRespectsContextCancel(t *testing.T) {
	c := newWithBaseURL("http://127.0.0.1:1", "guest", "guest", quietLogger()) // never ready
	c.retryDelay = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- c.WaitForRabbitMQ(ctx) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("err = nil, want context error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForRabbitMQ did not return after context cancel")
	}
}
