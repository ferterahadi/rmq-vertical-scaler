# RabbitMQ Vertical Scaler

[![GitHub](https://img.shields.io/badge/source-github-181717?logo=github)](https://github.com/ferterahadi/rmq-vertical-scaler)
[![License](https://img.shields.io/badge/license-MIT-blue)](https://github.com/ferterahadi/rmq-vertical-scaler/blob/master/LICENSE)
[![Image size](https://img.shields.io/docker/image-size/ferterahadi/rmq-vertical-scaler/latest)](https://hub.docker.com/r/ferterahadi/rmq-vertical-scaler/tags)

Right-sizes a RabbitMQ cluster's CPU and memory in Kubernetes, automatically, from
live queue metrics — with **zero pod restarts** on Kubernetes 1.33+.

A single static Go binary on `distroless/static:nonroot`. No shell, no package
manager, no interpreter. ~13 MB image, ~10–15 MB idle memory.

---

## What it does

Every few seconds the scaler reads queue depth and message rate from the RabbitMQ
management API, picks the matching resource profile, and applies it to your
`RabbitmqCluster` custom resource. Quiet cluster → small requests. Backlog
building → bigger requests, before consumers fall behind.

```
LOW       330m / 2Gi    ← floor, no thresholds
MEDIUM    800m / 3Gi    ← queue > 2 000   or rate > 200 msg/s
HIGH     1600m / 4Gi    ← queue > 10 000  or rate > 1 000 msg/s
CRITICAL 2400m / 8Gi    ← queue > 50 000  or rate > 2 000 msg/s
```

A change only lands after the target profile has held for a debounce window
(30s up, 120s down by default), so a traffic spike does not thrash the cluster.

---

## Supported tags

| Tag | Points at | Use it when |
|-|-|-|
| `X.Y.Z` (e.g. `2.2.1`) | An exact, immutable release | **Production.** Recommended |
| `2` | Latest 2.x release | You want patch and minor updates automatically |
| `latest` | Latest release of any version | Trying it out only — never in production |
| `1`, `1.x` | The legacy Node.js v1 | You have not migrated yet. No longer developed |

Architectures: `linux/amd64`, `linux/arm64`.

The v2 line is a behaviour-identical rewrite of v1 in Go — same scaling
decisions, same environment-variable contract, same generated manifests.
[Upgrade guide](https://github.com/ferterahadi/rmq-vertical-scaler/blob/master/docs/upgrading-from-v1.md).

---

## Quick start

The image carries both the controller and the manifest generator. Generate your
Kubernetes manifests with it, then apply them:

```bash
# 1. Scaffold and edit a config
docker run --rm -v "$PWD:/work" -w /work \
  ferterahadi/rmq-vertical-scaler:2 init

# 2. Generate manifests (validated against the JSON schema)
docker run --rm -v "$PWD:/work" -w /work \
  ferterahadi/rmq-vertical-scaler:2 generate -c my-config.json -o scaler.yaml

# 3. Deploy
kubectl apply -f scaler.yaml
```

The generated manifests create a ServiceAccount, a Role and RoleBinding, a state
ConfigMap, a PodDisruptionBudget, and the scaler Deployment — with the image tag
pinned to the release version, never `latest`.

The default command is `run` (the in-cluster control loop):

```
ENTRYPOINT ["/rmq-vertical-scaler"]
CMD ["run"]
```

Prefer Helm? See the
[chart](https://github.com/ferterahadi/rmq-vertical-scaler/tree/master/charts/rmq-vertical-scaler).

---

## Zero restarts — when, exactly

Two mechanisms apply a resource change, and `auto` (the default) picks one at
startup:

| Your cluster | Mode chosen | Pods restart? | Risk to in-flight messages |
|-|-|-|-|
| Kubernetes ≥ 1.33 **and** `pods/resize` RBAC granted | **in-place** | No — resources change live | None |
| Kubernetes < 1.33, **or** RBAC missing, **or** the node can't fit the new size | **rolling** | Yes — pods roll one at a time | Real on a single-node cluster |

So "zero restarts" is a property of your cluster, not a promise of the image.
Check the startup log — `Scale mode: inplace (pods/resize permitted)` or
`Scale mode: rolling (…)`. Set `SCALE_MODE=inplace` to refuse to start rather
than silently degrade.

| `SCALE_MODE` | Behaviour |
|-|-|
| `auto` (default) | In-place when `pods/resize` is supported and permitted; rolling otherwise |
| `inplace` | Always in-place. Fails fast at startup if RBAC is missing, naming the verbs to grant |
| `rolling` | Always patch the `RabbitmqCluster` and let the operator roll pods |

Only resource **requests** are ever patched. Limits set on your
`RabbitmqCluster` are untouched, so RabbitMQ's memory high-watermark — derived
from the limit — stays correct across resizes.

---

## Requirements

| | |
|-|-|
| Kubernetes | 1.24+ to run; **1.33+** for zero-restart in-place resize |
| RabbitMQ | Deployed by the [RabbitMQ Cluster Operator](https://github.com/rabbitmq/cluster-operator) as a `RabbitmqCluster` |
| Credentials | A secret named `{serviceName}-default-user` (the operator creates this) |
| Topology | **3+ nodes with quorum queues** |

⚠️ **Run RabbitMQ with 3 or more nodes and quorum queues. This still holds even
though in-place mode restarts nothing:**

1. **In-place is conditional.** Any fallback in the table above turns the next
   scale action into a rolling restart. On a single node that means dropped
   messages.
2. **Scaling down changes eviction priority.** The scaler lowers the pod's memory
   request; under node memory pressure a pod using more than its request is
   evicted sooner. A single-node cluster has nowhere to fail over to.
3. **Restarts happen anyway** — node drains, operator image bumps, OOM kills.

Vertical scaling suits **bursty or infrequent** workloads, where the resource
saving is worth this trade-off.

---

## Configuration

The controller is configured entirely by environment variables; `generate` and
the Helm chart write them for you. The ones you will actually touch:

| Variable | Default | Meaning |
|-|-|-|
| `RMQ_HOST` | *(none)* | Management API host |
| `RMQ_PORT` | *(none)* | Management API port, normally `15672` |
| `RMQ_USER` / `RMQ_PASS` | `guest` | Management API credentials — wire these to a secret |
| `RMQ_SERVICE_NAME` | `rmq` | Name of the `RabbitmqCluster` to patch |
| `NAMESPACE` | `prod` | Namespace of the cluster and state ConfigMap |
| `SCALE_MODE` | `auto` | `auto`, `inplace`, or `rolling` |
| `CHECK_INTERVAL_SECONDS` | `5` | Seconds between metric polls |
| `DEBOUNCE_SCALE_UP_SECONDS` | `30` | Hold time before scaling up |
| `DEBOUNCE_SCALE_DOWN_SECONDS` | `120` | Hold time before scaling down |
| `PROFILE_NAMES` | `LOW MEDIUM HIGH CRITICAL` | Ordered, smallest first; the first is the floor |
| `PROFILE_<NAME>_CPU` / `_MEMORY` | `1000m` / `2Gi` | Requests for that profile |
| `QUEUE_THRESHOLD_<NAME>` | `1000` | Queue depth that selects the profile |
| `RATE_THRESHOLD_<NAME>` | `100` | Messages/second that selects the profile |

[Full reference](https://github.com/ferterahadi/rmq-vertical-scaler/blob/master/docs/environment.md).

---

## Image details

| | |
|-|-|
| Base | `gcr.io/distroless/static:nonroot` |
| User | `nonroot:nonroot` (uid 65532), no shell, no package manager |
| Binary | Single static Go binary, `CGO_ENABLED=0`, stripped |
| Platforms | `linux/amd64`, `linux/arm64` |
| Signals | Go handles `SIGTERM`/`SIGINT` natively as PID 1 — no init wrapper |

---

## Links

- **Source and docs** — https://github.com/ferterahadi/rmq-vertical-scaler
- **Changelog** — https://github.com/ferterahadi/rmq-vertical-scaler/blob/master/CHANGELOG.md
- **Helm chart** — https://github.com/ferterahadi/rmq-vertical-scaler/tree/master/charts/rmq-vertical-scaler
- **Issues** — https://github.com/ferterahadi/rmq-vertical-scaler/issues
- **License** — MIT
