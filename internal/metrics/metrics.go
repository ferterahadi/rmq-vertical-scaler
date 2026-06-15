// Package metrics talks to the RabbitMQ management HTTP API. It is a port of
// the v1 MetricsCollector (lib/MetricsCollector.js) using only the standard
// library's net/http in place of axios.
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// Overview is the subset of GET /api/overview the scaler consumes.
type Overview struct {
	QueueTotals struct {
		Messages int `json:"messages"`
	} `json:"queue_totals"`
	MessageStats struct {
		PublishDetails struct {
			Rate float64 `json:"rate"`
		} `json:"publish_details"`
		DeliverGetDetails struct {
			Rate float64 `json:"rate"`
		} `json:"deliver_get_details"`
	} `json:"message_stats"`
}

// Queue is the subset of a GET /api/queues entry the scaler consumes.
type Queue struct {
	Name     string `json:"name"`
	Messages int    `json:"messages"`
}

// Timeouts match v1: 10s for metric fetches, 5s for the readiness probe.
const (
	metricTimeout = 10 * time.Second
	readyTimeout  = 5 * time.Second
)

// Collector fetches metrics from one RabbitMQ management API endpoint.
type Collector struct {
	baseURL string
	user    string
	pass    string
	client  *http.Client
	logger  *log.Logger

	// retryDelay is the backoff between readiness attempts (5s in v1);
	// overridable in tests.
	retryDelay time.Duration
}

// New builds a Collector for http://host:port with basic-auth credentials.
func New(host, port, user, pass string, logger *log.Logger) *Collector {
	return newWithBaseURL(fmt.Sprintf("http://%s:%s", host, port), user, pass, logger)
}

func newWithBaseURL(baseURL, user, pass string, logger *log.Logger) *Collector {
	if logger == nil {
		logger = log.Default()
	}
	return &Collector{
		baseURL:    baseURL,
		user:       user,
		pass:       pass,
		client:     &http.Client{},
		logger:     logger,
		retryDelay: 5 * time.Second,
	}
}

// get performs an authenticated GET against path with the given timeout and
// decodes the JSON body into out.
func (c *Collector) get(ctx context.Context, path string, timeout time.Duration, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.user, c.pass)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// GetQueueMetrics fetches /api/overview. The bool is false on any error,
// mirroring v1's "return {}" (which the scaling engine treats as a skip).
func (c *Collector) GetQueueMetrics(ctx context.Context) (Overview, bool) {
	c.logger.Println("✅ Fetching overview metrics from RabbitMQ API...")
	var ov Overview
	if err := c.get(ctx, "/api/overview", metricTimeout, &ov); err != nil {
		c.logger.Printf("[ERROR] Failed to connect to RabbitMQ API: %v", err)
		return Overview{}, false
	}
	return ov, true
}

// GetDetailedQueues fetches /api/queues. It returns an empty slice on error,
// mirroring v1's "return []".
func (c *Collector) GetDetailedQueues(ctx context.Context) []Queue {
	c.logger.Println("✅ Fetching queue details from RabbitMQ API...")
	var queues []Queue
	if err := c.get(ctx, "/api/queues", metricTimeout, &queues); err != nil {
		c.logger.Printf("[ERROR] Failed to fetch queue details: %v", err)
		return []Queue{}
	}
	c.logger.Printf("✅ Retrieved details for %d queues", len(queues))
	return queues
}

// WaitForRabbitMQ blocks until /api/overview is reachable, retrying every
// retryDelay. It returns early with ctx.Err() if the context is cancelled.
func (c *Collector) WaitForRabbitMQ(ctx context.Context) error {
	c.logger.Println("⏳ Waiting for RabbitMQ to be ready...")
	attempts := 0
	for {
		attempts++
		var discard any
		if err := c.get(ctx, "/api/overview", readyTimeout, &discard); err == nil {
			c.logger.Println("✅ RabbitMQ is ready")
			return nil
		} else if attempts > 10 {
			c.logger.Printf("❌ Failed to connect to RabbitMQ after %d attempts: %v", attempts, err)
		} else {
			c.logger.Printf("⏳ Waiting for RabbitMQ... (attempt %d)", attempts)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.retryDelay):
		}
	}
}
