/* eslint-disable prettier/prettier */
/* eslint-disable @typescript-eslint/no-var-requires */
const axios = require('axios');
const k8s = require('@kubernetes/client-node');
const fs = require('fs');

class RabbitMQVerticalScaler {
    constructor() {
        // Load configuration from config file or environment
        this.config = this.loadConfig();

        // RabbitMQ connection details
        this.rmqHost = this.config.rmq.host;
        this.rmqPort = this.config.rmq.port;
        this.rmqUser = process.env.RMQ_USER || 'guest';
        this.rmqPass = process.env.RMQ_PASS || 'guest';

        // Load configuration values
        this.thresholds = this.config.thresholds;
        this.profiles = this.config.profiles;
        this.scaleUpDebounceSeconds = this.config.debounce.scaleUpSeconds;
        this.scaleDownDebounceMinutes = this.config.debounce.scaleDownMinutes;
        this.checkIntervalSeconds = this.config.checkInterval;

        // Create CPU to profile mapping
        this.cpuToProfileMap = this.createCpuToProfileMap();

        // Initialize Kubernetes client
        this.kc = new k8s.KubeConfig();
        this.kc.loadFromCluster();
        this.k8sApi = this.kc.makeApiClient(k8s.CoreV1Api);
        this.customApi = this.kc.makeApiClient(k8s.CustomObjectsApi);
    }

    loadConfig() {
        try {
            // Try to load from mounted config file first
            if (fs.existsSync('/config/config.json')) {
                const configData = fs.readFileSync('/config/config.json', 'utf8');
                return JSON.parse(configData);
            }
        } catch (error) {
            console.warn('Could not load config file, using defaults:', error.message);
        }

        // Fallback to default configuration
        return {
            thresholds: {
                queue: { low: 1000, medium: 2000, high: 10000, critical: 50000 },
                rate: { low: 20, medium: 200, high: 1000, critical: 2000 }
            },
            profiles: {
                LOW: { cpu: '330m', memory: '2Gi' },
                MEDIUM: { cpu: '800m', memory: '3Gi' },
                HIGH: { cpu: '1600m', memory: '4Gi' },
                CRITICAL: { cpu: '2400m', memory: '8Gi' }
            },
            debounce: { scaleUpSeconds: 30, scaleDownMinutes: 2 },
            checkInterval: 5, // Check every 5 seconds
            rmq: { host: 'rmq.prod.svc.cluster.local', port: '15672' }
        };
    }

    createCpuToProfileMap() {
        const mapping = {};
        Object.entries(this.profiles).forEach(([profile, resources]) => {
            mapping[resources.cpu] = profile;
        });
        return mapping;
    }

    async getQueueMetrics() {
        try {
            console.log('✅ Fetching overview metrics from RabbitMQ API...');
            const response = await axios.get(`http://${this.rmqHost}:${this.rmqPort}/api/overview`, {
                auth: {
                    username: this.rmqUser,
                    password: this.rmqPass
                },
                timeout: 10000
            });
            return response.data;
        } catch (error) {
            console.error('[ERROR] Failed to connect to RabbitMQ API:', error.message);
            return {};
        }
    }

    async getDetailedQueues() {
        try {
            console.log('✅ Fetching queue details from RabbitMQ API...');
            const response = await axios.get(`http://${this.rmqHost}:${this.rmqPort}/api/queues`, {
                auth: {
                    username: this.rmqUser,
                    password: this.rmqPass
                },
                timeout: 10000
            });
            console.log(`✅ Retrieved details for ${response.data.length} queues`);
            return response.data;
        } catch (error) {
            console.error('[ERROR] Failed to fetch queue details:', error.message);
            return [];
        }
    }

    async calculateScaleProfile() {
        const overview = await this.getQueueMetrics();
        const queues = await this.getDetailedQueues();

        // Skip processing if we got error responses
        if (Object.keys(overview).length === 0 || queues.length === 0) {
            console.warn('[WARNING] Using default values due to API errors');
            return {
                metrics: { totalMessages: 0, maxQueueDepth: 0, messageRate: 0, consumeRate: 0, backlogRate: 0 },
                profile: 'LOW'
            };
        }

        // Extract key metrics with error handling
        const totalMessages = overview.queue_totals?.messages || 0;
        const messageRate = overview.message_stats?.publish_details?.rate || 0;
        const consumeRate = (overview.message_stats?.deliver_get_details?.rate || 0) +
            (overview.message_stats?.deliver_details?.rate || 0) +
            (overview.message_stats?.get_details?.rate || 0);
        const maxQueueDepth = Math.max(...queues.map(q => q.messages || 0), 0);
        const backlogRate = messageRate - consumeRate;

        const metrics = {
            totalMessages,
            maxQueueDepth,
            messageRate,
            consumeRate,
            backlogRate
        };

        // Determine scale profile based on metrics
        let profile = 'LOW';
        if (maxQueueDepth > this.thresholds.queue.critical || messageRate > this.thresholds.rate.critical) {
            profile = 'CRITICAL';
        } else if (maxQueueDepth > this.thresholds.queue.high || messageRate > this.thresholds.rate.high) {
            profile = 'HIGH';
        } else if (maxQueueDepth > this.thresholds.queue.medium || messageRate > this.thresholds.rate.medium) {
            profile = 'MEDIUM';
        }

        return { metrics, profile };
    }

    async getCurrentProfile() {
        try {
            const response = await this.customApi.getNamespacedCustomObject(
                'rabbitmq.com', 'v1beta1', 'prod', 'rabbitmqclusters', 'rmq'
            );
            const currentCpu = response.body.spec?.resources?.requests?.cpu || '0';

            return this.cpuToProfileMap[currentCpu] || 'UNKNOWN';
        } catch (error) {
            console.error('Error getting current profile:', error.message);
            return 'UNKNOWN';
        }
    }

    getProfilePriority(profile) {
        const priorities = { LOW: 1, MEDIUM: 2, HIGH: 3, CRITICAL: 4 };
        return priorities[profile] || 0;
    }

    async getStabilityState() {
        try {
            const response = await this.k8sApi.readNamespacedConfigMap('rmq-scaler-state', 'prod');
            return {
                stableProfile: response.body.data?.stable_profile || '',
                stableSince: parseInt(response.body.data?.stable_since || '0')
            };
        } catch (error) {
            return { stableProfile: '', stableSince: 0 };
        }
    }

    async updateStabilityTracking(profile) {
        const currentTime = Math.floor(Date.now() / 1000);
        const configMapData = {
            stable_profile: profile,
            stable_since: currentTime.toString()
        };

        try {
            // Try to get existing configmap to preserve other data
            try {
                const existing = await this.k8sApi.readNamespacedConfigMap('rmq-scaler-state', 'prod');
                Object.assign(configMapData, existing.body.data);
                configMapData.stable_profile = profile;
                configMapData.stable_since = currentTime.toString();
            } catch (e) {
                // ConfigMap doesn't exist, will create new one
            }

            const configMap = {
                metadata: { name: 'rmq-scaler-state', namespace: 'prod' },
                data: configMapData
            };

            try {
                await this.k8sApi.replaceNamespacedConfigMap('rmq-scaler-state', 'prod', configMap);
            } catch (e) {
                await this.k8sApi.createNamespacedConfigMap('prod', configMap);
            }
        } catch (error) {
            console.error('Error updating stability tracking:', error.message);
        }
    }

    async updateScaleState(newProfile) {
        const currentTime = Math.floor(Date.now() / 1000);
        let configMapData = {
            last_scaled_profile: newProfile,
            last_scale_time: currentTime.toString()
        };

        try {
            // Preserve stability tracking
            const stability = await this.getStabilityState();
            configMapData.stable_profile = stability.stableProfile;
            configMapData.stable_since = stability.stableSince.toString();

            const configMap = {
                metadata: { name: 'rmq-scaler-state', namespace: 'prod' },
                data: configMapData
            };

            try {
                await this.k8sApi.replaceNamespacedConfigMap('rmq-scaler-state', 'prod', configMap);
            } catch (e) {
                await this.k8sApi.createNamespacedConfigMap('prod', configMap);
            }
        } catch (error) {
            console.error('Error updating scale state:', error.message);
        }
    }

    async checkProfileStability(currentProfile, recommendedProfile) {
        const currentPriority = this.getProfilePriority(currentProfile);
        const recommendedPriority = this.getProfilePriority(recommendedProfile);

        const stability = await this.getStabilityState();
        const currentTime = Math.floor(Date.now() / 1000);

        // If recommendation changed from what we're tracking, update tracking
        if (stability.stableProfile !== recommendedProfile) {
            console.log(`🔄 Profile recommendation changed from ${stability.stableProfile} to ${recommendedProfile}`);
            await this.updateStabilityTracking(recommendedProfile);
            return false;
        }

        // If already at recommended profile, no need to check stability
        if (currentProfile === recommendedProfile) {
            return true;
        }

        // Check if recommendation has been stable long enough
        const timeStable = currentTime - stability.stableSince;
        const isScaleUp = recommendedPriority > currentPriority;

        if (isScaleUp) {
            // Scale-up debounce
            if (timeStable < this.scaleUpDebounceSeconds) {
                const remaining = this.scaleUpDebounceSeconds - timeStable;
                console.log(`⏳ Scale-up debounce: ${recommendedProfile} stable for ${timeStable}s, need ${this.scaleUpDebounceSeconds}s (${remaining}s remaining)`);
                return false;
            }
        } else {
            // Scale-down debounce
            const debounceSeconds = this.scaleDownDebounceMinutes * 60;
            if (timeStable < debounceSeconds) {
                const remaining = debounceSeconds - timeStable;
                const minutes = Math.floor(remaining / 60);
                const seconds = remaining % 60;
                console.log(`⏳ Scale-down debounce: ${recommendedProfile} stable for ${timeStable}s, need ${debounceSeconds}s (${minutes}m ${seconds}s remaining)`);
                return false;
            }
        }

        console.log('✅ Profile has been stable for required duration');
        return true;
    }

    async applyScale() {
        console.log('🔍 Analyzing RabbitMQ metrics...');
        const { metrics, profile } = await this.calculateScaleProfile();

        // Display metrics summary
        console.log(`📊 Queue Depth: ${metrics.maxQueueDepth} | Total: ${metrics.totalMessages} | Publish: ${metrics.messageRate}/s | Consume: ${metrics.consumeRate}/s | Backlog: ${metrics.backlogRate}/s`);

        // Get current profile
        const currentProfile = await this.getCurrentProfile();
        console.log(`📍 Current profile: ${currentProfile}`);

        // Set resources based on profile
        const resources = this.profiles[profile];
        let message = '';
        switch (profile) {
            case 'CRITICAL':
                message = '🚨 CRITICAL load detected - scaling to maximum resources';
                break;
            case 'HIGH':
                message = '⚠️  HIGH load detected - scaling up resources';
                break;
            case 'MEDIUM':
                message = '📈 MEDIUM load detected - moderate scaling';
                break;
            case 'LOW':
                message = '✅ LOW load detected - minimal resources';
                break;
            default:
                console.error(`❓ Unknown profile '${profile}', defaulting to LOW`);
                profile = 'LOW';
                resources = this.profiles.LOW;
        }
        console.log(message);

        // Skip if already at target profile
        if (currentProfile === profile) {
            console.log(`⏸️  Scaling skipped - already at ${profile} profile`);
            return;
        }

        // Check if profile recommendation has been stable long enough
        if (!(await this.checkProfileStability(currentProfile, profile))) {
            console.log('⏸️  Profile not stable long enough yet');
            return;
        }

        console.log(`⚙️  Applying: CPU=${resources.cpu}, Memory=${resources.memory}`);

        try {
            // Apply the patch to RabbitMQ cluster
            const patch = {
                spec: {
                    resources: {
                        requests: {
                            cpu: resources.cpu,
                            memory: resources.memory
                        }
                    }
                }
            };

            await this.customApi.patchNamespacedCustomObject(
                'rabbitmq.com', 'v1beta1', 'prod', 'rabbitmqclusters', 'rmq',
                patch,
                undefined, undefined, undefined,
                { headers: { 'Content-Type': 'application/merge-patch+json' } }
            );

            console.log('✅ Scaling completed successfully');
            // Update scale state after successful scaling
            await this.updateScaleState(profile);
            // Reset stability tracking since we just scaled
            await this.updateStabilityTracking(profile);
        } catch (error) {
            console.error('❌ Scaling failed:', error.message);
        }
    }

    async waitForRabbitMQ() {
        console.log('⏳ Waiting for RabbitMQ to be ready...');
        for (let i = 1; i <= 10; i++) {
            try {
                await axios.get(`http://${this.rmqHost}:${this.rmqPort}/api/overview`, {
                    auth: {
                        username: this.rmqUser,
                        password: this.rmqPass
                    },
                    timeout: 5000
                });
                console.log('✅ RabbitMQ is ready');
                return true;
            } catch (error) {
                console.log(`⏳ Waiting for RabbitMQ... (${i}/10)`);
                await new Promise(resolve => setTimeout(resolve, 5000));
            }
        }
        console.error('❌ RabbitMQ not ready after 10 attempts');
        return false;
    }

    async main() {
        console.log('🚀 RabbitMQ Vertical Scaler (Node.js)');

        if (!(await this.waitForRabbitMQ())) {
            process.exit(1);
        }

        // Run scaling loop
        while (true) {
            try {
                await this.applyScale();
                console.log('---');
                // Wait for configured interval (default 5 seconds)
                const intervalMs = (this.checkIntervalSeconds || 5) * 1000;
                await new Promise(resolve => setTimeout(resolve, intervalMs));
            } catch (error) {
                console.error('Error in scaling loop:', error.message);
                // Wait 30 seconds before retrying on error
                await new Promise(resolve => setTimeout(resolve, 30000));
            }
        }
    }
}

// Main execution
const scaler = new RabbitMQVerticalScaler();
scaler.main().catch(error => {
    console.error('Fatal error:', error);
    process.exit(1);
}); 