/* eslint-disable prettier/prettier */
class ScalingEngine {
    constructor(configManager, kubernetesClient, metricsCollector) {
        this.configManager = configManager;
        this.kubernetesClient = kubernetesClient;
        this.metricsCollector = metricsCollector;
    }

    async calculateScaleProfile() {
        const overview = await this.metricsCollector.getQueueMetrics();
        const queues = await this.metricsCollector.getDetailedQueues();

        // Skip processing if we got error responses
        if (Object.keys(overview).length === 0 || queues.length === 0) {
            console.warn('[WARNING] Unable to get overview and queues due to connection error, skipping scaling');
            throw new Error('Connection error: Unable to fetch metrics');
        }

        // Extract key metrics with error handling
        const totalMessages = overview.queue_totals?.messages || 0;
        const messageRate = overview.message_stats?.publish_details?.rate || 0;
        const consumeRate = overview.message_stats?.deliver_get_details?.rate || 0;
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
        let profile = this.configManager.config.profileNames[0];

        // Check thresholds from highest to lowest
        for (let i = this.configManager.config.profileNames.length - 1; i > 0; i--) {
            const profileName = this.configManager.config.profileNames[i];
            const queueThreshold = this.configManager.thresholds.queue[profileName];
            const rateThreshold = this.configManager.thresholds.rate[profileName];

            if ((queueThreshold && maxQueueDepth > queueThreshold) ||
                (rateThreshold && messageRate > rateThreshold)) {
                profile = profileName;
                break;
            }
        }

        return { metrics, profile };
    }

    getProfilePriority(profile) {
        // Priority based on position in profile names array
        const index = this.configManager.config.profileNames.indexOf(profile);
        return index >= 0 ? index + 1 : 0;
    }

    async checkProfileStability(currentProfile, recommendedProfile) {
        console.log(`🔍 Checking profile stability: current=${currentProfile}, recommended=${recommendedProfile}`);
        
        const stability = await this.kubernetesClient.getStabilityState();
        const currentTime = Math.floor(Date.now() / 1000);

        // If recommendation changed from what we're tracking, update tracking
        if (stability.stableProfile !== recommendedProfile) {
            console.log(`🔄 Profile recommendation changed from ${stability.stableProfile} to ${recommendedProfile}`);
            await this.kubernetesClient.updateStabilityTracking(recommendedProfile);
            return false;
        }

        // If already at recommended profile, update tracking to ensure timer resets if recommendation changed
        if (currentProfile === recommendedProfile) {
            console.log(`✅ Already at recommended profile: ${recommendedProfile}`);
            // Update tracking to reset timer in case recommendation oscillated
            await this.kubernetesClient.updateStabilityTracking(recommendedProfile);
            return true;
        }

        const currentPriority = this.getProfilePriority(currentProfile);
        const recommendedPriority = this.getProfilePriority(recommendedProfile);


        // Check if recommendation has been stable long enough
        const timeStable = currentTime - stability.stableSince;
        const isScaleUp = recommendedPriority > currentPriority;

        if (isScaleUp) {
            // Scale-up debounce
            if (timeStable < this.configManager.scaleUpDebounceSeconds) {
                const remaining = this.configManager.scaleUpDebounceSeconds - timeStable;
                console.log(`⏳ Scale-up debounce: ${recommendedProfile} stable for ${timeStable}s, need ${this.configManager.scaleUpDebounceSeconds}s (${remaining}s remaining)`);
                return false;
            }
        } else {
            // Scale-down debounce
            if (timeStable < this.configManager.scaleDownDebounceSeconds) {
                const remaining = this.configManager.scaleDownDebounceSeconds - timeStable;
                console.log(`⏳ Scale-down debounce: ${recommendedProfile} stable for ${timeStable}s, need ${this.configManager.scaleDownDebounceSeconds}s (${remaining}s remaining)`);
                return false;
            }
        }

        console.log('✅ Profile has been stable for required duration');
        return true;
    }

    generateScalingMessage(profile) {
        // Generate appropriate message based on profile position
        const profileIndex = this.configManager.config.profileNames.indexOf(profile);
        const profileCount = this.configManager.config.profileNames.length;
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
        
        return message;
    }
}

export default ScalingEngine;