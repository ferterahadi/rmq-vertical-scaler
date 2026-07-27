# RabbitMQ Vertical Scaler

[![Docker Image](https://img.shields.io/docker/v/ferterahadi/rmq-vertical-scaler?label=docker)](https://hub.docker.com/r/ferterahadi/rmq-vertical-scaler)
[![Go Reference](https://pkg.go.dev/badge/github.com/ferterahadi/rmq-vertical-scaler/v2/cmd/rmq-vertical-scaler.svg)](https://pkg.go.dev/github.com/ferterahadi/rmq-vertical-scaler/v2/cmd/rmq-vertical-scaler)
[![Go Report Card](https://goreportcard.com/badge/github.com/ferterahadi/rmq-vertical-scaler/v2)](https://goreportcard.com/report/github.com/ferterahadi/rmq-vertical-scaler/v2)

Right-sizes a RabbitMQ cluster's CPU and memory in Kubernetes, automatically, from live queue metrics — with **zero pod restarts** on Kubernetes 1.33+.

## What it does

Every few seconds the scaler reads queue depth and message rate from the RabbitMQ
management API, picks the matching resource profile, and applies it to your
`RabbitmqCluster`. Quiet cluster → small requests. Backlog building → bigger
requests, before consumers fall behind.

```
LOW       330m / 2Gi    ← floor, no thresholds
MEDIUM    800m / 3Gi    ← queue > 2 000   or rate > 200 msg/s
HIGH     1600m / 4Gi    ← queue > 10 000  or rate > 1 000 msg/s
CRITICAL 2400m / 8Gi    ← queue > 50 000  or rate > 2 000 msg/s
```

A change only lands after the target profile has held for a debounce window (30s
up, 120s down by default), so a traffic spike does not thrash the cluster.

It ships as one static Go binary in a `distroless` container: ~13 MB image,
~10–15 MB idle memory, no shell, no interpreter.

## How the change reaches your pods

This is the part that decides your restart and message-loss exposure. There are
two mechanisms, and `auto` (the default) picks one for you at startup:

| Your cluster | Mode chosen | Pods restart? | Risk to in-flight messages |
|-|-|-|-|
| Kubernetes ≥ 1.33 **and** `pods/resize` RBAC granted | **in-place** | No — resources change live | None |
| Kubernetes < 1.33, **or** RBAC missing, **or** the node can't fit the new size | **rolling** | Yes — the operator rolls pods one at a time | Real on a single-node cluster |

So "zero restarts" is a property of your cluster, not a promise of the tool.
Watch the startup log line — `Scale mode: inplace (pods/resize permitted)` or
`Scale mode: rolling (…)` — to see which one you actually got. Set
`SCALE_MODE=inplace` to refuse to start rather than silently degrade. Details in
[Scale modes](#scale-modes).

## Requirements

| | |
|-|-|
| Kubernetes | 1.24+ to run; **1.33+** for zero-restart in-place resize |
| RabbitMQ | Deployed by the [RabbitMQ Cluster Operator](https://github.com/rabbitmq/cluster-operator) as a `RabbitmqCluster` |
| Topology | **3+ nodes with quorum queues** — see below |

⚠️ **Run RabbitMQ with 3 or more nodes and quorum queues. This still holds in
v2.2.0+, even though in-place mode restarts nothing.** Three reasons:

1. **In-place is conditional.** In `auto` mode any of the fallbacks in the table
   above turns the next scale action into a rolling restart. On a single node a
   restart means dropped messages.
2. **Scaling down changes eviction priority.** The scaler lowers the pod's memory
   *request*; under node memory pressure, a pod using more than its request is
   evicted sooner. A single-node cluster has nowhere to fail over to.
3. **Restarts happen anyway** — node drains, operator image bumps, OOM kills.
   A single-node RabbitMQ has no redundancy for any of them.

Vertical scaling suits **bursty or infrequent** workloads, where the resource
saving is worth this trade-off. A steady, always-hot cluster gains little.

## Install

```bash
# Container image — runs the controller, and the `generate` CLI
docker pull ferterahadi/rmq-vertical-scaler:2

# CLI only, as a local binary
go install github.com/ferterahadi/rmq-vertical-scaler/v2/cmd/rmq-vertical-scaler@latest
```

Prebuilt `linux` and `darwin` binaries (amd64/arm64) with checksums are on the
[Releases page](https://github.com/ferterahadi/rmq-vertical-scaler/releases).

### Helm

```bash
helm install rvs charts/rmq-vertical-scaler \
  --namespace production --create-namespace \
  --set serviceName=my-rabbitmq
```

Chart values: [charts/rmq-vertical-scaler/README.md](charts/rmq-vertical-scaler/README.md).

## Quick start

```bash
rmq-vertical-scaler init                                    # writes my-config.json
# edit my-config.json
rmq-vertical-scaler generate -c my-config.json -o scaler.yaml
kubectl apply -f scaler.yaml
```

`generate` validates the config against the JSON schema and fails with a list of
violations — a typo never silently becomes a default.

Without a local binary, use the image:

```bash
docker run --rm -v "$PWD:/work" -w /work \
  ferterahadi/rmq-vertical-scaler:2 generate -c my-config.json -o scaler.yaml
```

## Configuration

### Config file

Start from [`examples/basic-config.json`](examples/basic-config.json) (or
[`production-config.json`](examples/production-config.json) — higher ceilings,
longer debounce, 5K–100K thresholds).

```json
{
  "$schema": "https://raw.githubusercontent.com/ferterahadi/rmq-vertical-scaler/master/schema/config-schema.json",
  "profiles": {
    "LOW":      { "cpu": "330m",  "memory": "2Gi" },
    "MEDIUM":   { "cpu": "800m",  "memory": "3Gi", "queue": 2000,  "rate": 200 },
    "HIGH":     { "cpu": "1600m", "memory": "4Gi", "queue": 10000, "rate": 1000 },
    "CRITICAL": { "cpu": "2400m", "memory": "8Gi", "queue": 50000, "rate": 2000 }
  },
  "debounce":      { "scaleUpSeconds": 30, "scaleDownSeconds": 120 },
  "checkInterval": 5,
  "scaling":       { "mode": "auto" },
  "rmq":           { "host": "rabbitmq.default.svc.cluster.local", "port": "15672" },
  "kubernetes":    { "namespace": "default", "rmqServiceName": "rabbitmq" }
}
```

The first profile is the **floor** and takes no thresholds. Every later profile
is selected when its `queue` **or** `rate` threshold is exceeded; the engine
scans highest to lowest and takes the first match. The `$schema` line gives you
autocomplete and validation in most editors.

At runtime the controller reads plain environment variables (`PROFILE_*_CPU`,
`QUEUE_THRESHOLD_*`, `SCALE_MODE`, `CHECK_INTERVAL_SECONDS`, …) — `generate`
just writes them into the Deployment for you. The full list is in
[docs/environment.md](docs/environment.md).

### Scale modes

`scaling.mode` in the config, or `SCALE_MODE` in the environment:

| Mode | Behaviour |
|-|-|
| `auto` (default) | In-place when `pods/resize` is supported **and** permitted; rolling otherwise |
| `inplace` | Always in-place. **Fails fast at startup** if RBAC is missing, naming the verbs to grant |
| `rolling` | Always patch the `RabbitmqCluster` only, and let the operator roll pods (v2.0.0 behaviour) |

**How in-place works.** The scaler sets the cluster's StatefulSet override to
`updateStrategy: OnDelete`, then on each scale action patches every pod's
resource **requests** through the `pods/resize` subresource *and* patches the
custom resource, so a pod recreated for any reason comes back at the current
size. If the kubelet returns `Infeasible` (the node can never fit it), `auto`
reverts the override and rolls instead.

Two things to know before enabling it:

- **`OnDelete` trade-off.** While in-place is active, operator-driven template
  changes (a RabbitMQ image bump, say) no longer roll pods automatically. Delete
  pods one at a time to roll them, or switch to `scaling.mode: rolling`.
- **Only requests change.** Limits you set on the `RabbitmqCluster` are never
  touched. RabbitMQ derives its memory high-watermark from the *limit*, so the
  watermark stays correct across resizes — no action needed.

### RabbitMQ credentials

The scaler reads the management API using a secret named
`{serviceName}-default-user`, which the RabbitMQ Cluster Operator creates for
you. For hand-rolled deployments, create it yourself:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: rabbitmq-default-user     # {serviceName}-default-user
  namespace: production
data:
  username: <base64>
  password: <base64>
```

### CLI flags

| Flag | Description |
|-|-|
| `-c, --config` | Path to the JSON config file |
| `-o, --output` | Output YAML file name |
| `-n, --namespace` | Kubernetes namespace |
| `-s, --service-name` | RabbitMQ cluster name (drives DNS, secret, PDB selector) |
| `--image` | Scaler container image (defaults to the CLI's own version, never `latest`) |
| `--scaler-name` | Name for the generated ServiceAccount, Role, Deployment |
| `--no-pdb` | Skip the PodDisruptionBudget for the RabbitMQ cluster |

## Deploy

```bash
rmq-vertical-scaler generate -c examples/production-config.json -o production-scaler.yaml
kubectl apply -f production-scaler.yaml

kubectl logs -f deployment/rmq-vertical-scaler -n production
```

`generate` emits a ServiceAccount, a Role + RoleBinding (get/patch on
`rabbitmqclusters` and `configmaps`, get on `secrets`, get/list on `pods`, patch
on `pods/resize`), a state ConfigMap, a PodDisruptionBudget, and the Deployment.
The container runs `rmq-vertical-scaler run` by default.

The first log lines tell you the chosen scale mode and the starting profile —
check them after every deploy.

## Documentation

| | |
|-|-|
| [docs/environment.md](docs/environment.md) | Every environment variable the controller reads |
| [docs/architecture.md](docs/architecture.md) | Package layout, control loop, project structure |
| [docs/why-go.md](docs/why-go.md) | Why v2 is Go — footprint, not speed |
| [docs/upgrading-from-v1.md](docs/upgrading-from-v1.md) | Drop-in upgrade from the Node.js v1 |
| [docs/local-testing.md](docs/local-testing.md) | Running against a local kind cluster |
| [research/benchmark.md](research/benchmark.md) | v1-vs-v2 measurements and methodology |
| [CHANGELOG.md](CHANGELOG.md) | Release history |

## Development

Requires Go 1.26+.

```bash
make test     # unit tests, including the v1 YAML parity golden test
make build    # static binary into dist/
make image    # container image (requires Docker)
```

## Acknowledgments

Built on the [RabbitMQ Cluster Operator](https://github.com/rabbitmq/cluster-operator)
and [client-go](https://github.com/kubernetes/client-go).

Licensed under the [MIT License](LICENSE).
