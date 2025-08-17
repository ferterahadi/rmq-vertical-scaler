import { describe, it, before, beforeEach, mock } from 'node:test';
import assert from 'node:assert';
import MetricsCollector from '../../lib/MetricsCollector.js';

describe('MetricsCollector (lib)', () => {
  let collector;
  let mockConfigManager;
  let mockAxios;

  beforeEach(() => {
    mockConfigManager = {
      rmqHost: 'localhost',
      rmqPort: 15672,
      rmqUser: 'guest',
      rmqPass: 'guest'
    };
    collector = new MetricsCollector(mockConfigManager);
  });

  describe('constructor', () => {
    it('should initialize with configManager', () => {
      assert.strictEqual(collector.configManager, mockConfigManager);
    });

    it('should have correct RabbitMQ connection details', () => {
      assert.strictEqual(collector.configManager.rmqHost, 'localhost');
      assert.strictEqual(collector.configManager.rmqPort, 15672);
      assert.strictEqual(collector.configManager.rmqUser, 'guest');
      assert.strictEqual(collector.configManager.rmqPass, 'guest');
    });
  });

  describe('getQueueMetrics', () => {
    it('should construct correct API URL', async () => {
      // Mock axios to capture the URL
      const originalAxios = (await import('axios')).default;
      let capturedUrl = '';
      
      mock.method(originalAxios, 'get', async (url, options) => {
        capturedUrl = url;
        return { data: { queue_totals: { messages: 100 } } };
      });

      await collector.getQueueMetrics();
      assert.strictEqual(capturedUrl, 'http://localhost:15672/api/overview');
      
      originalAxios.get.mock.restore();
    });

    it('should return empty object on error', async () => {
      // Since we can't easily mock axios, we test with invalid config
      const errorCollector = new MetricsCollector({
        rmqHost: 'invalid-host',
        rmqPort: 99999,
        rmqUser: 'invalid',
        rmqPass: 'invalid'
      });

      const result = await errorCollector.getQueueMetrics();
      assert.deepStrictEqual(result, {});
    });

    it('should handle successful response', async () => {
      const originalAxios = (await import('axios')).default;
      const mockData = {
        queue_totals: { messages: 500 },
        message_stats: {
          publish_details: { rate: 10.5 },
          deliver_get_details: { rate: 8.2 }
        }
      };

      mock.method(originalAxios, 'get', async () => {
        return { data: mockData };
      });

      const result = await collector.getQueueMetrics();
      assert.deepStrictEqual(result, mockData);

      originalAxios.get.mock.restore();
    });

    it('should use correct authentication', async () => {
      const originalAxios = (await import('axios')).default;
      let capturedAuth = null;

      mock.method(originalAxios, 'get', async (url, options) => {
        capturedAuth = options.auth;
        return { data: {} };
      });

      await collector.getQueueMetrics();
      assert.deepStrictEqual(capturedAuth, {
        username: 'guest',
        password: 'guest'
      });

      originalAxios.get.mock.restore();
    });

    it('should use correct timeout', async () => {
      const originalAxios = (await import('axios')).default;
      let capturedTimeout = null;

      mock.method(originalAxios, 'get', async (url, options) => {
        capturedTimeout = options.timeout;
        return { data: {} };
      });

      await collector.getQueueMetrics();
      assert.strictEqual(capturedTimeout, 10000);

      originalAxios.get.mock.restore();
    });
  });

  describe('getDetailedQueues', () => {
    it('should construct correct API URL for queues', async () => {
      const originalAxios = (await import('axios')).default;
      let capturedUrl = '';

      mock.method(originalAxios, 'get', async (url, options) => {
        capturedUrl = url;
        return { data: [] };
      });

      await collector.getDetailedQueues();
      assert.strictEqual(capturedUrl, 'http://localhost:15672/api/queues');

      originalAxios.get.mock.restore();
    });

    it('should return empty array on error', async () => {
      const errorCollector = new MetricsCollector({
        rmqHost: 'invalid-host',
        rmqPort: 99999,
        rmqUser: 'invalid',
        rmqPass: 'invalid'
      });

      const result = await errorCollector.getDetailedQueues();
      assert.deepStrictEqual(result, []);
    });

    it('should handle successful response with queue data', async () => {
      const originalAxios = (await import('axios')).default;
      const mockQueues = [
        { name: 'queue1', messages: 100 },
        { name: 'queue2', messages: 200 },
        { name: 'queue3', messages: 50 }
      ];

      mock.method(originalAxios, 'get', async () => {
        return { data: mockQueues };
      });

      const result = await collector.getDetailedQueues();
      assert.deepStrictEqual(result, mockQueues);
      assert.strictEqual(result.length, 3);

      originalAxios.get.mock.restore();
    });

    it('should use same authentication as getQueueMetrics', async () => {
      const originalAxios = (await import('axios')).default;
      let capturedAuth = null;

      mock.method(originalAxios, 'get', async (url, options) => {
        capturedAuth = options.auth;
        return { data: [] };
      });

      await collector.getDetailedQueues();
      assert.deepStrictEqual(capturedAuth, {
        username: 'guest',
        password: 'guest'
      });

      originalAxios.get.mock.restore();
    });
  });

  describe('waitForRabbitMQ', () => {
    it('should return true when RabbitMQ is ready immediately', async () => {
      const originalAxios = (await import('axios')).default;
      let attemptCount = 0;

      mock.method(originalAxios, 'get', async () => {
        attemptCount++;
        return { data: { status: 'ok' } };
      });

      const result = await collector.waitForRabbitMQ();
      assert.strictEqual(result, true);
      assert.strictEqual(attemptCount, 1);

      originalAxios.get.mock.restore();
    });

    it('should retry on initial failures', async () => {
      const originalAxios = (await import('axios')).default;
      let attemptCount = 0;

      mock.method(originalAxios, 'get', async () => {
        attemptCount++;
        if (attemptCount < 3) {
          throw new Error('Connection refused');
        }
        return { data: { status: 'ok' } };
      });

      // Mock setTimeout to speed up test
      const originalSetTimeout = global.setTimeout;
      global.setTimeout = (fn, ms) => {
        fn();
        return 0;
      };

      const result = await collector.waitForRabbitMQ();
      assert.strictEqual(result, true);
      assert.strictEqual(attemptCount, 3);

      originalAxios.get.mock.restore();
      global.setTimeout = originalSetTimeout;
    });

    it('should use shorter timeout for health checks', async () => {
      const originalAxios = (await import('axios')).default;
      let capturedTimeout = null;

      mock.method(originalAxios, 'get', async (url, options) => {
        capturedTimeout = options.timeout;
        return { data: {} };
      });

      await collector.waitForRabbitMQ();
      assert.strictEqual(capturedTimeout, 5000);

      originalAxios.get.mock.restore();
    });

    it('should log appropriate messages during wait', async () => {
      const originalAxios = (await import('axios')).default;
      const originalConsoleLog = console.log;
      const originalConsoleError = console.error;
      let logMessages = [];
      let errorMessages = [];

      console.log = (...args) => logMessages.push(args.join(' '));
      console.error = (...args) => errorMessages.push(args.join(' '));

      let attemptCount = 0;
      mock.method(originalAxios, 'get', async () => {
        attemptCount++;
        if (attemptCount < 2) {
          throw new Error('Connection refused');
        }
        return { data: {} };
      });

      // Mock setTimeout
      const originalSetTimeout = global.setTimeout;
      global.setTimeout = (fn, ms) => {
        fn();
        return 0;
      };

      await collector.waitForRabbitMQ();

      assert.ok(logMessages.some(msg => msg.includes('Waiting for RabbitMQ to be ready')));
      assert.ok(logMessages.some(msg => msg.includes('Waiting for RabbitMQ... (attempt 1)')));
      assert.ok(logMessages.some(msg => msg.includes('RabbitMQ is ready')));

      originalAxios.get.mock.restore();
      console.log = originalConsoleLog;
      console.error = originalConsoleError;
      global.setTimeout = originalSetTimeout;
    });
  });

  describe('error handling', () => {
    it('should log error messages appropriately', async () => {
      const originalConsoleError = console.error;
      let errorMessages = [];
      console.error = (...args) => errorMessages.push(args.join(' '));

      const errorCollector = new MetricsCollector({
        rmqHost: 'invalid',
        rmqPort: 0,
        rmqUser: 'invalid',
        rmqPass: 'invalid'
      });

      await errorCollector.getQueueMetrics();
      assert.ok(errorMessages.some(msg => msg.includes('[ERROR] Failed to connect to RabbitMQ API')));

      errorMessages = [];
      await errorCollector.getDetailedQueues();
      assert.ok(errorMessages.some(msg => msg.includes('[ERROR] Failed to fetch queue details')));

      console.error = originalConsoleError;
    });
  });

  describe('integration scenarios', () => {
    it('should work with different configManager configurations', () => {
      const customConfig = {
        rmqHost: 'rabbit.example.com',
        rmqPort: 15672,
        rmqUser: 'admin',
        rmqPass: 'secret123'
      };

      const customCollector = new MetricsCollector(customConfig);
      assert.strictEqual(customCollector.configManager.rmqHost, 'rabbit.example.com');
      assert.strictEqual(customCollector.configManager.rmqUser, 'admin');
    });

    it('should handle missing configManager properties gracefully', () => {
      const minimalConfig = {};
      const minimalCollector = new MetricsCollector(minimalConfig);
      
      // Should not throw errors
      assert.ok(minimalCollector);
      assert.strictEqual(minimalCollector.configManager, minimalConfig);
    });
  });
});