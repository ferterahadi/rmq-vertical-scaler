export default class ScalingEngine {
  constructor(config) {
    this.profiles = config.profiles;
    this.thresholds = config.thresholds;
    this.profileNames = config.profileNames;
    this.scaleUpDebounceSeconds = config.debounce.scaleUpSeconds;
    this.scaleDownDebounceSeconds = config.debounce.scaleDownSeconds;
    
    this.cpuToProfileMap = this.createCpuToProfileMap();
    this.lastScaleDecision = null;
    this.lastScaleTime = 0;
  }

  createCpuToProfileMap() {
    const mapping = {};
    Object.entries(this.profiles).forEach(([profile, resources]) => {
      mapping[resources.cpu] = profile;
    });
    return mapping;
  }

  determineTargetProfile(metrics) {
    const { totalMessages, publishRate } = metrics;
    
    // Start from highest profile and work down
    for (let i = this.profileNames.length - 1; i >= 0; i--) {
      const profileName = this.profileNames[i];
      const queueThreshold = this.thresholds.queue[profileName];
      const rateThreshold = this.thresholds.rate[profileName];
      
      if (queueThreshold && totalMessages >= queueThreshold) {
        return profileName;
      }
      if (rateThreshold && publishRate >= rateThreshold) {
        return profileName;
      }
    }
    
    return this.profileNames[0]; // Default to lowest profile
  }

  shouldScale(currentProfile, targetProfile, isStable, stableDurationSeconds) {
    const currentIndex = this.profileNames.indexOf(currentProfile);
    const targetIndex = this.profileNames.indexOf(targetProfile);
    
    if (currentIndex === targetIndex) {
      return { shouldScale: false, reason: 'Already at target profile' };
    }
    
    const isScaleUp = targetIndex > currentIndex;
    const requiredStability = isScaleUp ? this.scaleUpDebounceSeconds : this.scaleDownDebounceSeconds;
    
    if (!isStable || stableDurationSeconds < requiredStability) {
      return { 
        shouldScale: false, 
        reason: `Waiting for stability (${stableDurationSeconds}s/${requiredStability}s)`
      };
    }
    
    const timeSinceLastScale = (Date.now() - this.lastScaleTime) / 1000;
    const cooldownPeriod = isScaleUp ? 30 : 60;
    
    if (timeSinceLastScale < cooldownPeriod) {
      return { 
        shouldScale: false, 
        reason: `Cooling down (${Math.round(cooldownPeriod - timeSinceLastScale)}s remaining)`
      };
    }
    
    return { 
      shouldScale: true, 
      reason: isScaleUp ? 'Scaling up due to high load' : 'Scaling down due to low load'
    };
  }

  recordScaleEvent() {
    this.lastScaleTime = Date.now();
  }
}