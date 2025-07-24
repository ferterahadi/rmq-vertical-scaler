import axios from 'axios';

export default class MetricsCollector {
  constructor(config) {
    this.rmqHost = config.rmqHost;
    this.rmqPort = config.rmqPort;
    this.rmqUser = config.rmqUser;
    this.rmqPass = config.rmqPass;
    this.timeout = config.timeout || 10000;
  }

  async getQueueMetrics() {
    try {
      console.log('✅ Fetching overview metrics from RabbitMQ API...');
      const response = await axios.get(`http://${this.rmqHost}:${this.rmqPort}/api/overview`, {
        auth: {
          username: this.rmqUser,
          password: this.rmqPass
        },
        timeout: this.timeout
      });
      return response.data;
    } catch (error) {
      throw new Error(`Failed to fetch RabbitMQ metrics: ${error.message}`);
    }
  }

  extractMetrics(data) {
    const queueTotals = data.queue_totals || {};
    const messageStats = data.message_stats || {};
    
    return {
      totalMessages: queueTotals.messages || 0,
      publishRate: messageStats.publish_details?.rate || 0,
      deliverRate: messageStats.deliver_details?.rate || 0,
      ackRate: messageStats.ack_details?.rate || 0
    };
  }
}