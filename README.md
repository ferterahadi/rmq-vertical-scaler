# RabbitMQ Vertical Scaler

[![Docker Image](https://img.shields.io/docker/v/ferterahadi/rmq-vertical-scaler?label=docker)](https://hub.docker.com/r/ferterahadi/rmq-vertical-scaler)

Node.js application that automatically **vertically scales** RabbitMQ cluster resources (CPU/Memory) based on real-time queue metrics and message rates in Kubernetes environments.

## 🚀 Features

- **🎯 Intelligent Scaling**: Automatically adjusts RabbitMQ resources based on queue depth and message rates
- **📊 Multiple Profiles**: Supports configurable scaling profiles (LOW, MEDIUM, HIGH, CRITICAL)
- **⚡ Debounced Scaling**: Prevents oscillation with configurable scale-up/scale-down delays
- **🔧 Highly Configurable**: Environment variables, config files, and CLI options
- **🐳 Cloud Native**: Kubernetes-first design with built-in deployment tools
- **📈 Monitoring Ready**: Built-in health checks and metrics endpoints
- **🛡️ Production Hardened**: Comprehensive error handling and logging
- **📦 Easy Installation**: Docker image ready to use

## 📋 Table of Contents

- [Quick Start](#-quick-start)
- [Configuration](#️-configuration)
- [Deployment](#-deployment)
- [Monitoring](#-monitoring)
- [Architecture](#️-architecture)

## ⚡ Quick Start

```bash
# Option 1: Use configuration file (recommended)
npx rmq-vertical-scaler generate \
  --config examples/production-config.json \
  --output my-scaler.yaml

# Option 2: Use CLI options
npx rmq-vertical-scaler generate \
  --namespace production \
  --service-name my-rabbitmq \
  --output my-scaler.yaml

# Deploy to your cluster
kubectl apply -f my-scaler.yaml
```

## ⚙️ Configuration

The scaler supports two configuration methods:

### Option 1: Configuration File (Recommended)

Use a JSON configuration file for complex setups:

```bash
# Use pre-built configuration templates
npx rmq-vertical-scaler generate --config examples/basic-config.json
npx rmq-vertical-scaler generate --config examples/production-config.json

# Or download the template to create your own
curl -o my-config.json https://raw.githubusercontent.com/ferterahadi/rmq-vertical-scaler/master/examples/template-config.json
```

**📋 Schema Validation**: All config files include JSON Schema annotations for IDE support:
- **Autocomplete**: Get suggestions for valid configuration options
- **Validation**: Real-time error checking and type validation  
- **Documentation**: Hover over properties to see descriptions and examples
- **IntelliSense**: Modern editors like VS Code provide rich editing experience

To get started with a custom configuration:
```bash
# Download the template file
curl -o my-config.json https://raw.githubusercontent.com/ferterahadi/rmq-vertical-scaler/master/examples/template-config.json

# Alternative: using wget
# wget -O my-config.json https://raw.githubusercontent.com/ferterahadi/rmq-vertical-scaler/master/examples/template-config.json

# Edit in VS Code (or any JSON Schema-aware editor)
code my-config.json

# Generate manifests with your custom config
npx rmq-vertical-scaler generate --config my-config.json --output my-scaler.yaml
```

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
- Higher thresholds: Queue depths from 5K to 100K messages, message rates from 500 to 5K msg/s
- Self-contained profiles: Each profile includes CPU, memory, queue threshold, and rate threshold

### Option 2: CLI Options

For simple customizations, use CLI options directly:

```bash
npx rmq-vertical-scaler generate --help
```

CLI options override config file values when both are provided.

### RabbitMQ Credentials

The scaler requires access to RabbitMQ's management API to collect queue metrics. By default, the generated manifests assume RabbitMQ credentials are stored in a Kubernetes secret named `{service-name}-default-user`:

```yaml
# The scaler expects these secrets to exist:
apiVersion: v1
kind: Secret
metadata:
  name: rabbitmq-default-user  # Format: {serviceName}-default-user
  namespace: production
data:
  username: <base64-encoded-username>
  password: <base64-encoded-password>
```

**For RabbitMQ Cluster Operator users**: This secret is created automatically when you deploy a RabbitMQ cluster.

**For custom RabbitMQ deployments**: Create the secret manually or modify the generated manifest to reference your existing secret name.

## 🚢 Deployment


### Kubernetes Deployment

```bash
# Generate manifests using config file
npx rmq-vertical-scaler generate \
  --config examples/production-config.json \
  --output production-scaler.yaml

# Apply to your cluster
kubectl apply -f production-scaler.yaml

# Monitor the deployment
kubectl get deployment rmq-vertical-scaler -n production
kubectl logs -f deployment/rmq-vertical-scaler -n production
```

## 📊 Monitoring

Monitor the scaler deployment:

```bash
# Check deployment status
kubectl get deployment rmq-vertical-scaler -n production

# View logs and scaling decisions
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
6. **State Tracking**: Update ConfigMap with current stable profile

### Development Setup

```bash
# Clone repository
git clone https://github.com/ferterahadi/rmq-vertical-scaler.git
cd rmq-vertical-scaler

# Install dependencies
npm install

# Run tests
npm test

# Start in development mode
npm run dev
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
├── k8s-test/               # Local Kubernetes testing scripts
└── scripts/                # Build and utility scripts
```

## 🏆 Acknowledgments
- [RabbitMQ Cluster Operator](https://github.com/rabbitmq/cluster-operator) for Kubernetes integration
- [Kubernetes JavaScript Client](https://github.com/kubernetes-client/javascript) for API access
- The RabbitMQ and Kubernetes communities for inspiration and best practices

