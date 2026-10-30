# Usage and operations

This guide describes the current v2.2 controller. Start with the
[README](../README.md) for the Helm installation path. Unreleased behavior lives
in the implementation plan linked there.

## Configure profiles and timing

A profile pairs a CPU request with a memory request for every RabbitMQ broker.
The first profile is the floor and has no thresholds. The controller scans the
remaining profiles from highest to lowest, choosing the first whose queue
**or** rate threshold is strictly exceeded; equality does not trigger it.

The queue signal is the maximum `messages` value across individual queues from
`/api/queues`. The rate signal is the cluster publish rate from `/api/overview`.
Total messages, consume rate, and backlog rate are logged but do not select a
profile. An unavailable overview or an empty queue list skips scaling; a declared
queue containing zero messages is different from having no queues.

### Defaults supplied by Helm and the generator

| Profile | CPU request | Memory request | Queue threshold | Publish-rate threshold |
|---|---|---|---|---|
| LOW | 330m | 2Gi | Floor | Floor |
| MEDIUM | 800m | 3Gi | > 2,000 | > 200 msg/s |
| HIGH | 1600m | 4Gi | > 10,000 | > 1,000 msg/s |
| CRITICAL | 2400m | 8Gi | > 50,000 | > 2,000 msg/s |

For example, a deepest queue of 12,000 messages selects HIGH even when publish
rate is low. A cluster total of 12,000 spread across many small queues does not
select HIGH on queue depth alone.

Polling defaults to 5 seconds. A changed recommendation resets its stability
timer; it must hold for 30 seconds before an increase or 120 seconds before a
reduction. Changes can jump across profiles in either direction. These delays
control submission timing, not a deadline for completed resizing.

Choose distinct CPU values: current-profile detection matches the CPU request
in the `RabbitmqCluster` spec, not observed broker memory or effective resources.
Review limits, node capacity, and RabbitMQ's own memory budget separately. The
controller changes requests only and does not adjust RabbitMQ's memory watermark.
Lowering a memory request below actual usage can increase eviction exposure
under node pressure. Raising a request does not demonstrate extra usable CPU,
higher throughput, or reduced infrastructure cost.

### JSON configuration for the CLI

Start from [basic-config.json](../examples/basic-config.json), or use
[production-config.json](../examples/production-config.json) as an example of
higher ceilings, longer debounce, and 5K–100K queue thresholds. These are examples
to adapt, not workload sizing guarantees.

```json
{
  "$schema": "https://raw.githubusercontent.com/ferterahadi/rmq-vertical-scaler/master/schema/config-schema.json",
  "profiles": {
    "LOW":      { "cpu": "330m",  "memory": "2Gi" },
    "MEDIUM":   { "cpu": "800m",  "memory": "3Gi", "queue": 2000, "rate": 200 },
    "HIGH":     { "cpu": "1600m", "memory": "4Gi", "queue": 10000, "rate": 1000 },
    "CRITICAL": { "cpu": "2400m", "memory": "8Gi", "queue": 50000, "rate": 2000 }
  },
  "debounce": { "scaleUpSeconds": 30, "scaleDownSeconds": 120 },
  "checkInterval": 5,
  "scaling": { "mode": "auto" },
  "rmq": { "host": "rabbitmq.default.svc.cluster.local", "port": "15672" },
  "kubernetes": { "namespace": "default", "rmqServiceName": "rabbitmq" }
}
```

The `$schema` enables editor assistance. `generate` validates JSON against that
schema before writing manifests. At runtime, the controller reads environment
variables; Helm and `generate` translate configuration into those variables.
Their complete names and direct-runtime fallback values are in the
[environment reference](environment.md).

## Scale modes

Set `scaling.mode` in Helm/JSON, or `SCALE_MODE` in a hand-written Deployment.
The controller discovers `pods/resize` support rather than comparing version
numbers. Kubernetes 1.33+ enables the in-place feature by default; API discovery,
permissions, node support, and container policy still matter.

| Mode | Behavior |
|---|---|
| `auto` (default) | Selects in-place when the resize API is advertised and permissions pass; otherwise selects rolling. An in-place action can fall back to rolling on an infeasible resize or a permission failure. |
| `inplace` | Keeps the in-place path and never falls back to rolling. Missing or unverifiable permissions fail startup. Missing API support only logs a warning; later resize requests may fail. |
| `rolling` | Patches the `RabbitmqCluster` requests and relies on the Operator's StatefulSet update strategy to replace brokers. |

In-place mode patches the `rabbitmq` container's requests through `pods/resize`,
then synchronizes the `RabbitmqCluster` requests so replacement pods use the
selected profile. It leaves CPU and memory limits untouched. Avoiding Pod
replacement does not guarantee an unchanged container: `resizePolicy` can
require a container restart.

The controller makes one immediate feasibility check after each accepted Pod
patch. Deferred or not-yet-reported resize state is not treated as failure.
Its success logs therefore do not establish effective-resource convergence.
Partial Pod changes can occur before another Pod fails.

### OnDelete and Operator upgrades

Before in-place changes, the controller writes this Operator override:

```yaml
spec:
  override:
    statefulSet:
      spec:
        updateStrategy:
          type: OnDelete
```

`OnDelete` lets template changes reach future pods without automatically replacing
existing ones. This also affects Operator-driven changes such as RabbitMQ image
upgrades. To apply those changes while retaining `OnDelete`, replace brokers one
at a time under your normal maintenance procedure, checking health and quorum
between replacements.

The automatic fallback explicitly changes this override to `RollingUpdate`.
Simply setting `scaling.mode: rolling`, stopping the scaler, or uninstalling it
does **not** remove a previously written `OnDelete` override. To resume automatic
rollouts, stop in-place actions, deliberately restore the Operator's intended
update strategy, and verify the resulting StatefulSet. Preserve unrelated
Operator overrides when doing so.

Use three or more brokers with quorum queues for redundancy. A single broker
has no failover during restarts, eviction, or node maintenance. Message durability
also depends on queue type, persistence, publisher confirms, and acknowledgements;
neither scaling mode guarantees lossless delivery.

## Credentials and deployment resources

Deploy the scaler in the same namespace as its Operator-managed target. The
Helm chart defaults to management HTTP port `15672` at
`<serviceName>.<namespace>.svc.cluster.local`. The default credentials Secret is
`<serviceName>-default-user`, with `username` and `password` keys, created by the
RabbitMQ Cluster Operator.

For an existing alternate Secret, Helm accepts `auth.existingSecret`,
`auth.usernameKey`, and `auth.passwordKey`. Use `rmq.host` and `rmq.port` for a
management endpoint override. Supplying a Secret does not add support for
RabbitMQ deployments without a `RabbitmqCluster` resource.

Helm and the CLI generator supply a Deployment, ServiceAccount, namespaced Role
and RoleBinding, state ConfigMap, and optional PodDisruptionBudget (PDB). The Role
grants get/patch/update on `rabbitmqclusters`, get/create/update/patch on
`configmaps`, get on `secrets`, get/list on `pods`, and patch on `pods/resize`.
Startup permission checks also use SelfSubjectAccessReview.

The default PDB requires two RabbitMQ pods to remain available; review it for
your topology or disable creation when you already manage an appropriate budget.
The default chart image is pinned to its `appVersion`. If overriding it with a
mutable image tag, set `image.pullPolicy: Always`.

## Deploy with the CLI or Docker

Install the CLI with Go, or download a matching platform binary and checksums
from [GitHub Releases](https://github.com/ferterahadi/rmq-vertical-scaler/releases):

```bash
go install github.com/ferterahadi/rmq-vertical-scaler/v2/cmd/rmq-vertical-scaler@latest
rmq-vertical-scaler init
# Edit my-config.json: target namespace/name, profiles, timing, and mode.
rmq-vertical-scaler generate -c my-config.json -o scaler.yaml
kubectl apply -f scaler.yaml
```

`init` writes `my-config.json` without overwriting an existing file. It accepts
`-o, --output` for another path. Review generated manifests before applying them;
the container starts `rmq-vertical-scaler run` automatically.

Without a local CLI, copy and edit a JSON example, then generate with Docker:

```bash
cp examples/basic-config.json my-config.json
# Edit my-config.json before generating and applying.
docker run --rm -v "$PWD:/work" -w /work \
  ferterahadi/rmq-vertical-scaler:2 generate -c my-config.json -o scaler.yaml
kubectl apply -f scaler.yaml
```

### Generator flags

| Flag | Purpose |
|---|---|
| `-c, --config` | JSON configuration file |
| `-o, --output` | Generated YAML filename |
| `-n, --namespace` | Target Kubernetes namespace |
| `-s, --service-name` | RabbitmqCluster name; determines DNS, Secret, and PDB selector |
| `--image` | Controller image override; defaults to CLI version, or `:2` for a development build |
| `--scaler-name` | Generated ServiceAccount, Role, Deployment, and related resource names |
| `--no-pdb` | Omit the RabbitMQ PodDisruptionBudget |

## Verify a deployment

For the default resource name and a target named `my-rabbitmq` in `production`:

```bash
kubectl logs -f deployment/rmq-vertical-scaler -n production
kubectl get rabbitmqcluster my-rabbitmq -n production -o yaml
kubectl get pods -n production \
  -l app.kubernetes.io/name=my-rabbitmq -o yaml
```

Check:

- Startup mode messages, missing permissions, observed metrics, chosen profile,
  and debounce messages. Explicit `rolling` currently has no mode-selection log.
- The cluster and Pod desired requests, existing limits, and StatefulSet strategy.
- Broker effective resources in container status where exposed, resize
  conditions, readiness, container identities, and restart counts.

The log `Scaling completed successfully` means the cluster patch succeeded.
It is not a broker-level completion or message-durability check. Confirm actual
resource changes during a representative load/cooldown test before relying on
timing or performance claims.

## Experiment evidence

The motivating target was **100m → 800m → 100m CPU requests within 15 minutes**.
Two separate GKE Standard experiments on 26 October 2026 informed the project:

| Experiment | Result | Boundary |
|---|---|---|
| [Native-only, fixed-policy cycle](vpa-experiment-evidence.md#native-vpa-observation) | Five minutes of actual AMQP load plus ten minutes of cooldown on a fresh broker. Native recommendation reached 800m; applied requests stayed 100m in all 171 samples. Audit recorded zero Pod resize calls. | Native VPA missed this bounded responsiveness requirement; this does not show that VPA cannot resize StatefulSets. |
| [Fast custom recommender](vpa-experiment-evidence.md#separate-custom-recommender-observation) | GKE's VPA updater applied 100m → 800m → 100m with the same Pod/container and zero restarts. | Demonstrates the updater path with a custom recommendation policy, not native response timing. |

Neither experiment ran this repository's controller or the RabbitMQ Cluster
Operator. Only CPU requests changed; CPU limits and memory stayed fixed. They
establish neither this controller's convergence nor throughput, cost, or
lossless-delivery benefits. The [historical summary](vpa-experiment-evidence.md)
records the controls and evidence boundary; experimental scripts and raw cloud
captures remain outside the validated 2.3 scope.

## Development and further reading

The controller is a static Go binary packaged in a distroless container. Requires
Go 1.26+ to build from source:

```bash
make test     # unit tests, including v1 YAML parity
make build    # static binary in dist/
make image    # container image; requires Docker
```

| Document | Use it for |
|---|---|
| [Architecture](architecture.md) | Package layout and control loop |
| [Why Go](why-go.md) | Runtime footprint and language choice |
| [Upgrade from v1](upgrading-from-v1.md) | Migration from the Node.js controller |
| [Local testing](local-testing.md) | Running against a kind cluster |
| [Benchmarks](../research/benchmark.md) | v1/v2 measurements and methodology |
| [Changelog](../CHANGELOG.md) | Release history |
| [Go reference](https://pkg.go.dev/github.com/ferterahadi/rmq-vertical-scaler/v2/cmd/rmq-vertical-scaler) | Command package documentation |
| [Container images](https://hub.docker.com/r/ferterahadi/rmq-vertical-scaler) | Published images |

Built on the RabbitMQ Cluster Operator and
[client-go](https://github.com/kubernetes/client-go).
