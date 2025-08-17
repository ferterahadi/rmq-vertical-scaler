const express = require('express');
const amqp = require('amqplib');

const app = express();
app.use(express.json());

const RABBITMQ_URL = process.env.RABBITMQ_URL || 'amqp://guest:guest@localhost:5672';
const QUEUE_NAME = process.env.QUEUE_NAME || 'test-queue';
const PORT = process.env.PORT || 3000;

let connection = null;
let channel = null;

async function connectRabbitMQ() {
    try {
        connection = await amqp.connect(RABBITMQ_URL);
        channel = await connection.createChannel();
        await channel.assertQueue(QUEUE_NAME, { durable: true });
        console.log(`Connected to RabbitMQ at ${RABBITMQ_URL}`);
    } catch (error) {
        console.error('Failed to connect to RabbitMQ:', error);
        setTimeout(connectRabbitMQ, 5000);
    }
}

app.post('/produce/:count', async (req, res) => {
    const count = parseInt(req.params.count) || 1;
    
    if (!channel) {
        return res.status(503).json({ error: 'RabbitMQ not connected' });
    }
    
    try {
        const messages = [];
        for (let i = 0; i < count; i++) {
            const message = {
                id: Date.now() + i,
                timestamp: new Date().toISOString(),
                data: `Test message ${i + 1} of ${count}`,
                random: Math.random()
            };
            
            const sent = channel.sendToQueue(
                QUEUE_NAME,
                Buffer.from(JSON.stringify(message)),
                { persistent: true }
            );
            
            if (sent) {
                messages.push(message);
            }
        }
        
        res.json({
            success: true,
            count: messages.length,
            queue: QUEUE_NAME,
            messages: messages
        });
    } catch (error) {
        res.status(500).json({ error: error.message });
    }
});

app.delete('/consume/:count', async (req, res) => {
    const count = parseInt(req.params.count) || 1;
    
    if (!channel) {
        return res.status(503).json({ error: 'RabbitMQ not connected' });
    }
    
    try {
        const messages = [];
        let consumed = 0;
        
        for (let i = 0; i < count; i++) {
            const msg = await channel.get(QUEUE_NAME, { noAck: false });
            
            if (msg) {
                const content = JSON.parse(msg.content.toString());
                messages.push(content);
                channel.ack(msg);
                consumed++;
            } else {
                break;
            }
        }
        
        res.json({
            success: true,
            requested: count,
            consumed: consumed,
            queue: QUEUE_NAME,
            messages: messages
        });
    } catch (error) {
        res.status(500).json({ error: error.message });
    }
});

app.get('/health', (req, res) => {
    res.json({
        status: 'healthy',
        rabbitmq: connection && channel ? 'connected' : 'disconnected',
        queue: QUEUE_NAME
    });
});

app.get('/queue-info', async (req, res) => {
    if (!channel) {
        return res.status(503).json({ error: 'RabbitMQ not connected' });
    }
    
    try {
        const queueInfo = await channel.checkQueue(QUEUE_NAME);
        res.json({
            queue: QUEUE_NAME,
            messageCount: queueInfo.messageCount,
            consumerCount: queueInfo.consumerCount
        });
    } catch (error) {
        res.status(500).json({ error: error.message });
    }
});

async function gracefulShutdown() {
    console.log('Shutting down gracefully...');
    if (channel) await channel.close();
    if (connection) await connection.close();
    process.exit(0);
}

process.on('SIGTERM', gracefulShutdown);
process.on('SIGINT', gracefulShutdown);

connectRabbitMQ().then(() => {
    app.listen(PORT, () => {
        console.log(`RabbitMQ test app listening on port ${PORT}`);
        console.log(`Endpoints:`);
        console.log(`  POST /produce/:count - Produce X messages`);
        console.log(`  DELETE /consume/:count - Consume/delete X messages`);
        console.log(`  GET /health - Health check`);
        console.log(`  GET /queue-info - Queue information`);
    });
});