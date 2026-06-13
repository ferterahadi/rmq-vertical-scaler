# RabbitMQ Vertical Scaler

[![Docker Image](https://img.shields.io/docker/v/ferterahadi/rmq-vertical-scaler?label=docker)](https://hub.docker.com/r/ferterahadi/rmq-vertical-scaler)
[![Go Reference](https://pkg.go.dev/badge/github.com/ferterahadi/rmq-vertical-scaler.svg)](https://pkg.go.dev/github.com/ferterahadi/rmq-vertical-scaler)
[![Go Report Card](https://goreportcard.com/badge/github.com/ferterahadi/rmq-vertical-scaler)](https://goreportcard.com/report/github.com/ferterahadi/rmq-vertical-scaler)

Automatically scales RabbitMQ cluster resources (CPU/Memory) based on real-time queue metrics and message rates in Kubernetes.

A small, dependency-light Kubernetes control loop written in **Go**. It watches the RabbitMQ management API and patches the `RabbitmqCluster` custom resource's CPU/memory requests up or down as load changes — shipped as a single static binary in a `distroless` container.

> ⚠️ **Note**  
> Vertically scaling RabbitMQ is *not* generally recommended, since node restarts can cause **temporary disruption and potential message loss**. I understand and accept this trade-off. This scaler is intended as an **alternative** for **infrequent or bursty workloads** where some disruption is acceptable to save resources.  
>  
> ⚠️ **Important**: This scaler is recommended only for **quorum queues with 3+ nodes**. Using it on **single-node** RabbitMQ deployments will result in **message loss** during scaling operations.

> ℹ️ **v2.0.0** is a behaviour-identical rewrite of the Node.js v1 in Go — same scaling decisions, same env-var contract, same generated manifests, smaller footprint. See [Upgrading from v1](#-upgrading-from-v1).

## 🚀 Features

- **🎯 Auto Scaling**: Adjusts resources based on queue depth and message rates
- **⚡ Debounced**: Prevents oscillation with configurable scale-up/scale-down delays
- **🔧 Configurable**: Environment variables, config files, and CLI options
- **🐳 Cloud Native**: Official `client-go`, `distroless` image, single static binary
- **🪶 Tiny footprint**: ~10–15 MB idle RSS, no interpreted runtime, minimal attack surface

## 📋 Table of Contents

- [Why Go (footprint, not speed)](#-why-go-footprint-not-speed)
- [Install](#-install)
- [Quick Start](#-quick-start)
- [Configuration](#️-configuration)
- [Deployment](#-deployment)
- [Architecture](#️-architecture)
- [Upgrading from v1](#-upgrading-from-v1)
- [Development](#-development)

## 🧭 Why Go (footprint, not speed)

**This rewrite is not about speed.** The workload is a low-frequency, I/O-bound
control loop: every few seconds it does one HTTP GET against the RabbitMQ
management API, compares a handful of numbers, and *maybe* issues a Kubernetes
PATCH. It is idle ~99.9% of the time and the bottleneck is the network call, not
the CPU. Go does not make any scaling decision perceptibly faster.

Go was chosen because, for a Kubernetes operator, it wins on what actually
matters for a tool whose entire pitch is *saving cluster resources*:

1. **Memory footprint** — Node idles ~50–80 MB RSS; the Go binary idles ~10–15 MB. A fat runtime sidecar undercuts a resource-saver.
2. **Image & attack surface** — `distroless/static:nonroot` + one static binary, no shell, no package manager, no interpreter. Fewer CVEs, faster pulls.
3. **Ecosystem fit** — `client-go` is the official, first-class Kubernetes client; the whole operator ecosystem is Go.
4. **Dependency hygiene** — stdlib `net/http` instead of axios; `client-go` is the one real dependency.
5. **Distribution** — a single cross-compiled binary (`go install` or a release download); no "do you have Node 18+?".

See [research/benchmark.md](research/benchmark.md) for the full methodology and the v1-vs-v2 numbers.

## 📦 Install

The CLI generates Kubernetes manifests; the same binary runs the controller in-cluster.

```bash
# Docker (controller image — also runs `generate`)
docker pull ferterahadi/rmq-vertical-scaler:2

# Go install (CLI / generator)
go install github.com/ferterahadi/rmq-vertical-scaler/cmd/rmq-vertical-scaler@latest

# Or grab a prebuilt binary from the GitHub Releases page (linux/amd64, linux/arm64)
```

## ⚡ Quick Start

```bash
# Generate manifests from a config file
rmq-vertical-scaler generate \
  --config examples/production-config.json \
  --output my-scaler.yaml

# Deploy to your cluster
kubectl apply -f my-scaler.yaml
```

Using the container image instead of a local binary:

```bash
docker run --rm -v "$PWD:/work" -w /work \
  ferterahadi/rmq-vertical-scaler:2 generate \
  --config examples/production-config.json --output my-scaler.yaml
```

## ⚙️ Configuration

The scaler supports two configuration methods.

### Configuration File (Recommended)

```bash
# Use pre-built templates
rmq-vertical-scaler generate --config examples/basic-config.json
rmq-vertical-scaler generate --config examples/production-config.json

# Create custom configuration
curl -o my-config.json https://raw.githubusercontent.com/ferterahadi/rmq-vertical-scaler/master/examples/template-config.json
rmq-vertical-scaler generate --config my-config.json --output my-scaler.yaml
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

The first profile is the **floor** (no thresholds). Each subsequent profile is
selected when its `queue` depth **or** `rate` threshold is exceeded; the engine
scans highest-to-lowest and takes the first match.

**Production Configuration** (`examples/production-config.json`):
- Higher resource limits: MINIMAL (500m/4Gi) → MAXIMUM (4000m/32Gi)
- Conservative scaling: Longer debounce times (60s up, 300s down)
- Higher thresholds: Queue depths from 5K to 100K messages

### CLI Options

```bash
rmq-vertical-scaler generate --help
```

| Flag | Description |
|---|---|
| `-c, --config` | Path to JSON configuration file |
| `-n, --namespace` | Kubernetes namespace |
| `-s, --service-name` | RabbitMQ service/cluster name (DNS + secret) |
| `-o, --output` | Output YAML file name |
| `--image` | Scaler container image |
| `--scaler-name` | Name for scaler resources (ServiceAccount, Role, …) |

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
rmq-vertical-scaler generate \
  --config examples/production-config.json \
  --output production-scaler.yaml

kubectl apply -f production-scaler.yaml

# Monitor deployment
kubectl get deployment rmq-vertical-scaler -n production
kubectl logs -f deployment/rmq-vertical-scaler -n production
```

The generated manifests create a `ServiceAccount`, a `Role` + `RoleBinding`
(get/patch on `rabbitmqclusters` and `configmaps`, get on `secrets`), a state
`ConfigMap`, a `PodDisruptionBudget`, and the scaler `Deployment`. The container
runs `rmq-vertical-scaler run` by default.

## 🏗️ Architecture

### Component Overview

```
┌─────────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│  internal/metrics   │────│ internal/scaling │────│   internal/k8s  │
│                     │    │                  │    │                 │
│ • RabbitMQ API      │    │ • Profile logic  │    │ • client-go     │
│ • net/http GETs     │    │ • Thresholds     │    │ • CRD (dynamic) │
│ • Wait-for-ready    │    │ • Debounce (pure)│    │ • ConfigMap     │
└─────────────────────┘    └──────────────────┘    └─────────────────┘
         │                           │                         │
         └───────────────────────────┼─────────────────────────┘
                                      │
                  ┌─────────────────────────────────────┐
                  │       internal/controller           │
                  │                                     │
                  │ • Orchestration (ApplyScale/Run)    │
                  │ • internal/config (env vars)        │
                  │ • Signal-driven graceful shutdown   │
                  │ • Stability tracking                │
                  └─────────────────────────────────────┘
```

### Scaling Logic

1. **Metrics Collection**: Fetch queue depth and message rates from the RabbitMQ API
2. **Profile Determination**: Compare metrics against configured thresholds (highest match wins)
3. **Stability Check**: Ensure the target profile is stable for the required duration
4. **Debouncing**: Apply scale-up/scale-down delays to prevent oscillation
5. **Resource Update**: JSON-Patch the `RabbitmqCluster` CPU/memory requests

### Project Structure

```
rmq-vertical-scaler/
├── cmd/rmq-vertical-scaler/   # CLI entry point: `generate` + `run`
├── internal/
│   ├── config/                # Env-var configuration (profiles, thresholds)
│   ├── metrics/               # RabbitMQ management API client (net/http)
│   ├── scaling/               # Pure scaling + debounce engine (table-tested)
│   ├── k8s/                   # client-go: CRD (dynamic) + ConfigMap (CoreV1)
│   ├── controller/            # Control loop orchestration
│   └── manifests/             # Embedded YAML template for `generate`
├── examples/                  # Configuration templates
├── schema/                    # JSON Schema for configuration validation
├── research/                  # Benchmark methodology + data
├── Dockerfile                 # Multi-stage → distroless/static:nonroot
└── Makefile                   # build / test / image / bench
```

## ⬆️ Upgrading from v1

v2 is a **drop-in** replacement:

- **Same env-var contract.** A Deployment generated by v1 drives v2 unchanged — just swap the image tag to `ferterahadi/rmq-vertical-scaler:2`.
- **Same scaling decisions.** The engine makes byte-identical decisions to v1 given the same metrics + config (enforced by a parity test).
- **Same generated manifests.** `generate` emits YAML byte-for-byte compatible with v1.
- **Same config format.** Your existing `examples/*.json` configs work as-is.

The Node.js v1 remains available via the `v1.x` git tags and the
`ferterahadi/rmq-vertical-scaler:1.x` images.

## 🛠️ Development

Requires Go 1.26+.

```bash
git clone https://github.com/ferterahadi/rmq-vertical-scaler.git
cd rmq-vertical-scaler

make test     # run all unit tests (incl. the v1 YAML parity golden test)
make build    # build a static binary into dist/
make image    # build the container image (requires Docker)
```

## 🏆 Acknowledgments
- [RabbitMQ Cluster Operator](https://github.com/rabbitmq/cluster-operator) for Kubernetes integration
- [client-go](https://github.com/kubernetes/client-go) for first-class Kubernetes API access
- The RabbitMQ and Kubernetes communities for inspiration and best practices
