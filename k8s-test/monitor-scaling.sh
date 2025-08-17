#!/bin/bash

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${BLUE}🔍 RabbitMQ Scaling Monitor${NC}"
echo "============================="

# Function to get current resources
get_current_resources() {
    kubectl get rabbitmqclusters rabbitmq -o jsonpath='{.spec.resources}' 2>/dev/null | jq -r '.requests.cpu + " / " + .requests.memory' 2>/dev/null || echo "N/A"
}

# Function to get queue stats
get_queue_stats() {
    local logs=$(kubectl logs -l app=rmq-vertical-scaler --tail=1 2>/dev/null | grep "📊 Metrics:" || echo "📊 Metrics: N/A")
    echo "$logs" | sed 's/📊 Metrics: //'
}

# Function to get target profile
get_target_profile() {
    local logs=$(kubectl logs -l app=rmq-vertical-scaler --tail=3 2>/dev/null | grep "🎯 Current:" || echo "🎯 Current: N/A → Target: N/A")
    echo "$logs" | sed 's/🎯 Current: //'
}

# Function to get scaler decision
get_scaler_decision() {
    local logs=$(kubectl logs -l app=rmq-vertical-scaler --tail=2 2>/dev/null | grep "⚡ Decision:" || echo "⚡ Decision: N/A")
    echo "$logs" | sed 's/⚡ Decision: //'
}

# Function to display current status
show_status() {
    clear
    echo -e "${BLUE}🔍 RabbitMQ Scaling Monitor - $(date)${NC}"
    echo "=================================================="
    
    echo -e "\n${YELLOW}📊 Current Resource Allocation:${NC}"
    echo -e "  CPU/Memory: $(get_current_resources)"
    
    echo -e "\n${CYAN}📈 Queue Metrics:${NC}"
    echo -e "  $(get_queue_stats)"
    
    echo -e "\n${GREEN}🎯 Profile Status:${NC}"
    echo -e "  $(get_target_profile)"
    
    echo -e "\n${YELLOW}⚡ Scaler Decision:${NC}"
    echo -e "  $(get_scaler_decision)"
    
    echo -e "\n${BLUE}🚀 RabbitMQ Pods:${NC}"
    kubectl get pods -l app.kubernetes.io/name=rabbitmq --no-headers | while read line; do
        echo "  $line"
    done
    
    echo -e "\n${CYAN}📊 Scaler Pod:${NC}"
    kubectl get pods -l app=rmq-vertical-scaler --no-headers | while read line; do
        echo "  $line"
    done
    
    echo -e "\n${GREEN}💡 Commands:${NC}"
    echo "  - Press Ctrl+C to stop monitoring"
    echo "  - In another terminal, run load tests:"
    echo "    ./k8s-test/test-scaling.sh load-medium"
    echo "    ./k8s-test/test-scaling.sh load-heavy"
    
    echo -e "\n${YELLOW}⏰ Next update in 5 seconds...${NC}"
}

# Main monitoring loop
trap 'echo -e "\n${GREEN}✅ Monitoring stopped${NC}"; exit 0' INT

while true; do
    show_status
    sleep 5
done