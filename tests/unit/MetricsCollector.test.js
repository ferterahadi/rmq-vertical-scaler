import { describe, it, before, after, mock } from 'node:test';
import assert from 'node:assert';
import MetricsCollector from '../../lib/MetricsCollector.js';

describe('MetricsCollector', () => {
  let collector;

  before(() => {
    collector = new MetricsCollector({
      rmqHost: 'localhost',
      rmqPort: 15672,
      rmqUser: 'guest',
      rmqPass: 'guest'
    });
  });

  describe('extractMetrics', () => {
    it('should extract basic metrics correctly', () => {
      const mockData = {
        queue_totals: {
          messages: 1500
        },
        message_stats: {
          publish_details: { rate: 25.5 },
          deliver_details: { rate: 20.0 },
          ack_details: { rate: 18.5 }
        }
      };

      const metrics = collector.extractMetrics(mockData);

      assert.strictEqual(metrics.totalMessages, 1500);
      assert.strictEqual(metrics.publishRate, 25.5);
      assert.strictEqual(metrics.deliverRate, 20.0);
      assert.strictEqual(metrics.ackRate, 18.5);
    });

    it('should handle missing data gracefully', () => {
      const mockData = {};
      
      const metrics = collector.extractMetrics(mockData);

      assert.strictEqual(metrics.totalMessages, 0);
      assert.strictEqual(metrics.publishRate, 0);
      assert.strictEqual(metrics.deliverRate, 0);
      assert.strictEqual(metrics.ackRate, 0);
    });

    it('should handle partial data', () => {
      const mockData = {
        queue_totals: {
          messages: 500
        }
        // message_stats missing
      };

      const metrics = collector.extractMetrics(mockData);

      assert.strictEqual(metrics.totalMessages, 500);
      assert.strictEqual(metrics.publishRate, 0);
      assert.strictEqual(metrics.deliverRate, 0);
      assert.strictEqual(metrics.ackRate, 0);
    });
  });

  describe('constructor', () => {
    it('should set configuration correctly', () => {
      const config = {
        rmqHost: 'test-host',
        rmqPort: 15672,
        rmqUser: 'test-user',
        rmqPass: 'test-pass',
        timeout: 5000
      };

      const testCollector = new MetricsCollector(config);

      assert.strictEqual(testCollector.rmqHost, 'test-host');
      assert.strictEqual(testCollector.rmqPort, 15672);
      assert.strictEqual(testCollector.rmqUser, 'test-user');
      assert.strictEqual(testCollector.rmqPass, 'test-pass');
      assert.strictEqual(testCollector.timeout, 5000);
    });

    it('should use default timeout when not provided', () => {
      const config = {
        rmqHost: 'test-host',
        rmqPort: 15672,
        rmqUser: 'test-user',
        rmqPass: 'test-pass'
      };

      const testCollector = new MetricsCollector(config);

      assert.strictEqual(testCollector.timeout, 10000);
    });
  });
});