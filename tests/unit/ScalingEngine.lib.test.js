import { describe, it, beforeEach, mock } from 'node:test';
import assert from 'node:assert';
import ScalingEngine from '../../lib/ScalingEngine.js';

describe('ScalingEngine (lib)', () => {
  let engine;
  let mockConfigManager;
  let mockKubernetesClient;
  let mockMetricsCollector;

  beforeEach(() => {
    mockConfigManager = {
      config: {
        profileNames: ['LOW', 'MEDIUM', 'HIGH', 'CRITICAL'],
        profiles: {
          LOW: { cpu: '330m', memory: '2Gi' },
          MEDIUM: { cpu: '800m', memory: '3Gi' },
          HIGH: { cpu: '1600m', memory: '4Gi' },
          CRITICAL: { cpu: '2400m', memory: '8Gi' }
        },
        thresholds: {
          queue: {
            MEDIUM: 2000,
            HIGH: 10000,
            CRITICAL: 50000
          },
          rate: {
            MEDIUM: 200,
            HIGH: 1000,
            CRITICAL: 2000
          }
        }
      },
      thresholds: {
        queue: {
          MEDIUM: 2000,
          HIGH: 10000,
          CRITICAL: 50000
        },
        rate: {
          MEDIUM: 200,
          HIGH: 1000,
          CRITICAL: 2000
        }
      },
      scaleUpDebounceSeconds: 30,
      scaleDownDebounceSeconds: 120
    };

    mockKubernetesClient = {
      getStabilityState: mock.fn(async () => ({
        stableProfile: '',
        stableSince: 0
      })),
      updateStabilityTracking: mock.fn(async () => {})
    };

    mockMetricsCollector = {
      getQueueMetrics: mock.fn(async () => ({
        queue_totals: { messages: 100 },
        message_stats: {
          publish_details: { rate: 10 },
          deliver_get_details: { rate: 8 }
        }
      })),
      getDetailedQueues: mock.fn(async () => [
        { name: 'queue1', messages: 50 },
        { name: 'queue2', messages: 30 }
      ])
    };

    engine = new ScalingEngine(mockConfigManager, mockKubernetesClient, mockMetricsCollector);
  });

  describe('constructor', () => {
    it('should initialize with all dependencies', () => {
      assert.strictEqual(engine.configManager, mockConfigManager);
      assert.strictEqual(engine.kubernetesClient, mockKubernetesClient);
      assert.strictEqual(engine.metricsCollector, mockMetricsCollector);
    });
  });

  describe('calculateScaleProfile', () => {
    it('should calculate LOW profile for minimal load', async () => {
      mockMetricsCollector.getQueueMetrics.mock.mockImplementation(async () => ({
        queue_totals: { messages: 100 },
        message_stats: {
          publish_details: { rate: 50 },
          deliver_get_details: { rate: 45 }
        }
      }));
      mockMetricsCollector.getDetailedQueues.mock.mockImplementation(async () => [
        { messages: 50 },
        { messages: 50 }
      ]);

      const result = await engine.calculateScaleProfile();
      assert.strictEqual(result.profile, 'LOW');
      assert.strictEqual(result.metrics.totalMessages, 100);
      assert.strictEqual(result.metrics.messageRate, 50);
      assert.strictEqual(result.metrics.maxQueueDepth, 50);
    });

    it('should calculate MEDIUM profile based on queue threshold', async () => {
      mockMetricsCollector.getQueueMetrics.mock.mockImplementation(async () => ({
        queue_totals: { messages: 3000 },
        message_stats: {
          publish_details: { rate: 100 },
          deliver_get_details: { rate: 80 }
        }
      }));
      mockMetricsCollector.getDetailedQueues.mock.mockImplementation(async () => [
        { messages: 2500 },
        { messages: 500 }
      ]);

      const result = await engine.calculateScaleProfile();
      assert.strictEqual(result.profile, 'MEDIUM');
      assert.strictEqual(result.metrics.maxQueueDepth, 2500);
    });

    it('should calculate MEDIUM profile based on rate threshold', async () => {
      mockMetricsCollector.getQueueMetrics.mock.mockImplementation(async () => ({
        queue_totals: { messages: 500 },
        message_stats: {
          publish_details: { rate: 250 },
          deliver_get_details: { rate: 200 }
        }
      }));
      mockMetricsCollector.getDetailedQueues.mock.mockImplementation(async () => [
        { messages: 300 },
        { messages: 200 }
      ]);

      const result = await engine.calculateScaleProfile();
      assert.strictEqual(result.profile, 'MEDIUM');
      assert.strictEqual(result.metrics.messageRate, 250);
    });

    it('should calculate HIGH profile for high load', async () => {
      mockMetricsCollector.getQueueMetrics.mock.mockImplementation(async () => ({
        queue_totals: { messages: 15000 },
        message_stats: {
          publish_details: { rate: 500 },
          deliver_get_details: { rate: 300 }
        }
      }));
      mockMetricsCollector.getDetailedQueues.mock.mockImplementation(async () => [
        { messages: 12000 },
        { messages: 3000 }
      ]);

      const result = await engine.calculateScaleProfile();
      assert.strictEqual(result.profile, 'HIGH');
    });

    it('should calculate CRITICAL profile for critical load', async () => {
      mockMetricsCollector.getQueueMetrics.mock.mockImplementation(async () => ({
        queue_totals: { messages: 60000 },
        message_stats: {
          publish_details: { rate: 2500 },
          deliver_get_details: { rate: 1000 }
        }
      }));
      mockMetricsCollector.getDetailedQueues.mock.mockImplementation(async () => [
        { messages: 55000 },
        { messages: 5000 }
      ]);

      const result = await engine.calculateScaleProfile();
      assert.strictEqual(result.profile, 'CRITICAL');
    });

    it('should handle missing metrics gracefully', async () => {
      mockMetricsCollector.getQueueMetrics.mock.mockImplementation(async () => ({}));
      mockMetricsCollector.getDetailedQueues.mock.mockImplementation(async () => []);

      await assert.rejects(
        async () => await engine.calculateScaleProfile(),
        (err) => {
          assert.strictEqual(err.message, 'Connection error: Unable to fetch metrics');
          return true;
        }
      );
    });

    it('should handle partial metrics data', async () => {
      mockMetricsCollector.getQueueMetrics.mock.mockImplementation(async () => ({
        queue_totals: { messages: 500 }
        // Missing message_stats
      }));
      mockMetricsCollector.getDetailedQueues.mock.mockImplementation(async () => [
        { messages: 300 },
        { messages: 200 }
      ]);

      const result = await engine.calculateScaleProfile();
      assert.strictEqual(result.metrics.messageRate, 0);
      assert.strictEqual(result.metrics.consumeRate, 0);
      assert.strictEqual(result.metrics.backlogRate, 0);
    });

    it('should calculate backlog rate correctly', async () => {
      mockMetricsCollector.getQueueMetrics.mock.mockImplementation(async () => ({
        queue_totals: { messages: 1000 },
        message_stats: {
          publish_details: { rate: 100 },
          deliver_get_details: { rate: 60 }
        }
      }));
      mockMetricsCollector.getDetailedQueues.mock.mockImplementation(async () => [
        { messages: 1000 }  // Need at least one queue to avoid error
      ]);

      const result = await engine.calculateScaleProfile();
      assert.strictEqual(result.metrics.backlogRate, 40);
    });

    it('should select highest applicable profile', async () => {
      mockMetricsCollector.getQueueMetrics.mock.mockImplementation(async () => ({
        queue_totals: { messages: 60000 },
        message_stats: {
          publish_details: { rate: 1500 }, // Would trigger HIGH
          deliver_get_details: { rate: 500 }
        }
      }));
      mockMetricsCollector.getDetailedQueues.mock.mockImplementation(async () => [
        { messages: 55000 } // Would trigger CRITICAL
      ]);

      const result = await engine.calculateScaleProfile();
      assert.strictEqual(result.profile, 'CRITICAL');
    });
  });

  describe('getProfilePriority', () => {
    it('should return correct priority for each profile', () => {
      assert.strictEqual(engine.getProfilePriority('LOW'), 1);
      assert.strictEqual(engine.getProfilePriority('MEDIUM'), 2);
      assert.strictEqual(engine.getProfilePriority('HIGH'), 3);
      assert.strictEqual(engine.getProfilePriority('CRITICAL'), 4);
    });

    it('should return 0 for unknown profile', () => {
      assert.strictEqual(engine.getProfilePriority('UNKNOWN'), 0);
      assert.strictEqual(engine.getProfilePriority('INVALID'), 0);
      assert.strictEqual(engine.getProfilePriority(''), 0);
    });
  });

  describe('checkProfileStability', () => {
    it('should return false when profile recommendation changes', async () => {
      mockKubernetesClient.getStabilityState.mock.mockImplementation(async () => ({
        stableProfile: 'MEDIUM',
        stableSince: Date.now() / 1000 - 60
      }));

      const result = await engine.checkProfileStability('LOW', 'HIGH');
      
      assert.strictEqual(result, false);
      assert.strictEqual(mockKubernetesClient.updateStabilityTracking.mock.calls.length, 1);
      assert.strictEqual(mockKubernetesClient.updateStabilityTracking.mock.calls[0].arguments[0], 'HIGH');
    });

    it('should return true when already at recommended profile', async () => {
      mockKubernetesClient.getStabilityState.mock.mockImplementation(async () => ({
        stableProfile: 'MEDIUM',
        stableSince: Date.now() / 1000 - 60
      }));

      const result = await engine.checkProfileStability('MEDIUM', 'MEDIUM');
      
      assert.strictEqual(result, true);
      assert.strictEqual(mockKubernetesClient.updateStabilityTracking.mock.calls.length, 1);
    });

    it('should enforce scale-up debounce period', async () => {
      const now = Date.now() / 1000;
      mockKubernetesClient.getStabilityState.mock.mockImplementation(async () => ({
        stableProfile: 'MEDIUM',
        stableSince: now - 15 // Only 15 seconds stable
      }));

      const result = await engine.checkProfileStability('LOW', 'MEDIUM');
      
      assert.strictEqual(result, false);
    });

    it('should allow scale-up after debounce period', async () => {
      const now = Date.now() / 1000;
      mockKubernetesClient.getStabilityState.mock.mockImplementation(async () => ({
        stableProfile: 'MEDIUM',
        stableSince: now - 35 // 35 seconds stable (> 30)
      }));

      const result = await engine.checkProfileStability('LOW', 'MEDIUM');
      
      assert.strictEqual(result, true);
    });

    it('should enforce scale-down debounce period', async () => {
      const now = Date.now() / 1000;
      mockKubernetesClient.getStabilityState.mock.mockImplementation(async () => ({
        stableProfile: 'LOW',
        stableSince: now - 60 // Only 60 seconds stable
      }));

      const result = await engine.checkProfileStability('MEDIUM', 'LOW');
      
      assert.strictEqual(result, false);
    });

    it('should allow scale-down after debounce period', async () => {
      const now = Date.now() / 1000;
      mockKubernetesClient.getStabilityState.mock.mockImplementation(async () => ({
        stableProfile: 'LOW',
        stableSince: now - 125 // 125 seconds stable (> 120)
      }));

      const result = await engine.checkProfileStability('MEDIUM', 'LOW');
      
      assert.strictEqual(result, true);
    });

    it('should log appropriate messages', async () => {
      const originalConsoleLog = console.log;
      let logMessages = [];
      console.log = (...args) => logMessages.push(args.join(' '));

      mockKubernetesClient.getStabilityState.mock.mockImplementation(async () => ({
        stableProfile: 'MEDIUM',
        stableSince: Date.now() / 1000 - 35
      }));

      await engine.checkProfileStability('LOW', 'MEDIUM');

      assert.ok(logMessages.some(msg => msg.includes('Checking profile stability')));
      assert.ok(logMessages.some(msg => msg.includes('Profile has been stable for required duration')));

      console.log = originalConsoleLog;
    });
  });

  describe('generateScalingMessage', () => {
    it('should generate correct message for LOW profile', () => {
      const message = engine.generateScalingMessage('LOW');
      assert.ok(message.includes('LOW'));
      assert.ok(message.includes('minimal resources'));
    });

    it('should generate correct message for MEDIUM profile', () => {
      const message = engine.generateScalingMessage('MEDIUM');
      assert.ok(message.includes('MEDIUM'));
      assert.ok(message.includes('moderate scaling'));
    });

    it('should generate correct message for HIGH profile', () => {
      const message = engine.generateScalingMessage('HIGH');
      assert.ok(message.includes('HIGH'));
      assert.ok(message.includes('moderate scaling')); // HIGH is at index 2, which is <= profileCount/2 (4/2=2)
    });

    it('should generate correct message for CRITICAL profile', () => {
      const message = engine.generateScalingMessage('CRITICAL');
      assert.ok(message.includes('CRITICAL'));
      assert.ok(message.includes('maximum resources'));
    });

    it('should handle unknown profiles', () => {
      const message = engine.generateScalingMessage('UNKNOWN');
      assert.ok(typeof message === 'string');
    });
  });

  describe('error handling', () => {
    it('should propagate errors from metrics collector', async () => {
      mockMetricsCollector.getQueueMetrics.mock.mockImplementation(async () => {
        throw new Error('API connection failed');
      });

      await assert.rejects(
        async () => await engine.calculateScaleProfile(),
        (err) => {
          assert.ok(err.message.includes('API connection failed'));
          return true;
        }
      );
    });

    it('should handle kubernetes client errors gracefully', async () => {
      mockKubernetesClient.getStabilityState.mock.mockImplementation(async () => {
        throw new Error('ConfigMap not found');
      });

      await assert.rejects(
        async () => await engine.checkProfileStability('LOW', 'MEDIUM'),
        (err) => {
          assert.ok(err.message.includes('ConfigMap not found'));
          return true;
        }
      );
    });
  });

  describe('integration scenarios', () => {
    it('should handle complete scaling workflow', async () => {
      // Simulate a full scaling decision workflow
      mockMetricsCollector.getQueueMetrics.mock.mockImplementation(async () => ({
        queue_totals: { messages: 15000 },
        message_stats: {
          publish_details: { rate: 1200 },
          deliver_get_details: { rate: 800 }
        }
      }));
      mockMetricsCollector.getDetailedQueues.mock.mockImplementation(async () => [
        { messages: 12000 },
        { messages: 3000 }
      ]);

      const now = Date.now() / 1000;
      mockKubernetesClient.getStabilityState.mock.mockImplementation(async () => ({
        stableProfile: 'HIGH',
        stableSince: now - 35
      }));

      const { metrics, profile } = await engine.calculateScaleProfile();
      assert.strictEqual(profile, 'HIGH');

      const shouldScale = await engine.checkProfileStability('MEDIUM', profile);
      assert.strictEqual(shouldScale, true);

      const message = engine.generateScalingMessage(profile);
      assert.ok(message.includes('HIGH'));
    });

    it('should handle oscillating load patterns', async () => {
      // First check - HIGH load
      mockKubernetesClient.getStabilityState.mock.mockImplementation(async () => ({
        stableProfile: 'HIGH',
        stableSince: Date.now() / 1000 - 10
      }));

      let result = await engine.checkProfileStability('MEDIUM', 'HIGH');
      assert.strictEqual(result, false); // Not stable long enough

      // Load drops - changes to MEDIUM (already at target)
      mockKubernetesClient.getStabilityState.mock.mockImplementation(async () => ({
        stableProfile: 'MEDIUM',
        stableSince: Date.now() / 1000 - 5
      }));
      
      result = await engine.checkProfileStability('MEDIUM', 'MEDIUM');
      assert.strictEqual(result, true); // Already at target

      // Load increases again - back to HIGH (profile changed)
      mockKubernetesClient.getStabilityState.mock.mockImplementation(async () => ({
        stableProfile: 'MEDIUM',
        stableSince: Date.now() / 1000 - 5
      }));

      result = await engine.checkProfileStability('MEDIUM', 'HIGH');
      assert.strictEqual(result, false); // Profile changed, reset timer
    });
  });
});