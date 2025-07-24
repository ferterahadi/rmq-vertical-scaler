import MetricsCollector from './MetricsCollector.js';
import ScalingEngine from './ScalingEngine.js';
import KubernetesClient from './KubernetesClient.js';
import ConfigManager from './ConfigManager.js';

export default class RabbitMQVerticalScaler {
    constructor(options = {}) {
        this.options = options;
        
        // Load and validate configuration
        this.configManager = new ConfigManager(options);
        this.config = this.configManager.config;
        this.configManager.validate();
        
        // Initialize components
        this.metricsCollector = new MetricsCollector({
            rmqHost: this.config.rmq.host,
            rmqPort: this.config.rmq.port,
            rmqUser: this.config.rmq.user,
            rmqPass: this.config.rmq.pass
        });
        
        this.scalingEngine = new ScalingEngine({
            profiles: this.config.profiles,
            thresholds: this.config.thresholds,
            profileNames: this.config.profileNames,
            debounce: this.config.debounce
        });
        
        this.k8sClient = new KubernetesClient({
            namespace: this.config.kubernetes.namespace,
            rmqServiceName: this.config.kubernetes.rmqServiceName,
            configMapName: this.config.kubernetes.configMapName
        });
        
        // Runtime state
        this.isRunning = false;
        this.checkIntervalSeconds = this.config.checkInterval;
    }

    async start() {
        console.log('🚀 Starting RabbitMQ Vertical Scaler');
        console.log(`📊 Profiles: ${this.config.profileNames.join(', ')}`);
        console.log(`⏱️  Check interval: ${this.checkIntervalSeconds}s`);
        console.log(`🏃 Mode: ${this.options.dryRun ? 'DRY RUN' : 'ACTIVE'}`);
        
        this.isRunning = true;
        await this.waitForRabbitMQ();
        await this.runScalingLoop();
    }
    
    async stop() {
        console.log('🛑 Stopping RabbitMQ Vertical Scaler');
        this.isRunning = false;
    }

    async waitForRabbitMQ() {
        console.log('⏳ Waiting for RabbitMQ to be ready...');
        let attempts = 0;
        const maxAttempts = this.options.maxRetries || 10;
        
        while (attempts < maxAttempts) {
            attempts++;
            try {
                await this.metricsCollector.getQueueMetrics();
                console.log('✅ RabbitMQ is ready');
                return true;
            } catch (error) {
                if (attempts >= maxAttempts) {
                    throw new Error(`Failed to connect to RabbitMQ after ${attempts} attempts: ${error.message}`);
                }
                console.log(`⏳ Waiting for RabbitMQ... (attempt ${attempts})`);
                await new Promise(resolve => setTimeout(resolve, 5000));
            }
        }
    }

    async runScalingLoop() {
        while (this.isRunning) {
            try {
                await this.processScalingDecision();
                console.log('---');
            } catch (error) {
                console.error('❌ Error in scaling loop:', error.message);
                if (this.options.debug) {
                    console.error(error.stack);
                }
            }
            
            await this.sleep(this.checkIntervalSeconds * 1000);
        }
    }

    async processScalingDecision() {
        // Get current metrics and determine target profile
        const { metrics, profile: targetProfile } = await this.calculateScaleProfile();
        
        // Get current profile
        const currentProfile = await this.getCurrentProfile();
        
        console.log(`📊 Metrics: ${metrics.totalMessages} msgs, ${metrics.publishRate.toFixed(1)} msg/s`);
        console.log(`🎯 Current: ${currentProfile} → Target: ${targetProfile}`);
        
        // Get stability state
        const { stableProfile, stableSince } = await this.getStabilityState();
        const now = Math.floor(Date.now() / 1000);
        const stableDurationSeconds = stableProfile === targetProfile ? now - stableSince : 0;
        const isStable = stableProfile === targetProfile;
        
        // Update stability tracking if target changed
        if (stableProfile !== targetProfile) {
            await this.updateStabilityTracking(targetProfile);
        }
        
        // Check if scaling should occur
        const { shouldScale, reason } = this.scalingEngine.shouldScale(
            currentProfile, 
            targetProfile, 
            isStable, 
            stableDurationSeconds
        );
        
        console.log(`⚡ Decision: ${reason}`);
        
        if (shouldScale) {
            await this.applyScaling(targetProfile);
        }
    }

    async calculateScaleProfile() {
        const overview = await this.metricsCollector.getQueueMetrics();
        const metrics = this.metricsCollector.extractMetrics(overview);
        const targetProfile = this.scalingEngine.determineTargetProfile(metrics);
        
        return { metrics, profile: targetProfile };
    }

    async getCurrentProfile() {
        try {
            const cluster = await this.k8sClient.getRabbitMQCluster();
            const currentCpu = cluster.spec?.resources?.requests?.cpu || '0';
            return this.scalingEngine.cpuToProfileMap[currentCpu] || 'UNKNOWN';
        } catch (error) {
            console.error('Error getting current profile:', error.message);
            return 'UNKNOWN';
        }
    }

    async getStabilityState() {
        try {
            const configMap = await this.k8sClient.getConfigMap();
            return {
                stableProfile: configMap?.data?.stable_profile || '',
                stableSince: parseInt(configMap?.data?.stable_since || '0')
            };
        } catch (error) {
            console.error('Error getting stability state:', error.message);
            return { stableProfile: '', stableSince: 0 };
        }
    }

    async updateStabilityTracking(profile) {
        const currentTime = Math.floor(Date.now() / 1000);
        try {
            await this.k8sClient.updateConfigMap({
                stable_profile: profile,
                stable_since: currentTime.toString()
            });
        } catch (error) {
            console.error('Error updating stability tracking:', error.message);
        }
    }

    async applyScaling(targetProfile) {
        const resources = this.config.profiles[targetProfile];
        
        console.log(`🔄 Scaling to ${targetProfile}: CPU=${resources.cpu}, Memory=${resources.memory}`);
        
        try {
            await this.k8sClient.updateRabbitMQResources(
                resources.cpu, 
                resources.memory, 
                this.options.dryRun
            );
            
            if (!this.options.dryRun) {
                console.log('✅ Scaling completed successfully');
                this.scalingEngine.recordScaleEvent();
                await this.updateStabilityTracking(targetProfile);
            }
        } catch (error) {
            console.error('❌ Scaling failed:', error.message);
            throw error;
        }
    }

    async sleep(ms) {
        return new Promise(resolve => setTimeout(resolve, ms));
    }

    // Health check method for monitoring
    async healthCheck() {
        try {
            await this.metricsCollector.getQueueMetrics();
            await this.k8sClient.getRabbitMQCluster();
            return { status: 'healthy', timestamp: new Date().toISOString() };
        } catch (error) {
            return { 
                status: 'unhealthy', 
                error: error.message, 
                timestamp: new Date().toISOString() 
            };
        }
    }
}