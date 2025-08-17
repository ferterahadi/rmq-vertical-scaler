/* eslint-disable prettier/prettier */
class ConfigManager {
    constructor() {
        // Load configuration from config file or environment
        this.config = this.loadConfig();

        // RabbitMQ connection details
        this.rmqHost = this.config.rmq.host;
        this.rmqPort = this.config.rmq.port;
        this.rmqUser = process.env.RMQ_USER || 'guest';
        this.rmqPass = process.env.RMQ_PASS || 'guest';

        // Dynamic resource names from environment
        this.rmqServiceName = process.env.RMQ_SERVICE_NAME || 'rmq';
        this.namespace = process.env.NAMESPACE || 'prod';
        this.configMapName = process.env.CONFIG_MAP_NAME || 'rmq-config';

        // Load configuration values
        this.thresholds = this.config.thresholds;
        this.profiles = this.config.profiles;
        this.scaleUpDebounceSeconds = this.config.debounce.scaleUpSeconds;
        this.scaleDownDebounceSeconds = this.config.debounce.scaleDownSeconds;
        this.checkIntervalSeconds = this.config.checkInterval;

        // Create CPU to profile mapping
        this.cpuToProfileMap = this.createCpuToProfileMap();
    }

    loadConfig() {
        // Load from environment variables
        const profileNames = (process.env.PROFILE_NAMES || 'LOW MEDIUM HIGH CRITICAL').split(' ');

        // Build profiles and thresholds from environment
        const profiles = {};
        const queueThresholds = {};
        const rateThresholds = {};

        for (let i = 0; i < profileNames.length; i++) {
            const name = profileNames[i];

            // Load profile resources
            profiles[name] = {
                cpu: process.env[`PROFILE_${name}_CPU`] || '1000m',
                memory: process.env[`PROFILE_${name}_MEMORY`] || '2Gi'
            };

            // Load thresholds (first profile doesn't have thresholds)
            if (i > 0) {
                queueThresholds[name] = parseInt(process.env[`QUEUE_THRESHOLD_${name}`] || '1000');
                rateThresholds[name] = parseInt(process.env[`RATE_THRESHOLD_${name}`] || '100');
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
                host: process.env.RMQ_HOST,
                port: process.env.RMQ_PORT
            }
        };
    }

    createCpuToProfileMap() {
        const mapping = {};
        Object.entries(this.profiles).forEach(([profile, resources]) => {
            mapping[resources.cpu] = profile;
        });
        return mapping;
    }
}

export default ConfigManager;