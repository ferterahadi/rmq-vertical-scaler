# RabbitMQ Vertical Scaler

[![Docker Image](https://img.shields.io/docker/v/ferterahadi/rmq-vertical-scaler?label=docker)](https://hub.docker.com/r/ferterahadi/rmq-vertical-scaler)

Automatically scales RabbitMQ cluster resources (CPU/Memory) based on real-time queue metrics and message rates in Kubernetes.

> ⚠️ **Important**: This scaler is recommended only for **quorum queues with 3+ nodes**. Using it on single-node RabbitMQ deployments may result in **message loss** during scaling operations.

## 🚀 Features

- **🎯 Auto Scaling**: Adjusts resources based on queue depth and message rates
- **⚡ Debounced**: Prevents oscillation with configurable delays
- **🔧 Configurable**: Environment variables, config files, and CLI options
- **🐳 Cloud Native**: Kubernetes-first with built-in deployment tools
- **🛡️ Production Hardened**: Comprehensive error handling and logging

## 📋 Table of Contents

- [Quick Start](#-quick-start)
- [Configuration](#️-configuration)
- [Deployment](#-deployment)
- [Monitoring](#-monitoring)
- [Architecture](#️-architecture)

## ⚡ Quick Start

```bash
# Using configuration file (recommended)
npx rmq-vertical-scaler generate \
  --config examples/production-config.json \
  --output my-scaler.yaml

# Deploy to your cluster
kubectl apply -f my-scaler.yaml
```

## ⚙️ Configuration

The scaler supports two configuration methods:

### Configuration File (Recommended)

```bash
# Use pre-built templates
npx rmq-vertical-scaler generate --config examples/basic-config.json
npx rmq-vertical-scaler generate --config examples/production-config.json

# Create custom configuration
curl -o my-config.json https://raw.githubusercontent.com/ferterahadi/rmq-vertical-scaler/master/examples/template-config.json
npx rmq-vertical-scaler generate --config my-config.json --output my-scaler.yaml
```

**JSON Schema Support**: Configuration files include schema annotations for IDE autocomplete, validation, and documentation.

**Basic Configuration** (`examples/basic-config.json`):
```json
{
  "$schema": "../schema/config-schema.json",
  "profiles": {
    "LOW": { "cpu": "330m", "memory": "2Gi" },
    "MEDIUM": { "cpu": "800m", "memory": "3Gi", "queue": 2000, "rate": 200 },
    "HIGH": { "cpu": "1600m", "memory": "4Gi", "queue": 10000, "rate": 1000 },
    "CRITICAL": { "cpu": "2400m", "memory": "8Gi", "queue": 50000, "rate": 2000 }
  },
  "debounce": { "scaleUpSeconds": 30, "scaleDownSeconds": 120 },
  "checkInterval": 5,
  "rmq": {
    "host": "rabbitmq.default.svc.cluster.local",
    "port": "15672"
  },
  "kubernetes": {
    "namespace": "default",
    "rmqServiceName": "rabbitmq"
  }
}
```

**Production Configuration** (`examples/production-config.json`):
- Higher resource limits: MINIMAL (500m/4Gi) → MAXIMUM (4000m/32Gi)
- Conservative scaling: Longer debounce times (60s up, 300s down)
- Higher thresholds: Queue depths from 5K to 100K messages

### CLI Options

For simple setups, use CLI options:
```bash
npx rmq-vertical-scaler generate --help
```

### RabbitMQ Credentials

The scaler requires access to RabbitMQ's management API. Credentials must be stored in a Kubernetes secret named `{service-name}-default-user`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: rabbitmq-default-user  # Format: {serviceName}-default-user
  namespace: production
data:
  username: <base64-encoded-username>
  password: <base64-encoded-password>
```

This secret is automatically created by the RabbitMQ Cluster Operator. For custom deployments, create it manually.

## 🚢 Deployment

```bash
# Generate and deploy
npx rmq-vertical-scaler generate \
  --config examples/production-config.json \
  --output production-scaler.yaml

kubectl apply -f production-scaler.yaml

# Monitor deployment
kubectl get deployment rmq-vertical-scaler -n production
kubectl logs -f deployment/rmq-vertical-scaler -n production
```


## 🏗️ Architecture

### Component Overview

```
┌─────────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   MetricsCollector  │────│  ScalingEngine   │────│ KubernetesClient│
│                     │    │                  │    │                 │
│ • RabbitMQ API      │    │ • Profile Logic  │    │ • Cluster API   │
│ • Queue Metrics     │    │ • Thresholds     │    │ • ConfigMaps    │
│ • Rate Calculation  │    │ • Debouncing     │    │ • RBAC          │
└─────────────────────┘    └──────────────────┘    └─────────────────┘
         │                           │                         │
         └───────────────────────────┼─────────────────────────┘
                                     │
                  ┌─────────────────────────────────────┐
                  │    RabbitMQVerticalScaler           │
                  │                                     │
                  │ • Orchestration                     │
                  │ • Configuration Management          │
                  │ • Error Handling                    │
                  │ • Stability Tracking                │
                  └─────────────────────────────────────┘
```

### Scaling Logic

1. **Metrics Collection**: Fetch queue depth and message rates from RabbitMQ API
2. **Profile Determination**: Compare metrics against configured thresholds
3. **Stability Check**: Ensure target profile is stable for required duration
4. **Debouncing**: Apply scale-up/scale-down delays to prevent oscillation
5. **Resource Update**: Patch RabbitMQ cluster resource specifications

### Development

```bash
git clone https://github.com/ferterahadi/rmq-vertical-scaler.git
cd rmq-vertical-scaler
npm install
npm test
```

### Project Structure

```
rmq-vertical-scaler/
├── lib/                    # Core library modules
│   ├── RabbitMQVerticalScaler.js
│   ├── MetricsCollector.js
│   ├── ScalingEngine.js
│   ├── KubernetesClient.js
│   ├── ConfigManager.js
│   └── index.js            # Main entry point for Docker
├── bin/                    # CLI executable for manifest generation
├── examples/               # Configuration templates
│   ├── basic-config.json
│   ├── production-config.json
│   └── template-config.json
├── schema/                 # JSON Schema for configuration validation
│   └── config-schema.json
├── tests/                  # Test suites
└── scripts/                # Build and utility scripts
```

## 🏆 Acknowledgments
- [RabbitMQ Cluster Operator](https://github.com/rabbitmq/cluster-operator) for Kubernetes integration
- [Kubernetes JavaScript Client](https://github.com/kubernetes-client/javascript) for API access
- The RabbitMQ and Kubernetes communities for inspiration and best practices

