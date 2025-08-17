#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}🚀 RabbitMQ Vertical Scaler Testing Script${NC}"
echo "=========================================="

# Function to get RabbitMQ credentials
get_rabbitmq_creds() {
    RMQ_USER=$(kubectl get secret rabbitmq-default-user -o jsonpath='{.data.username}' | base64 -d)
    RMQ_PASS=$(kubectl get secret rabbitmq-default-user -o jsonpath='{.data.password}' | base64 -d)
    echo -e "${GREEN}✓ Got RabbitMQ credentials: ${RMQ_USER}${NC}"
}

# Function to check current resource allocation
check_resources() {
    echo -e "\n${YELLOW}📊 Current RabbitMQ Resource Allocation:${NC}"
    kubectl get rabbitmqclusters rabbitmq -o jsonpath='{.spec.resources}' | jq '.'
}

# Function to check queue stats
check_queue_stats() {
    echo -e "\n${YELLOW}📈 RabbitMQ Queue Statistics:${NC}"
    kubectl port-forward svc/rabbitmq 15672:15672 &
    PORT_FORWARD_PID=$!
    sleep 3
    
    curl -s -u "${RMQ_USER}:${RMQ_PASS}" \
        "http://localhost:15672/api/overview" | \
        jq '.queue_totals, .message_stats' 2>/dev/null || echo "API not ready yet"
    
    kill $PORT_FORWARD_PID 2>/dev/null || true
}

# Function to create test queue and publish messages
create_load() {
    local message_count=${1:-1000}
    echo -e "\n${BLUE}📦 Creating load: ${message_count} messages${NC}"
    
    # Create a simple producer pod
    kubectl run rabbitmq-producer --image=rabbitmq:3-management --rm -i --restart=Never -- \
        bash -c "
        export RABBITMQ_DEFAULT_USER='${RMQ_USER}'
        export RABBITMQ_DEFAULT_PASS='${RMQ_PASS}'
        
        # Install python and pika
        apt-get update -qq && apt-get install -y python3 python3-pip -qq
        pip3 install pika --quiet
        
        # Python script to publish messages
        python3 -c \"
import pika
import sys
import time

# Connect to RabbitMQ
connection = pika.BlockingConnection(
    pika.ConnectionParameters(
        host='rabbitmq.default.svc.cluster.local',
        credentials=pika.PlainCredentials('${RMQ_USER}', '${RMQ_PASS}')
    )
)
channel = connection.channel()

# Declare queue
queue_name = 'test-scaling-queue'
channel.queue_declare(queue=queue_name, durable=True)

# Publish messages
for i in range(${message_count}):
    message = f'Test message {i+1} - {time.time()}'
    channel.basic_publish(
        exchange='',
        routing_key=queue_name,
        body=message,
        properties=pika.BasicProperties(delivery_mode=2)  # Make message persistent
    )
    if (i + 1) % 100 == 0:
        print(f'Published {i+1} messages')

print(f'✅ Successfully published ${message_count} messages')
connection.close()
\"
        "
}

# Function to monitor scaler logs
monitor_scaler() {
    echo -e "\n${YELLOW}📊 Monitoring Scaler Activity:${NC}"
    echo "Press Ctrl+C to stop monitoring..."
    kubectl logs -f -l app=rmq-vertical-scaler --tail=10
}

# Function to show cluster status
show_status() {
    echo -e "\n${YELLOW}🔍 Cluster Status:${NC}"
    echo "RabbitMQ Cluster:"
    kubectl get rabbitmqclusters
    echo -e "\nRabbitMQ Pods:"
    kubectl get pods -l app.kubernetes.io/name=rabbitmq
    echo -e "\nScaler Pod:"
    kubectl get pods -l app=rmq-vertical-scaler
}

# Function to cleanup
cleanup() {
    echo -e "\n${RED}🧹 Cleaning up test resources...${NC}"
    kubectl delete pod rabbitmq-producer --ignore-not-found=true
    echo -e "${GREEN}✓ Cleanup complete${NC}"
}

# Main menu
case "${1:-menu}" in
    "creds")
        get_rabbitmq_creds
        ;;
    "resources")
        check_resources
        ;;
    "queue-stats")
        get_rabbitmq_creds
        check_queue_stats
        ;;
    "load-light")
        get_rabbitmq_creds
        create_load 500
        ;;
    "load-medium")
        get_rabbitmq_creds
        create_load 2500
        ;;
    "load-heavy")
        get_rabbitmq_creds
        create_load 15000
        ;;
    "load-critical")
        get_rabbitmq_creds
        create_load 60000
        ;;
    "monitor")
        monitor_scaler
        ;;
    "status")
        show_status
        ;;
    "cleanup")
        cleanup
        ;;
    "full-test")
        echo -e "${BLUE}🧪 Running Full Scaling Test${NC}"
        get_rabbitmq_creds
        show_status
        check_resources
        
        echo -e "\n${YELLOW}Phase 1: Light load (should stay LOW profile)${NC}"
        create_load 500
        sleep 30
        check_resources
        
        echo -e "\n${YELLOW}Phase 2: Medium load (should scale to MEDIUM)${NC}"
        create_load 2500
        sleep 60
        check_resources
        
        echo -e "\n${YELLOW}Phase 3: Heavy load (should scale to HIGH)${NC}"
        create_load 15000
        sleep 60
        check_resources
        
        echo -e "\n${YELLOW}Phase 4: Monitor for scale down${NC}"
        echo "Wait 2-3 minutes to see scale down..."
        ;;
    *)
        echo -e "${YELLOW}Usage: $0 [command]${NC}"
        echo ""
        echo "Commands:"
        echo "  creds         - Get RabbitMQ credentials"
        echo "  resources     - Check current resource allocation"
        echo "  queue-stats   - Check queue statistics"
        echo "  load-light    - Create light load (500 msgs)"
        echo "  load-medium   - Create medium load (2500 msgs)"
        echo "  load-heavy    - Create heavy load (15000 msgs)"
        echo "  load-critical - Create critical load (60000 msgs)"
        echo "  monitor       - Monitor scaler logs"
        echo "  status        - Show cluster status"
        echo "  cleanup       - Clean up test resources"
        echo "  full-test     - Run complete scaling test"
        echo ""
        echo -e "${GREEN}💡 Tip: Start with 'status' to see everything is running${NC}"
        ;;
esac