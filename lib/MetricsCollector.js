/* eslint-disable prettier/prettier */
import axios from 'axios';

class MetricsCollector {
    constructor(configManager) {
        this.configManager = configManager;
    }

    async getQueueMetrics() {
        try {
            console.log('✅ Fetching overview metrics from RabbitMQ API...');
            const response = await axios.get(`http://${this.configManager.rmqHost}:${this.configManager.rmqPort}/api/overview`, {
                auth: {
                    username: this.configManager.rmqUser,
                    password: this.configManager.rmqPass
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
            const response = await axios.get(`http://${this.configManager.rmqHost}:${this.configManager.rmqPort}/api/queues`, {
                auth: {
                    username: this.configManager.rmqUser,
                    password: this.configManager.rmqPass
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

    async waitForRabbitMQ() {
        console.log('⏳ Waiting for RabbitMQ to be ready...');
        let attempts = 0;
        while (true) {
            attempts++;
            try {
                await axios.get(`http://${this.configManager.rmqHost}:${this.configManager.rmqPort}/api/overview`, {
                    auth: {
                        username: this.configManager.rmqUser,
                        password: this.configManager.rmqPass
                    },
                    timeout: 5000
                });
                console.log('✅ RabbitMQ is ready');
                return true;
            } catch (error) {
                if (attempts > 10) {
                    console.error(`❌ Failed to connect to RabbitMQ after ${attempts} attempts:`, error.message);
                } else {
                    console.log(`⏳ Waiting for RabbitMQ... (attempt ${attempts})`);
                }
                await new Promise(resolve => setTimeout(resolve, 5000));
            }
        }
    }
}

export default MetricsCollector;