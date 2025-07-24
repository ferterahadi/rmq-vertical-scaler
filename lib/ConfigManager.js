import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

export default class ConfigManager {
  constructor(options = {}) {
    this.configPath = options.configPath;
    this.config = this.loadConfig();
  }

  loadConfig() {
    // Try to load from file first
    if (this.configPath && fs.existsSync(this.configPath)) {
      return JSON.parse(fs.readFileSync(this.configPath, 'utf-8'));
    }

    // Otherwise load from environment variables
    return this.loadFromEnvironment();
  }

  loadFromEnvironment() {
    const profileNames = (process.env.PROFILE_NAMES || 'LOW MEDIUM HIGH CRITICAL').split(' ');

    // Build profiles and thresholds from environment
    const profiles = {};
    const queueThresholds = {};
    const rateThresholds = {};

    for (let i = 0; i < profileNames.length; i++) {
      const name = profileNames[i];

      // Load profile resources
      profiles[name] = {
        cpu: process.env[`PROFILE_${name}_CPU`] || this.getDefaultCpu(i),
        memory: process.env[`PROFILE_${name}_MEMORY`] || this.getDefaultMemory(i)
      };

      // Load thresholds (first profile doesn't have thresholds)
      if (i > 0) {
        queueThresholds[name] = parseInt(process.env[`QUEUE_THRESHOLD_${name}`] || this.getDefaultQueueThreshold(i));
        rateThresholds[name] = parseInt(process.env[`RATE_THRESHOLD_${name}`] || this.getDefaultRateThreshold(i));
      }
    }

    return {
      profileNames,
      profiles,
      thresholds: {
        queue: queueThresholds,
        rate: rateThresholds
      },
      debounce: {
        scaleUpSeconds: parseInt(process.env.DEBOUNCE_SCALE_UP_SECONDS || '30'),
        scaleDownSeconds: parseInt(process.env.DEBOUNCE_SCALE_DOWN_SECONDS || '120')
      },
      checkInterval: parseInt(process.env.CHECK_INTERVAL_SECONDS || '5'),
      rmq: {
        host: process.env.RMQ_HOST || 'localhost',
        port: process.env.RMQ_PORT || '15672',
        user: process.env.RMQ_USER || 'guest',
        pass: process.env.RMQ_PASS || 'guest'
      },
      kubernetes: {
        namespace: process.env.NAMESPACE || 'default',
        rmqServiceName: process.env.RMQ_SERVICE_NAME || 'rmq',
        configMapName: process.env.CONFIG_MAP_NAME || 'rmq-vertical-scaler-config'
      }
    };
  }

  getDefaultCpu(profileIndex) {
    const defaults = ['330m', '800m', '1600m', '2400m'];
    return defaults[profileIndex] || '1000m';
  }

  getDefaultMemory(profileIndex) {
    const defaults = ['2Gi', '3Gi', '4Gi', '8Gi'];
    return defaults[profileIndex] || '2Gi';
  }

  getDefaultQueueThreshold(profileIndex) {
    const defaults = [0, 2000, 10000, 50000];
    return defaults[profileIndex] || 1000;
  }

  getDefaultRateThreshold(profileIndex) {
    const defaults = [0, 200, 1000, 2000];
    return defaults[profileIndex] || 100;
  }

  validate() {
    const errors = [];

    if (!this.config.rmq.host) {
      errors.push('RabbitMQ host is required (RMQ_HOST)');
    }

    if (!this.config.kubernetes.namespace) {
      errors.push('Kubernetes namespace is required (NAMESPACE)');
    }

    if (this.config.profileNames.length === 0) {
      errors.push('At least one profile must be defined');
    }

    if (errors.length > 0) {
      throw new Error(`Configuration validation failed:\n${errors.join('\n')}`);
    }

    return true;
  }
}