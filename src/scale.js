/* eslint-disable prettier/prettier */
import axios from 'axios';
import * as k8s from '@kubernetes/client-node';
import fs from 'fs';

class RabbitMQVerticalScaler {
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
        this.scalerName = process.env.SCALER_NAME || 'rmq';
        this.namespace = process.env.NAMESPACE || 'prod';
        this.configMapName = `${this.scalerName}-config`;

        // Load configuration values
        this.thresholds = this.config.thresholds;
        this.profiles = this.config.profiles;
        this.scaleUpDebounceSeconds = this.config.debounce.scaleUpSeconds;
        this.scaleDownDebounceSeconds = this.config.debounce.scaleDownSeconds;
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
                profile: this.config.profileNames[0] // Use first profile as default
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
        // Start with the lowest profile
        let profile = this.config.profileNames[0];
        
        // Check thresholds from highest to lowest
        for (let i = this.config.profileNames.length - 1; i > 0; i--) {
            const profileName = this.config.profileNames[i];
            const queueThreshold = this.thresholds.queue[profileName];
            const rateThreshold = this.thresholds.rate[profileName];
            
            if ((queueThreshold && maxQueueDepth > queueThreshold) || 
                (rateThreshold && messageRate > rateThreshold)) {
                profile = profileName;
                break;
            }
        }

        return { metrics, profile };
    }

    async getCurrentProfile() {
        try {
            const response = await this.customApi.getNamespacedCustomObject({
                group: 'rabbitmq.com',
                version: 'v1beta1',
                namespace: this.namespace,
                plural: 'rabbitmqclusters',
                name: this.rmqServiceName
            });

            const currentCpu = response.spec?.resources?.requests?.cpu || '0';

            return this.cpuToProfileMap[currentCpu] || 'UNKNOWN';
        } catch (error) {
            console.error('Error getting current profile:', error.message);
            return 'UNKNOWN';
        }
    }

    getProfilePriority(profile) {
        // Priority based on position in profile names array
        const index = this.config.profileNames.indexOf(profile);
        return index >= 0 ? index + 1 : 0;
    }

    async getStabilityState() {
        try {
            const response = await this.k8sApi.namespacedconfigmap({
                name: this.configMapName,
                namespace: this.namespace
            });

            return {
                stableProfile: response.data?.stable_profile || '',
                stableSince: parseInt(response.data?.stable_since || '0')
            };
        } catch (error) {
            return { stableProfile: '', stableSince: 0 };
        }
    }

    async updateStabilityTracking(profile) {
        const currentTime = Math.floor(Date.now() / 1000);
        
        try {
            // Get existing configmap (should exist from deployment)
            const existing = await this.k8sApi.readNamespacedConfigMap({
                name: this.configMapName,
                namespace: this.namespace
            });

            const configMapData = { ...existing.data };
            
            // Update stability tracking fields
            configMapData.stable_profile = profile;
            configMapData.stable_since = currentTime.toString();

            const configMap = {
                data: configMapData
            };

            await this.k8sApi.patchNamespacedConfigMap({
                name: this.configMapName,
                namespace: this.namespace,
                body: configMap
            });
            console.log(`📝 Updated stability tracking: ${profile} since ${currentTime}`);
        } catch (error) {
            console.error('Error updating stability tracking:', error.message);
        }
    }

    async updateScaleState(newProfile) {
        const currentTime = Math.floor(Date.now() / 1000);

        try {
            // Get existing configmap and preserve all data
            const existing = await this.k8sApi.readNamespacedConfigMap({
                name: this.configMapName,
                namespace: this.namespace
            });

            const configMapData = { ...existing.data };
            
            // Update scale state fields
            configMapData.last_scaled_profile = newProfile;
            configMapData.last_scale_time = currentTime.toString();

            const configMap = {
                data: configMapData
            };

            await this.k8sApi.patchNamespacedConfigMap({
                name: this.configMapName,
                namespace: this.namespace,
                body: configMap
            });
            console.log(`📝 Updated scale state: ${newProfile} at ${currentTime}`);
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
            if (timeStable < this.scaleDownDebounceSeconds) {
                const remaining = this.scaleDownDebounceSeconds - timeStable;
                console.log(`⏳ Scale-down debounce: ${recommendedProfile} stable for ${timeStable}s, need ${this.scaleDownDebounceSeconds}s (${remaining}s remaining)`);
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
        if (!resources) {
            console.error(`❓ Unknown profile '${profile}', defaulting to ${this.config.profileNames[0]}`);
            profile = this.config.profileNames[0];
            resources = this.profiles[profile];
        }
        
        // Generate appropriate message based on profile position
        const profileIndex = this.config.profileNames.indexOf(profile);
        const profileCount = this.config.profileNames.length;
        let message = '';
        
        if (profileIndex === 0) {
            message = `✅ ${profile} load detected - minimal resources`;
        } else if (profileIndex === profileCount - 1) {
            message = `🚨 ${profile} load detected - scaling to maximum resources`;
        } else if (profileIndex > profileCount / 2) {
            message = `⚠️  ${profile} load detected - scaling up resources`;
        } else {
            message = `📈 ${profile} load detected - moderate scaling`;
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

            await this.customApi.patchNamespacedCustomObject({
                group: 'rabbitmq.com',
                version: 'v1beta1',
                namespace: this.namespace,
                plural: 'rabbitmqclusters',
                name: this.rmqServiceName,
                body: patch
            });

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