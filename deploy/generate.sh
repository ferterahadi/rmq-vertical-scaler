#!/bin/bash

# Script to generate RabbitMQ Vertical Scaler Kubernetes configuration
# Usage: ./generate-scaler.sh

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}RabbitMQ Vertical Scaler Configuration Generator${NC}"
echo "=================================================="

# Prompt for scaler name
read -p "Enter the scaler name (e.g., 'rmq'): " SCALER_NAME
if [ -z "$SCALER_NAME" ]; then
    echo -e "${RED}Error: Scaler name cannot be empty${NC}"
    exit 1
fi

# Prompt for suffix name
read -p "Enter suffix name (default is 'vertical-scaler'): " SUFFIX
if [ -z "$SUFFIX" ]; then
    SUFFIX="vertical-scaler"
fi

# Prompt for namespace
read -p "Enter the namespace (e.g., 'prod'): " NAMESPACE
if [ -z "$NAMESPACE" ]; then
    echo -e "${RED}Error: Namespace cannot be empty${NC}"
    exit 1
fi

# Prompt for RabbitMQ host
echo ""
echo -e "${YELLOW}RabbitMQ Connection Configuration:${NC}"
read -p "Enter RabbitMQ service name (default: $SCALER_NAME): " RMQ_SERVICE_NAME
RMQ_SERVICE_NAME="${RMQ_SERVICE_NAME:-$SCALER_NAME}"

# Construct full hostname
RMQ_HOST="${RMQ_SERVICE_NAME}.${NAMESPACE}.svc.cluster.local"
echo -e "${GREEN}Using RabbitMQ host: $RMQ_HOST${NC}"

# RabbitMQ management port
read -p "Enter RabbitMQ management port (default: 15672): " RMQ_PORT
RMQ_PORT="${RMQ_PORT:-15672}"

# Set predefined profiles
PROFILE_NAMES=("LOW" "MEDIUM" "HIGH" "CRITICAL")
NUM_PROFILES=4

echo ""
echo -e "${YELLOW}=== Scaling Profile Configuration ===${NC}"
echo "Using 4 predefined profiles: LOW, MEDIUM, HIGH, CRITICAL"

# Collect profile configurations
# Using regular arrays indexed by profile position
declare -a PROFILES_CPU_VALUES
declare -a PROFILES_MEMORY_VALUES
declare -a QUEUE_THRESHOLD_VALUES
declare -a RATE_THRESHOLD_VALUES

# Use default values for all profiles
echo ""
echo -e "${YELLOW}Using default profile configurations:${NC}"
echo "  LOW:      CPU=330m,  Memory=2Gi"
echo "  MEDIUM:   CPU=800m,  Memory=3Gi  (triggers at: 2000 queue depth, 200 msg/s)"
echo "  HIGH:     CPU=1600m, Memory=4Gi  (triggers at: 10000 queue depth, 1000 msg/s)"
echo "  CRITICAL: CPU=2400m, Memory=8Gi  (triggers at: 50000 queue depth, 2000 msg/s)"
echo ""
echo "You can customize these values later in the generated YAML file."

# Set default values
PROFILES_CPU_VALUES[0]="330m"
PROFILES_MEMORY_VALUES[0]="2Gi"

PROFILES_CPU_VALUES[1]="800m"
PROFILES_MEMORY_VALUES[1]="3Gi"
QUEUE_THRESHOLD_VALUES[1]="2000"
RATE_THRESHOLD_VALUES[1]="200"

PROFILES_CPU_VALUES[2]="1600m"
PROFILES_MEMORY_VALUES[2]="4Gi"
QUEUE_THRESHOLD_VALUES[2]="10000"
RATE_THRESHOLD_VALUES[2]="1000"

PROFILES_CPU_VALUES[3]="2400m"
PROFILES_MEMORY_VALUES[3]="8Gi"
QUEUE_THRESHOLD_VALUES[3]="50000"
RATE_THRESHOLD_VALUES[3]="2000"

# Default debounce settings
SCALE_UP_DEBOUNCE="30"
SCALE_DOWN_DEBOUNCE="120"
CHECK_INTERVAL="5"

# Generate resource names
SERVICE_ACCOUNT="${SCALER_NAME}-${SUFFIX}-sa"
ROLE="${SCALER_NAME}-${SUFFIX}-role"
ROLE_BINDING="${SCALER_NAME}-${SUFFIX}-binding"
PDB="${SCALER_NAME}-pdb"
DEPLOYMENT="${SCALER_NAME}-${SUFFIX}"
CONFIG_MAP="${SCALER_NAME}-${SUFFIX}-config"

# Output file name
OUTPUT_FILE="${SCALER_NAME}-scaler.yaml"

echo ""
echo -e "${YELLOW}Generated resource names:${NC}"
echo "  Service Account: $SERVICE_ACCOUNT"
echo "  Role: $ROLE"
echo "  Role Binding: $ROLE_BINDING"
echo "  PodDisruptionBudget: $PDB"
echo "  ConfigMap: $CONFIG_MAP"
echo "  Deployment: $DEPLOYMENT"
echo ""
echo -e "${YELLOW}Output file: $OUTPUT_FILE${NC}"
echo ""

# Confirm generation
read -p "Generate configuration? (y/N): " CONFIRM
if [[ ! "$CONFIRM" =~ ^[Yy]$ ]]; then
    echo "Cancelled."
    exit 0
fi

# Generate the YAML configuration
cat > "$OUTPUT_FILE" << EOF
---
# ServiceAccount for the scaler
apiVersion: v1
kind: ServiceAccount
metadata:
  name: $SERVICE_ACCOUNT
  namespace: $NAMESPACE
---
# Role for vertical scaling operations
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: $ROLE
  namespace: $NAMESPACE
rules:
  - apiGroups: ['rabbitmq.com']
    resources: ['rabbitmqclusters']
    verbs: ['get', 'patch', 'update']
  - apiGroups: ['']
    resources: ['secrets']
    verbs: ['get']
  - apiGroups: ['']
    resources: ['configmaps']
    verbs: ['get', 'create', 'update', 'patch']
---
# RoleBinding
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: $ROLE_BINDING
  namespace: $NAMESPACE
subjects:
  - kind: ServiceAccount
    name: $SERVICE_ACCOUNT
    namespace: $NAMESPACE
roleRef:
  kind: Role
  name: $ROLE
  apiGroup: rbac.authorization.k8s.io
---
# ConfigMap for scaler state tracking
apiVersion: v1
kind: ConfigMap
metadata:
  name: $CONFIG_MAP
  namespace: $NAMESPACE
data:
  stable_profile: ""
  stable_since: "0"
---
# PodDisruptionBudget to ensure only 1 pod scaling at a time
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: $PDB
  namespace: $NAMESPACE
spec:
  minAvailable: 2
  selector:
    matchLabels:
      app.kubernetes.io/name: $RMQ_SERVICE_NAME
---
# Deployment for the vertical scaler
apiVersion: apps/v1
kind: Deployment
metadata:
  name: $DEPLOYMENT
  namespace: $NAMESPACE
spec:
  replicas: 1
  selector:
    matchLabels:
      app: $DEPLOYMENT
  template:
    metadata:
      labels:
        app: $DEPLOYMENT
    spec:
      serviceAccountName: $SERVICE_ACCOUNT
      containers:
        - name: scaler
          image: ferterahadi/rabbitmq-scaler:latest
          imagePullPolicy: Always
          resources:
            requests:
              cpu: 75m
              memory: 128Mi
            limits:
              cpu: 200m
              memory: 512Mi
          env:
            # RabbitMQ credentials from secret
            - name: RMQ_USER
              valueFrom:
                secretKeyRef:
                  name: $RMQ_SERVICE_NAME-default-user
                  key: username
            - name: RMQ_PASS
              valueFrom:
                secretKeyRef:
                  name: $RMQ_SERVICE_NAME-default-user
                  key: password
            # RabbitMQ connection settings
            - name: RMQ_HOST
              value: '$RMQ_HOST'
            - name: RMQ_PORT
              value: '$RMQ_PORT'
            - name: RMQ_SERVICE_NAME
              value: '$RMQ_SERVICE_NAME'
            # Dynamic resource names
            - name: CONFIG_MAP
              value: '$CONFIG_MAP'
            # Profile configuration
            - name: PROFILE_COUNT
              value: '$NUM_PROFILES'
            - name: PROFILE_NAMES
              value: '${PROFILE_NAMES[*]}'
EOF

# Generate threshold and resource environment variables dynamically
for i in "${!PROFILE_NAMES[@]}"; do
    profile="${PROFILE_NAMES[$i]}"
    
    # Resource profiles
    cat >> "$OUTPUT_FILE" << EOF
            - name: PROFILE_${profile}_CPU
              value: '${PROFILES_CPU_VALUES[$i]}'
            - name: PROFILE_${profile}_MEMORY
              value: '${PROFILES_MEMORY_VALUES[$i]}'
EOF
    
    # Thresholds (only for non-first profiles)
    if [ "$i" -gt 0 ]; then
        cat >> "$OUTPUT_FILE" << EOF
            - name: QUEUE_THRESHOLD_${profile}
              value: '${QUEUE_THRESHOLD_VALUES[$i]}'
            - name: RATE_THRESHOLD_${profile}
              value: '${RATE_THRESHOLD_VALUES[$i]}'
EOF
    fi
done

# Continue with remaining env vars
cat >> "$OUTPUT_FILE" << EOF
            # Debounce settings
            - name: DEBOUNCE_SCALE_UP_SECONDS
              value: '$SCALE_UP_DEBOUNCE'
            - name: DEBOUNCE_SCALE_DOWN_SECONDS
              value: '$SCALE_DOWN_DEBOUNCE'
            # Check interval
            - name: CHECK_INTERVAL_SECONDS
              value: '$CHECK_INTERVAL'
      nodeSelector:
        cloud.google.com/gke-spot: 'true'
      restartPolicy: Always
EOF

echo -e "${GREEN}✓ Configuration generated successfully!${NC}"
echo -e "${GREEN}✓ File saved as: $OUTPUT_FILE${NC}"
echo ""
echo -e "${YELLOW}Next steps:${NC}"
echo "1. Review the generated configuration"
echo "2. Update the image registry URL in the deployment (or use UPDATE_DEPLOYMENT=true with build.sh)"
echo "3. Customize profile resources and thresholds in the environment variables section"
echo "4. Apply the configuration: kubectl apply -f $OUTPUT_FILE"
echo ""
echo -e "${GREEN}✓ ConfigMap pre-created for state tracking (eliminates runtime creation delays)${NC}"
echo ""
echo -e "${YELLOW}To customize profiles, edit these environment variables in $OUTPUT_FILE:${NC}"
echo "  - PROFILE_*_CPU/MEMORY: Resource requests for each profile"
echo "  - QUEUE_THRESHOLD_*: Queue depth to trigger each profile"
echo "  - RATE_THRESHOLD_*: Message rate to trigger each profile"
echo "  - DEBOUNCE_SCALE_*_SECONDS: Time before scaling up/down" 