# RabbitMQ Vertical Scaler for GKE

A Node.js application that automatically **vertically scales** RabbitMQ cluster resources (CPU/Memory) based on queue metrics and message rates in Google Kubernetes Engine (GKE).

## What is this?

This scaler monitors your RabbitMQ cluster and automatically adjusts the CPU and memory resources of your RabbitMQ pods based on:
- Queue depth (number of messages waiting)
- Message publish/consume rates
- Overall cluster load

**Important**: This is **vertical scaling** (increasing/decreasing pod resources), not horizontal scaling (adding/removing pods).

## Quick Start

### 1. Login to your GKE cluster
```bash
gcloud container clusters get-credentials your-cluster-name --zone your-zone --project your-project
```

### 2. Generate deployment files
```bash
cd deploy
./generate.sh
```
This creates a `*-scaler.yaml` file customized for your environment.

### 3. Deploy the scaler
```bash
kubectl apply -f *-scaler.yaml
```

## Configuration

After running `generate.sh`, you can modify the generated `*-scaler.yaml` file to customize the scaler behavior by editing the `env` section in the deployment:

### Scaler Resource Requirements
```yaml
resources:
  requests:
    cpu: 75m        # CPU request for the scaler pod
    memory: 128Mi   # Memory request for the scaler pod
  limits:
    cpu: 200m       # CPU limit for the scaler pod
    memory: 512Mi   # Memory limit for the scaler pod
```

### RabbitMQ Connection Settings
```yaml
env:
  - name: RMQ_HOST
    value: 'rmq.prod.svc.cluster.local'  # Your RabbitMQ service hostname
  - name: RMQ_PORT
    value: '15672'                       # RabbitMQ management port
  - name: SCALER_NAME
    value: 'rabbitmq'                    # Name of your RabbitMQ cluster resource
  - name: NAMESPACE
    value: 'prod'                        # Kubernetes namespace
```

### Resource Profiles (RabbitMQ Cluster Resources)
```yaml
env:
  # LOW Profile
  - name: PROFILE_LOW_CPU
    value: '300m'     # CPU for LOW profile
  - name: PROFILE_LOW_MEMORY
    value: '2Gi'      # Memory for LOW profile
  
  # MEDIUM Profile
  - name: PROFILE_MEDIUM_CPU
    value: '800m'     # CPU for MEDIUM profile
  - name: PROFILE_MEDIUM_MEMORY
    value: '3Gi'      # Memory for MEDIUM profile
  
  # HIGH Profile
  - name: PROFILE_HIGH_CPU
    value: '1600m'    # CPU for HIGH profile
  - name: PROFILE_HIGH_MEMORY
    value: '4Gi'      # Memory for HIGH profile
  
  # CRITICAL Profile
  - name: PROFILE_CRITICAL_CPU
    value: '2400m'    # CPU for CRITICAL profile
  - name: PROFILE_CRITICAL_MEMORY
    value: '8Gi'      # Memory for CRITICAL profile
```

### Scaling Thresholds
```yaml
env:
  # Queue depth thresholds (number of messages)
  - name: QUEUE_THRESHOLD_MEDIUM
    value: '2000'     # Scale to MEDIUM when queue > 2000 messages
  - name: QUEUE_THRESHOLD_HIGH
    value: '10000'    # Scale to HIGH when queue > 10000 messages
  - name: QUEUE_THRESHOLD_CRITICAL
    value: '50000'    # Scale to CRITICAL when queue > 50000 messages
  
  # Message rate thresholds (messages per second)
  - name: RATE_THRESHOLD_MEDIUM
    value: '200'      # Scale to MEDIUM when rate > 200 msg/s
  - name: RATE_THRESHOLD_HIGH
    value: '1000'     # Scale to HIGH when rate > 1000 msg/s
  - name: RATE_THRESHOLD_CRITICAL
    value: '2000'     # Scale to CRITICAL when rate > 2000 msg/s
```

### Timing Settings
```yaml
env:
  - name: DEBOUNCE_SCALE_UP_SECONDS
    value: '30'       # Wait 30s before scaling up
  - name: DEBOUNCE_SCALE_DOWN_SECONDS
    value: '120'      # Wait 120s (2min) before scaling down
  - name: CHECK_INTERVAL_SECONDS
    value: '5'        # Check metrics every 5 seconds
```

## How it Works

1. **Monitoring**: Checks RabbitMQ metrics every 5 seconds
2. **Decision**: Compares metrics against thresholds to determine target profile
3. **Debouncing**: Waits for stability before scaling (prevents flapping)
4. **Scaling**: Updates RabbitMQ cluster resource requests via Kubernetes API

### Scaling Logic
- **Scale Up**: 30-second debounce (quick response to load)
- **Scale Down**: 2-minute debounce (conservative to avoid thrashing)

## Limitations & Tested Environment

⚠️ **Use at your own risk** – This code is provided as-is and has only been tested in the environment described below.

### Tested Environment
- **GKE cluster** with RabbitMQ deployed via the [RabbitMQ Operator](https://www.rabbitmq.com/kubernetes/operator/operator-overview.html)
- **RabbitMQ version**: Tested with RabbitMQ 3.1.x
- **Cluster size**: Minimum 3-node RabbitMQ cluster used during testing
- **PodDisruptionBudget**: Included in `*-scaler.yaml`, set to at least `minAvailable: 2` to maintain high availability

### Limitations
- **Vertical scaling only** – Does not add/remove pods; only adjusts CPU/Memory
- **Single cluster support** – One RabbitMQ cluster per scaler instance
- **GKE specific** – Developed and tested exclusively on Google Kubernetes Engine
- **Operator dependency** – Requires the RabbitMQ Operator for cluster lifecycle management

## Monitoring

Check scaler status:
```bash
# View scaler logs
kubectl logs -l app=rmq-vertical-scaler -f

# Check current scaling state
kubectl get configmap *-scaler-state -o yaml

# Monitor RabbitMQ resource usage
kubectl describe rabbitmqcluster your-rmq-cluster
```

## Development

The codebase is organized as follows:

```
├── deploy/                 # User-facing deployment files
│   ├── generate.sh        # Script to generate deployment YAML
│   ├── README.md          # This documentation
│   └── templates/         # Template files (if any)
├── src/                   # Source code and build files
│   ├── scale.js          # Main application code
│   ├── package.json      # Dependencies
│   ├── webpack.config.js # Build configuration
│   ├── yarn.lock         # Lock file
│   ├── build.sh          # Build and push script
│   └── Dockerfile        # Container build
└── dist/                  # Build artifacts (generated)
```

To build the Docker image:
```bash
cd src
./build.sh
```

## Contributing

This code is provided as-is for the community. Feel free to:
- Fork and improve the code
- Submit issues and feature requests
- Adapt it for your specific use case

## License

MIT License - See LICENSE file for details.
