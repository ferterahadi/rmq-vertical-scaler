#!/usr/bin/env node

// RabbitMQ Vertical Scaler - Main Entry Point
import RabbitMQVerticalScaler from './RabbitMQVerticalScaler.js';

console.log('🐰 RabbitMQ Vertical Scaler');
console.log('==============================');

const scaler = new RabbitMQVerticalScaler();

// Handle graceful shutdown
process.on('SIGTERM', () => {
    console.log('📴 Received SIGTERM, shutting down gracefully');
    scaler.stop();
    process.exit(0);
});

process.on('SIGINT', () => {
    console.log('📴 Received SIGINT, shutting down gracefully');
    scaler.stop();
    process.exit(0);
});

// Start the scaler
scaler.main().catch(error => {
    console.error('❌ Failed to start scaler:', error);
    process.exit(1);
});