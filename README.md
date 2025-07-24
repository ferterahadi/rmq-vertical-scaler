# RabbitMQ Vertical Scaler

[![npm version](https://badge.fury.io/js/rmq-vertical-scaler.svg)](https://badge.fury.io/js/rmq-vertical-scaler)
[![Docker Image](https://img.shields.io/docker/v/rmq-vertical-scaler/rmq-vertical-scaler?label=docker)](https://hub.docker.com/r/rmq-vertical-scaler/rmq-vertical-scaler)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Node.js CI](https://github.com/yourusername/rmq-vertical-scaler/workflows/Node.js%20CI/badge.svg)](https://github.com/yourusername/rmq-vertical-scaler/actions)

A powerful, production-ready Node.js application that automatically **vertically scales** RabbitMQ cluster resources (CPU/Memory) based on real-time queue metrics and message rates in Kubernetes environments.

## 🚀 Features

- **🎯 Intelligent Scaling**: Automatically adjusts RabbitMQ resources based on queue depth and message rates
- **📊 Multiple Profiles**: Supports configurable scaling profiles (LOW, MEDIUM, HIGH, CRITICAL)
- **⚡ Debounced Scaling**: Prevents oscillation with configurable scale-up/scale-down delays
- **🔧 Highly Configurable**: Environment variables, config files, and CLI options
- **🐳 Cloud Native**: Kubernetes-first design with built-in deployment tools
- **📈 Monitoring Ready**: Built-in health checks and metrics endpoints
- **🛡️ Production Hardened**: Comprehensive error handling and logging
- **📦 Multiple Install Options**: npm, Docker, or binary releases

## 📋 Table of Contents

- [Quick Start](#-quick-start)
- [Installation](#-installation)
- [Configuration](#-configuration)
- [Usage](#-usage)
- [API Reference](#-api-reference)
- [Deployment](#-deployment)
- [Monitoring](#-monitoring)
- [Contributing](#-contributing)
- [License](#-license)

## ⚡ Quick Start

### Using npm

```bash
# Install globally
npm install -g rmq-vertical-scaler

# Run with environment variables
export RMQ_HOST="my-rabbitmq.prod.svc.cluster.local"
export NAMESPACE="production"
rmq-vertical-scaler --dry-run
```

### Using Docker

```bash
docker run --rm \
  -e RMQ_HOST=my-rabbitmq.prod.svc.cluster.local \
  -e NAMESPACE=production \
  rmq-vertical-scaler/rmq-vertical-scaler:latest
```

### Using Kubernetes CLI

```bash
# Generate and deploy in one command
rmq-vertical-scaler deploy generate --namespace production --service-name my-rabbitmq | kubectl apply -f -
```

## 🔧 Installation

### Prerequisites

- **Node.js**: ≥18.0.0
- **Kubernetes**: ≥1.20 with RabbitMQ Cluster Operator
- **RabbitMQ**: Cluster managed by [RabbitMQ Cluster Operator](https://github.com/rabbitmq/cluster-operator)

### Install Options

#### 1. npm Package

```bash
# Global installation
npm install -g rmq-vertical-scaler

# Local installation
npm install rmq-vertical-scaler
```

#### 2. Docker Image

```bash
# Pull latest image
docker pull rmq-vertical-scaler/rmq-vertical-scaler:latest

# Or build locally
git clone https://github.com/yourusername/rmq-vertical-scaler.git
cd rmq-vertical-scaler
docker build -t rmq-vertical-scaler .
```

#### 3. Kubernetes Deployment

```bash
# Generate deployment manifests
rmq-vertical-scaler deploy generate --namespace production --service-name my-rabbitmq

# Install to cluster
rmq-vertical-scaler deploy install

# Or do both in one step
rmq-vertical-scaler deploy generate --namespace production | kubectl apply -f -
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

### Command Line Interface

```bash
# Start the scaler (default command)
rmq-vertical-scaler [start] [options]
rmq-vertical-scaler --config ./config.json --namespace production

# Deploy to Kubernetes
rmq-vertical-scaler deploy generate [options]
rmq-vertical-scaler deploy install [options]

# Show help
rmq-vertical-scaler --help
rmq-vertical-scaler deploy --help

Options:
  -V, --version                output the version number
  -c, --config <path>          path to configuration file
  -n, --namespace <namespace>  Kubernetes namespace (default: "default")
  -d, --debug                  enable debug logging
  --dry-run                    simulate scaling without applying changes
  -h, --help                   display help for command
```

### Programmatic Usage

```javascript
import { RabbitMQVerticalScaler } from 'rmq-vertical-scaler';

const scaler = new RabbitMQVerticalScaler({
  configPath: './config.json',
  namespace: 'production',
  debug: true,
  dryRun: false
});

// Start scaling
await scaler.start();

// Health check
const health = await scaler.healthCheck();
console.log(health);

// Stop scaling
await scaler.stop();
```

### Docker Usage

```bash
# Basic usage
docker run --rm \
  -e RMQ_HOST=rabbitmq.default.svc.cluster.local \
  -e NAMESPACE=production \
  rmq-vertical-scaler/rmq-vertical-scaler:latest

# With custom configuration
docker run --rm \
  -v $(pwd)/config.json:/app/config.json \
  -e CONFIG_PATH=/app/config.json \
  rmq-vertical-scaler/rmq-vertical-scaler:latest

# Dry run mode
docker run --rm \
  -e RMQ_HOST=rabbitmq.default.svc.cluster.local \
  -e NAMESPACE=production \
  rmq-vertical-scaler/rmq-vertical-scaler:latest \
  --dry-run
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
# Generate and apply manifests
rmq-vertical-scaler deploy generate \
  --namespace production \
  --service-name my-rabbitmq \
  --image rmq-vertical-scaler:latest \
  --output production-scaler.yaml

# Install to cluster
rmq-vertical-scaler deploy install --file production-scaler.yaml

# Or combine both steps
rmq-vertical-scaler deploy generate --namespace production | kubectl apply -f -
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

## 🤝 Contributing

We welcome contributions! Please see our [Contributing Guide](CONTRIBUTING.md) for details.

### Development Setup

```bash
# Clone repository
git clone https://github.com/yourusername/rmq-vertical-scaler.git
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

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🆘 Support

- **Documentation**: [Full documentation](https://docs.rmq-vertical-scaler.dev)
- **Issues**: [GitHub Issues](https://github.com/yourusername/rmq-vertical-scaler/issues)
- **Discussions**: [GitHub Discussions](https://github.com/yourusername/rmq-vertical-scaler/discussions)
- **Email**: support@rmq-vertical-scaler.dev

## 🏆 Acknowledgments

- [RabbitMQ Cluster Operator](https://github.com/rabbitmq/cluster-operator) for Kubernetes integration
- [Kubernetes JavaScript Client](https://github.com/kubernetes-client/javascript) for API access
- The RabbitMQ and Kubernetes communities for inspiration and best practices

