# RabbitMQ Vertical Scaler

A Node.js application that automatically scales RabbitMQ cluster resources based on queue metrics and message rates.

## Features

- **5-second monitoring interval** - Checks RabbitMQ metrics every 5 seconds
- **Intelligent debouncing** - 30-second scale-up, 2-minute scale-down delays
- **Profile-based scaling** - LOW, MEDIUM, HIGH, CRITICAL resource profiles
- **Kubernetes native** - Uses Kubernetes API for scaling operations
- **Configurable thresholds** - Customizable via ConfigMap

## Build & Deploy

### 1. Build Docker Image

```bash
cd scaler
docker build -t your-registry/rmq-vertical-scaler:v1.0.0 .
docker push your-registry/rmq-vertical-scaler:v1.0.0
```

### 2. Update Deployment

Update the image in your Kubernetes deployment:

```yaml
spec:
  containers:
    - name: scaler
      image: your-registry/rmq-vertical-scaler:v1.0.0
      env:
        - name: RMQ_USER
          valueFrom:
            secretKeyRef:
              name: rmq-default-user
              key: username
        - name: RMQ_PASS
          valueFrom:
            secretKeyRef:
              name: rmq-default-user
              key: password
      volumeMounts:
        - name: config
          mountPath: /config
```

### 3. Deploy

```bash
kubectl apply -f k8s/prod/prep/vv.rmq-vertical-scaler2.yaml
```

## Configuration

The scaler reads configuration from `/config/config.json` (mounted via ConfigMap):

```json
{
  "thresholds": {
    "queue": { "low": 1000, "medium": 2000, "high": 10000, "critical": 50000 },
    "rate": { "low": 20, "medium": 200, "high": 1000, "critical": 2000 }
  },
  "profiles": {
    "LOW": { "cpu": "330m", "memory": "2Gi" },
    "MEDIUM": { "cpu": "800m", "memory": "3Gi" },
    "HIGH": { "cpu": "1600m", "memory": "4Gi" },
    "CRITICAL": { "cpu": "2400m", "memory": "8Gi" }
  },
  "debounce": {
    "scaleUpSeconds": 30,
    "scaleDownMinutes": 2
  },
  "checkInterval": 5,
  "rmq": {
    "host": "rmq.prod.svc.cluster.local",
    "port": "15672"
  }
}
```

## Scaling Logic

### Scale-Up Debounce (30 seconds)

```
low → low → medium(0s) → medium(5s) → ... → medium(30s) → ✅ SCALE UP
```

### Scale-Down Debounce (2 minutes)

```
medium → low(0s) → low(5s) → ... → low(120s) → ✅ SCALE DOWN
```

## Monitoring

Check scaler logs:

```bash
kubectl logs -n prod deployment/rmq-vertical-scaler-nodejs -f
```

Check scaling state:

```bash
kubectl get configmap rmq-scaler-state -n prod -o yaml
```
