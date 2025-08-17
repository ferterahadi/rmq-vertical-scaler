# RabbitMQ Vertical Scaler

[![Docker Image](https://img.shields.io/docker/v/ferterahadi/rabbitmq-scaler?label=docker)](https://hub.docker.com/r/ferterahadi/rabbitmq-scaler)

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
- [Installation](#-installation)
- [Configuration](#-configuration)
- [Usage](#-usage)
- [Deployment](#-deployment)
- [Monitoring](#-monitoring)
- [Contributing](#-contributing)

## ⚡ Quick Start

### Using Docker

```bash
docker run --rm \
  -e RMQ_HOST=my-rabbitmq.prod.svc.cluster.local \
  -e NAMESPACE=production \
  ferterahadi/rabbitmq-scaler:latest
```

### Using Kubernetes

```bash
# 1. Clone the repository to get the manifest generator
git clone https://github.com/ferterahadi/rmq-vertical-scaler.git
cd rmq-vertical-scaler

# 2. Generate Kubernetes manifests for your environment
./bin/rmq-vertical-scaler generate \
  --namespace production \
  --service-name my-rabbitmq \
  --output my-scaler.yaml

# 3. Deploy to your cluster
kubectl apply -f my-scaler.yaml
```

## 🔧 Installation

### Prerequisites

- **Kubernetes**: ≥1.20 with RabbitMQ Cluster Operator
- **RabbitMQ**: Cluster managed by [RabbitMQ Cluster Operator](https://github.com/rabbitmq/cluster-operator)
- **Docker**: For pulling the pre-built image

### Installation

The Docker image `ferterahadi/rabbitmq-scaler:latest` is ready to use. You just need to:

1. **Clone this repository** to get the Kubernetes manifest generator
2. **Generate manifests** for your specific environment
3. **Deploy** to your cluster

```bash
git clone https://github.com/ferterahadi/rmq-vertical-scaler.git
cd rmq-vertical-scaler
```


## ⚙️ Configuration

### Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `RMQ_HOST` | RabbitMQ management API host | - | ✅ |
| `RMQ_PORT` | RabbitMQ management API port | `15672` | |
| `RMQ_USER` | RabbitMQ username | `guest` | |
| `RMQ_PASS` | RabbitMQ password | `guest` | |
| `NAMESPACE` | Kubernetes namespace | `default` | ✅ |
| `RMQ_SERVICE_NAME` | RabbitMQ service name | `rmq` | |
| `CHECK_INTERVAL_SECONDS` | Scaling check interval | `5` | |
| `DEBOUNCE_SCALE_UP_SECONDS` | Scale-up delay | `30` | |
| `DEBOUNCE_SCALE_DOWN_SECONDS` | Scale-down delay | `120` | |

### Scaling Profiles

Configure resource profiles and thresholds:

```bash
# Profile Resources
export PROFILE_LOW_CPU="330m"
export PROFILE_LOW_MEMORY="2Gi"
export PROFILE_MEDIUM_CPU="800m" 
export PROFILE_MEDIUM_MEMORY="3Gi"
export PROFILE_HIGH_CPU="1600m"
export PROFILE_HIGH_MEMORY="4Gi"
export PROFILE_CRITICAL_CPU="2400m"
export PROFILE_CRITICAL_MEMORY="8Gi"

# Scaling Thresholds
export QUEUE_THRESHOLD_MEDIUM="2000"    # Queue depth trigger
export RATE_THRESHOLD_MEDIUM="200"      # Messages/sec trigger
export QUEUE_THRESHOLD_HIGH="10000"
export RATE_THRESHOLD_HIGH="1000"
export QUEUE_THRESHOLD_CRITICAL="50000"
export RATE_THRESHOLD_CRITICAL="2000"
```

### Configuration File

Create `config.json`:

```json
{
  "profileNames": ["LOW", "MEDIUM", "HIGH", "CRITICAL"],
  "profiles": {
    "LOW": { "cpu": "330m", "memory": "2Gi" },
    "MEDIUM": { "cpu": "800m", "memory": "3Gi" },
    "HIGH": { "cpu": "1600m", "memory": "4Gi" },
    "CRITICAL": { "cpu": "2400m", "memory": "8Gi" }
  },
  "thresholds": {
    "queue": {
      "MEDIUM": 2000,
      "HIGH": 10000,
      "CRITICAL": 50000
    },
    "rate": {
      "MEDIUM": 200,
      "HIGH": 1000,
      "CRITICAL": 2000
    }
  },
  "debounce": {
    "scaleUpSeconds": 30,
    "scaleDownSeconds": 120
  },
  "checkInterval": 5
}
```

## 📖 Usage

### Manifest Generator

The repository includes a script to generate Kubernetes manifests customized for your environment:

```bash
# Clone the repository
git clone https://github.com/ferterahadi/rmq-vertical-scaler.git
cd rmq-vertical-scaler

# Generate manifests (runs locally, no Docker needed)
./bin/rmq-vertical-scaler generate \
  --namespace production \
  --service-name my-rabbitmq \
  --output my-scaler.yaml

# Options:
#   -n, --namespace     Kubernetes namespace (default: "default")
#   -s, --service-name  RabbitMQ service name (default: "rabbitmq") 
#   -o, --output        Output file name (default: "rmq-scaler.yaml")
#   --image             Docker image (default: "ferterahadi/rabbitmq-scaler:latest")
```

### Docker Usage (Advanced)

For standalone Docker usage outside Kubernetes:

```bash
docker run --rm \
  -e RMQ_HOST=rabbitmq.default.svc.cluster.local \
  -e NAMESPACE=production \
  ferterahadi/rabbitmq-scaler:latest
```

## 🚢 Deployment

### Kubernetes RBAC

The scaler requires specific permissions:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: rmq-vertical-scaler-role
rules:
  - apiGroups: ['rabbitmq.com']
    resources: ['rabbitmqclusters']
    verbs: ['get', 'patch', 'update']
  - apiGroups: ['']
    resources: ['secrets', 'configmaps']
    verbs: ['get', 'create', 'update', 'patch']
```

### Kubernetes Deployment

```bash
# Clone the repository 
git clone https://github.com/ferterahadi/rmq-vertical-scaler.git
cd rmq-vertical-scaler

# Generate manifests for your environment
./bin/rmq-vertical-scaler generate \
  --namespace production \
  --service-name my-rabbitmq \
  --output production-scaler.yaml

# Apply to your cluster
kubectl apply -f production-scaler.yaml

# Monitor the deployment
kubectl get deployment rmq-vertical-scaler -n production
kubectl logs -f deployment/rmq-vertical-scaler -n production
```

## 📊 Monitoring

### Health Endpoint

The scaler exposes a health check method:

```javascript
const health = await scaler.healthCheck();
// Returns: { status: 'healthy', timestamp: '2024-01-01T00:00:00.000Z' }
```

### Metrics and Logging

- **Structured Logging**: JSON formatted logs with timestamps
- **Scaling Events**: Detailed logs for every scaling decision
- **Error Tracking**: Comprehensive error handling and reporting
- **Performance Metrics**: Queue depth, message rates, and scaling decisions

### Prometheus Integration

Example metrics collection (implement as needed):

```javascript
import client from 'prom-client';

const scalingCounter = new client.Counter({
  name: 'rmq_scaling_events_total',
  help: 'Total number of scaling events',
  labelNames: ['profile', 'direction']
});
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

# Run linting
npm run lint

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
│   └── ConfigManager.js
├── bin/                    # CLI executable with deploy commands
├── tests/                  # Test suites
├── examples/               # Usage examples
├── scripts/                # Build and utility scripts
├── docs/                   # Documentation
└── .github/                # CI/CD workflows
```

## 🏆 Acknowledgments
- [RabbitMQ Cluster Operator](https://github.com/rabbitmq/cluster-operator) for Kubernetes integration
- [Kubernetes JavaScript Client](https://github.com/kubernetes-client/javascript) for API access
- The RabbitMQ and Kubernetes communities for inspiration and best practices

