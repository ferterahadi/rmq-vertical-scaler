import { describe, it, before } from 'node:test';
import assert from 'node:assert';
import ScalingEngine from '../../lib/ScalingEngine.js';

describe('ScalingEngine', () => {
  let engine;
  
  const testConfig = {
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
    },
    profileNames: ['LOW', 'MEDIUM', 'HIGH', 'CRITICAL'],
    debounce: {
      scaleUpSeconds: 30,
      scaleDownSeconds: 120
    }
  };

  before(() => {
    engine = new ScalingEngine(testConfig);
  });

  describe('createCpuToProfileMap', () => {
    it('should create correct CPU to profile mapping', () => {
      const expectedMapping = {
        '330m': 'LOW',
        '800m': 'MEDIUM',
        '1600m': 'HIGH',
        '2400m': 'CRITICAL'
      };

      assert.deepStrictEqual(engine.cpuToProfileMap, expectedMapping);
    });
  });

  describe('determineTargetProfile', () => {
    it('should return LOW profile for minimal metrics', () => {
      const metrics = {
        totalMessages: 100,
        publishRate: 10
      };

      const profile = engine.determineTargetProfile(metrics);
      assert.strictEqual(profile, 'LOW');
    });

    it('should return MEDIUM profile when queue threshold is exceeded', () => {
      const metrics = {
        totalMessages: 2500,
        publishRate: 50
      };

      const profile = engine.determineTargetProfile(metrics);
      assert.strictEqual(profile, 'MEDIUM');
    });

    it('should return MEDIUM profile when rate threshold is exceeded', () => {
      const metrics = {
        totalMessages: 1000,
        publishRate: 250
      };

      const profile = engine.determineTargetProfile(metrics);
      assert.strictEqual(profile, 'MEDIUM');
    });

    it('should return HIGH profile for high queue depth', () => {
      const metrics = {
        totalMessages: 15000,
        publishRate: 100
      };

      const profile = engine.determineTargetProfile(metrics);
      assert.strictEqual(profile, 'HIGH');
    });

    it('should return CRITICAL profile for critical metrics', () => {
      const metrics = {
        totalMessages: 75000,
        publishRate: 2500
      };

      const profile = engine.determineTargetProfile(metrics);
      assert.strictEqual(profile, 'CRITICAL');
    });

    it('should choose highest applicable profile', () => {
      const metrics = {
        totalMessages: 75000, // Triggers CRITICAL
        publishRate: 1500     // Triggers HIGH
      };

      const profile = engine.determineTargetProfile(metrics);
      assert.strictEqual(profile, 'CRITICAL');
    });
  });

  describe('shouldScale', () => {
    it('should not scale when already at target profile', () => {
      const result = engine.shouldScale('MEDIUM', 'MEDIUM', true, 60);
      
      assert.strictEqual(result.shouldScale, false);
      assert.strictEqual(result.reason, 'Already at target profile');
    });

    it('should not scale when not stable long enough for scale up', () => {
      const result = engine.shouldScale('LOW', 'MEDIUM', true, 15);
      
      assert.strictEqual(result.shouldScale, false);
      assert.ok(result.reason.includes('Waiting for stability'));
    });

    it('should not scale when not stable long enough for scale down', () => {
      const result = engine.shouldScale('HIGH', 'MEDIUM', true, 60);
      
      assert.strictEqual(result.shouldScale, false);
      assert.ok(result.reason.includes('Waiting for stability'));
    });

    it('should not scale when profile is not stable', () => {
      const result = engine.shouldScale('LOW', 'MEDIUM', false, 60);
      
      assert.strictEqual(result.shouldScale, false);
      assert.ok(result.reason.includes('Waiting for stability'));
    });

    it('should scale up when conditions are met', () => {
      const result = engine.shouldScale('LOW', 'MEDIUM', true, 35);
      
      assert.strictEqual(result.shouldScale, true);
      assert.strictEqual(result.reason, 'Scaling up due to high load');
    });

    it('should scale down when conditions are met', () => {
      const result = engine.shouldScale('HIGH', 'MEDIUM', true, 125);
      
      assert.strictEqual(result.shouldScale, true);
      assert.strictEqual(result.reason, 'Scaling down due to low load');
    });
  });

  describe('recordScaleEvent', () => {
    it('should update last scale time', () => {
      const beforeTime = Date.now();
      engine.recordScaleEvent();
      const afterTime = Date.now();

      assert.ok(engine.lastScaleTime >= beforeTime);
      assert.ok(engine.lastScaleTime <= afterTime);
    });
  });
});