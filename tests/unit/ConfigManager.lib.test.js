import { describe, it, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert';
import ConfigManager from '../../lib/ConfigManager.js';

describe('ConfigManager (lib)', () => {
  let configManager;
  let originalEnv;

  beforeEach(() => {
    // Save original environment variables
    originalEnv = { ...process.env };
    
    // Clear relevant environment variables for clean tests
    delete process.env.PROFILE_NAMES;
    delete process.env.RMQ_USER;
    delete process.env.RMQ_PASS;
    delete process.env.RMQ_SERVICE_NAME;
    delete process.env.NAMESPACE;
    delete process.env.CONFIG_MAP_NAME;
    delete process.env.RMQ_HOST;
    delete process.env.RMQ_PORT;
    delete process.env.DEBOUNCE_SCALE_UP_SECONDS;
    delete process.env.DEBOUNCE_SCALE_DOWN_SECONDS;
    delete process.env.CHECK_INTERVAL_SECONDS;
  });

  afterEach(() => {
    // Restore original environment variables
    process.env = originalEnv;
  });

  describe('constructor and defaults', () => {
    it('should initialize with default values when no environment variables are set', () => {
      configManager = new ConfigManager();
      
      assert.strictEqual(configManager.rmqUser, 'guest');
      assert.strictEqual(configManager.rmqPass, 'guest');
      assert.strictEqual(configManager.rmqServiceName, 'rmq');
      assert.strictEqual(configManager.namespace, 'prod');
      assert.strictEqual(configManager.configMapName, 'rmq-config');
      assert.strictEqual(configManager.scaleUpDebounceSeconds, 30);
      assert.strictEqual(configManager.scaleDownDebounceSeconds, 120);
      assert.strictEqual(configManager.checkIntervalSeconds, 5);
    });

    it('should use environment variables when provided', () => {
      process.env.RMQ_USER = 'admin';
      process.env.RMQ_PASS = 'secret123';
      process.env.RMQ_SERVICE_NAME = 'my-rabbit';
      process.env.NAMESPACE = 'staging';
      process.env.CONFIG_MAP_NAME = 'rabbit-config';
      process.env.RMQ_HOST = 'rabbit.example.com';
      process.env.RMQ_PORT = '15672';
      
      configManager = new ConfigManager();
      
      assert.strictEqual(configManager.rmqUser, 'admin');
      assert.strictEqual(configManager.rmqPass, 'secret123');
      assert.strictEqual(configManager.rmqServiceName, 'my-rabbit');
      assert.strictEqual(configManager.namespace, 'staging');
      assert.strictEqual(configManager.configMapName, 'rabbit-config');
      assert.strictEqual(configManager.rmqHost, 'rabbit.example.com');
      assert.strictEqual(configManager.rmqPort, '15672');
    });
  });

  describe('loadConfig', () => {
    it('should load default profile names', () => {
      configManager = new ConfigManager();
      
      assert.deepStrictEqual(configManager.config.profileNames, ['LOW', 'MEDIUM', 'HIGH', 'CRITICAL']);
    });

    it('should load custom profile names from environment', () => {
      process.env.PROFILE_NAMES = 'MINIMAL STANDARD HEAVY EXTREME';
      
      configManager = new ConfigManager();
      
      assert.deepStrictEqual(configManager.config.profileNames, ['MINIMAL', 'STANDARD', 'HEAVY', 'EXTREME']);
    });

    it('should build default profiles with default resources', () => {
      configManager = new ConfigManager();
      
      assert.deepStrictEqual(configManager.profiles, {
        LOW: { cpu: '1000m', memory: '2Gi' },
        MEDIUM: { cpu: '1000m', memory: '2Gi' },
        HIGH: { cpu: '1000m', memory: '2Gi' },
        CRITICAL: { cpu: '1000m', memory: '2Gi' }
      });
    });

    it('should load custom profile resources from environment', () => {
      process.env.PROFILE_LOW_CPU = '500m';
      process.env.PROFILE_LOW_MEMORY = '1Gi';
      process.env.PROFILE_MEDIUM_CPU = '1000m';
      process.env.PROFILE_MEDIUM_MEMORY = '2Gi';
      process.env.PROFILE_HIGH_CPU = '2000m';
      process.env.PROFILE_HIGH_MEMORY = '4Gi';
      process.env.PROFILE_CRITICAL_CPU = '4000m';
      process.env.PROFILE_CRITICAL_MEMORY = '8Gi';
      
      configManager = new ConfigManager();
      
      assert.deepStrictEqual(configManager.profiles, {
        LOW: { cpu: '500m', memory: '1Gi' },
        MEDIUM: { cpu: '1000m', memory: '2Gi' },
        HIGH: { cpu: '2000m', memory: '4Gi' },
        CRITICAL: { cpu: '4000m', memory: '8Gi' }
      });
    });

    it('should load default thresholds', () => {
      configManager = new ConfigManager();
      
      assert.deepStrictEqual(configManager.thresholds, {
        queue: {
          MEDIUM: 1000,
          HIGH: 1000,
          CRITICAL: 1000
        },
        rate: {
          MEDIUM: 100,
          HIGH: 100,
          CRITICAL: 100
        }
      });
    });

    it('should load custom thresholds from environment', () => {
      process.env.QUEUE_THRESHOLD_MEDIUM = '2000';
      process.env.QUEUE_THRESHOLD_HIGH = '10000';
      process.env.QUEUE_THRESHOLD_CRITICAL = '50000';
      process.env.RATE_THRESHOLD_MEDIUM = '200';
      process.env.RATE_THRESHOLD_HIGH = '1000';
      process.env.RATE_THRESHOLD_CRITICAL = '5000';
      
      configManager = new ConfigManager();
      
      assert.deepStrictEqual(configManager.thresholds, {
        queue: {
          MEDIUM: 2000,
          HIGH: 10000,
          CRITICAL: 50000
        },
        rate: {
          MEDIUM: 200,
          HIGH: 1000,
          CRITICAL: 5000
        }
      });
    });

    it('should not create thresholds for the first profile', () => {
      configManager = new ConfigManager();
      
      assert.strictEqual(configManager.thresholds.queue.LOW, undefined);
      assert.strictEqual(configManager.thresholds.rate.LOW, undefined);
    });

    it('should load debounce settings from environment', () => {
      process.env.DEBOUNCE_SCALE_UP_SECONDS = '60';
      process.env.DEBOUNCE_SCALE_DOWN_SECONDS = '300';
      
      configManager = new ConfigManager();
      
      assert.strictEqual(configManager.scaleUpDebounceSeconds, 60);
      assert.strictEqual(configManager.scaleDownDebounceSeconds, 300);
    });

    it('should load check interval from environment', () => {
      process.env.CHECK_INTERVAL_SECONDS = '10';
      
      configManager = new ConfigManager();
      
      assert.strictEqual(configManager.checkIntervalSeconds, 10);
    });

    it('should handle RabbitMQ connection details', () => {
      process.env.RMQ_HOST = 'rabbitmq.cluster.local';
      process.env.RMQ_PORT = '5672';
      
      configManager = new ConfigManager();
      
      assert.strictEqual(configManager.rmqHost, 'rabbitmq.cluster.local');
      assert.strictEqual(configManager.rmqPort, '5672');
    });
  });

  describe('createCpuToProfileMap', () => {
    it('should create correct CPU to profile mapping', () => {
      process.env.PROFILE_LOW_CPU = '330m';
      process.env.PROFILE_MEDIUM_CPU = '800m';
      process.env.PROFILE_HIGH_CPU = '1600m';
      process.env.PROFILE_CRITICAL_CPU = '2400m';
      
      configManager = new ConfigManager();
      
      assert.deepStrictEqual(configManager.cpuToProfileMap, {
        '330m': 'LOW',
        '800m': 'MEDIUM',
        '1600m': 'HIGH',
        '2400m': 'CRITICAL'
      });
    });

    it('should handle duplicate CPU values', () => {
      process.env.PROFILE_LOW_CPU = '1000m';
      process.env.PROFILE_MEDIUM_CPU = '1000m';
      process.env.PROFILE_HIGH_CPU = '2000m';
      process.env.PROFILE_CRITICAL_CPU = '2000m';
      
      configManager = new ConfigManager();
      
      // Last profile with the same CPU value wins
      assert.strictEqual(configManager.cpuToProfileMap['1000m'], 'MEDIUM');
      assert.strictEqual(configManager.cpuToProfileMap['2000m'], 'CRITICAL');
    });

    it('should work with custom profile names', () => {
      process.env.PROFILE_NAMES = 'TINY SMALL BIG HUGE';
      process.env.PROFILE_TINY_CPU = '100m';
      process.env.PROFILE_SMALL_CPU = '500m';
      process.env.PROFILE_BIG_CPU = '2000m';
      process.env.PROFILE_HUGE_CPU = '8000m';
      
      configManager = new ConfigManager();
      
      assert.deepStrictEqual(configManager.cpuToProfileMap, {
        '100m': 'TINY',
        '500m': 'SMALL',
        '2000m': 'BIG',
        '8000m': 'HUGE'
      });
    });
  });

  describe('integration scenarios', () => {
    it('should handle complete configuration setup', () => {
      // Set up a complete environment
      process.env.PROFILE_NAMES = 'LOW MED HIGH';
      process.env.RMQ_HOST = 'rabbit.prod.example.com';
      process.env.RMQ_PORT = '15672';
      process.env.RMQ_USER = 'prod_user';
      process.env.RMQ_PASS = 'prod_pass';
      process.env.RMQ_SERVICE_NAME = 'prod-rabbit';
      process.env.NAMESPACE = 'production';
      process.env.CONFIG_MAP_NAME = 'prod-rabbit-config';
      
      process.env.PROFILE_LOW_CPU = '250m';
      process.env.PROFILE_LOW_MEMORY = '512Mi';
      process.env.PROFILE_MED_CPU = '1000m';
      process.env.PROFILE_MED_MEMORY = '2Gi';
      process.env.PROFILE_HIGH_CPU = '4000m';
      process.env.PROFILE_HIGH_MEMORY = '8Gi';
      
      process.env.QUEUE_THRESHOLD_MED = '5000';
      process.env.QUEUE_THRESHOLD_HIGH = '20000';
      process.env.RATE_THRESHOLD_MED = '500';
      process.env.RATE_THRESHOLD_HIGH = '2000';
      
      process.env.DEBOUNCE_SCALE_UP_SECONDS = '45';
      process.env.DEBOUNCE_SCALE_DOWN_SECONDS = '180';
      process.env.CHECK_INTERVAL_SECONDS = '15';
      
      configManager = new ConfigManager();
      
      // Verify all settings
      assert.strictEqual(configManager.rmqHost, 'rabbit.prod.example.com');
      assert.strictEqual(configManager.rmqPort, '15672');
      assert.strictEqual(configManager.rmqUser, 'prod_user');
      assert.strictEqual(configManager.rmqPass, 'prod_pass');
      assert.strictEqual(configManager.rmqServiceName, 'prod-rabbit');
      assert.strictEqual(configManager.namespace, 'production');
      assert.strictEqual(configManager.configMapName, 'prod-rabbit-config');
      
      assert.deepStrictEqual(configManager.config.profileNames, ['LOW', 'MED', 'HIGH']);
      
      assert.deepStrictEqual(configManager.profiles, {
        LOW: { cpu: '250m', memory: '512Mi' },
        MED: { cpu: '1000m', memory: '2Gi' },
        HIGH: { cpu: '4000m', memory: '8Gi' }
      });
      
      assert.deepStrictEqual(configManager.thresholds.queue, {
        MED: 5000,
        HIGH: 20000
      });
      
      assert.deepStrictEqual(configManager.thresholds.rate, {
        MED: 500,
        HIGH: 2000
      });
      
      assert.strictEqual(configManager.scaleUpDebounceSeconds, 45);
      assert.strictEqual(configManager.scaleDownDebounceSeconds, 180);
      assert.strictEqual(configManager.checkIntervalSeconds, 15);
      
      assert.deepStrictEqual(configManager.cpuToProfileMap, {
        '250m': 'LOW',
        '1000m': 'MED',
        '4000m': 'HIGH'
      });
    });

    it('should handle minimal configuration', () => {
      // Only set the most essential variables
      process.env.RMQ_HOST = 'localhost';
      process.env.RMQ_PORT = '15672';
      
      configManager = new ConfigManager();
      
      // Should still work with defaults
      assert.strictEqual(configManager.rmqHost, 'localhost');
      assert.strictEqual(configManager.rmqPort, '15672');
      assert.ok(configManager.profiles);
      assert.ok(configManager.thresholds);
      assert.ok(configManager.cpuToProfileMap);
    });

    it('should handle invalid number values gracefully', () => {
      process.env.DEBOUNCE_SCALE_UP_SECONDS = 'not-a-number';
      process.env.CHECK_INTERVAL_SECONDS = 'invalid';
      process.env.QUEUE_THRESHOLD_MEDIUM = 'abc';
      
      configManager = new ConfigManager();
      
      // Should use NaN or fallback to defaults
      assert.ok(isNaN(configManager.scaleUpDebounceSeconds) || configManager.scaleUpDebounceSeconds === 30);
      assert.ok(isNaN(configManager.checkIntervalSeconds) || configManager.checkIntervalSeconds === 5);
      assert.ok(isNaN(configManager.thresholds.queue.MEDIUM) || configManager.thresholds.queue.MEDIUM === 1000);
    });

    it('should handle empty profile names gracefully', () => {
      process.env.PROFILE_NAMES = '';
      
      configManager = new ConfigManager();
      
      // Should handle empty string
      assert.ok(configManager.config.profileNames.length > 0);
    });

    it('should preserve all configuration in config object', () => {
      configManager = new ConfigManager();
      
      assert.ok(configManager.config);
      assert.ok(configManager.config.profileNames);
      assert.ok(configManager.config.profiles);
      assert.ok(configManager.config.thresholds);
      assert.ok(configManager.config.debounce);
      assert.ok(typeof configManager.config.checkInterval === 'number');
      assert.ok(configManager.config.rmq);
    });
  });

  describe('error scenarios', () => {
    it('should handle missing RMQ host and port', () => {
      // Don't set RMQ_HOST or RMQ_PORT
      configManager = new ConfigManager();
      
      assert.strictEqual(configManager.rmqHost, undefined);
      assert.strictEqual(configManager.rmqPort, undefined);
    });

    it('should handle profile iteration correctly', () => {
      process.env.PROFILE_NAMES = 'SINGLE';
      
      configManager = new ConfigManager();
      
      // Should have only one profile
      assert.deepStrictEqual(configManager.config.profileNames, ['SINGLE']);
      assert.deepStrictEqual(configManager.profiles, {
        SINGLE: { cpu: '1000m', memory: '2Gi' }
      });
      
      // No thresholds for single profile
      assert.deepStrictEqual(configManager.thresholds.queue, {});
      assert.deepStrictEqual(configManager.thresholds.rate, {});
    });
  });
});