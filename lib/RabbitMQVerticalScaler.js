/* eslint-disable prettier/prettier */
import ConfigManager from './ConfigManager.js';
import KubernetesClient from './KubernetesClient.js';
import MetricsCollector from './MetricsCollector.js';
import ScalingEngine from './ScalingEngine.js';

class RabbitMQVerticalScaler {
    constructor() {
        // Initialize all modules
        this.configManager = new ConfigManager();
        this.kubernetesClient = new KubernetesClient(this.configManager);
        this.metricsCollector = new MetricsCollector(this.configManager);
        this.scalingEngine = new ScalingEngine(
            this.configManager,
            this.kubernetesClient,
            this.metricsCollector
        );
    }

    async applyScale() {
        console.log('🔍 Analyzing RabbitMQ metrics...');
        const { metrics, profile } = await this.scalingEngine.calculateScaleProfile();

        // Display metrics summary
        console.log(`📊 Queue Depth: ${metrics.maxQueueDepth} | Total: ${metrics.totalMessages} | Publish: ${metrics.messageRate}/s | Consume: ${metrics.consumeRate}/s | Backlog: ${metrics.backlogRate}/s`);

        // Get current profile
        const currentProfile = await this.kubernetesClient.getCurrentProfile();
        console.log(`📍 Current profile: ${currentProfile}`);

        // Set resources based on profile
        let resources = this.configManager.profiles[profile];
        if (!resources) {
            console.error(`❓ Unknown profile '${profile}', defaulting to ${this.configManager.config.profileNames[0]}`);
            profile = this.configManager.config.profileNames[0];
            resources = this.configManager.profiles[profile];
        }

        // Generate appropriate message based on profile position
        const message = this.scalingEngine.generateScalingMessage(profile);
        console.log(message);

        // Always check stability to ensure timer resets when recommendation changes
        if (!(await this.scalingEngine.checkProfileStability(currentProfile, profile))) {
            console.log('⏸️  Profile not stable long enough yet');
            return;
        }

        // Skip if already at target profile
        if (currentProfile === profile) {
            console.log(`⏸️  Scaling skipped - already at ${profile} profile`);
            return;
        }

        console.log(`⚙️  Applying: CPU=${resources.cpu}, Memory=${resources.memory}`);

        try {
            await this.kubernetesClient.applyPatch(resources);
            // Reset stability tracking since we just scaled
            await this.kubernetesClient.updateStabilityTracking(profile);
        } catch (error) {
            console.error('❌ Scaling failed:', error.message);
        }
    }

    async main() {
        console.log('🚀 RabbitMQ Vertical Scaler (Node.js)');

        await this.metricsCollector.waitForRabbitMQ();

        // Run scaling loop
        while (true) {
            try {
                await this.applyScale();
                console.log('---');
            } catch (error) {
                console.error('Error in scaling loop:', error.message);
            }
            
            // Wait for configured interval (default 5 seconds)
            const intervalMs = (this.configManager.checkIntervalSeconds || 5) * 1000;
            await new Promise(resolve => setTimeout(resolve, intervalMs));
        }
    }
}

export default RabbitMQVerticalScaler;