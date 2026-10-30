# RabbitMQ Vertical Scaler

Scale RabbitMQ resources from queue depth, publish rate, or CPU and memory usage,
using profiles and timing you control. Runs alongside the RabbitMQ Cluster
Operator in Kubernetes.

**2.3.x:** opt into queue/rate and CPU/memory policies with explicit requests and
limits. New policies start in recommendation-only `Off`; existing configurations
keep their behavior. See the [2.3 policy guide](docs/v2.3-policy.md).

Strict in-place policies currently require a fixed memory limit. Changing that
limit requires explicitly enabling rolling fallback, which restarts brokers.
The [final local acceptance results](docs/v2.3-acceptance-results.md) cover the
pinned platform; rolling within two minutes requires explicit deployment tuning.

## Why this exists

The motivating requirement was a load-driven CPU-request cycle:
**100m → 800m → 100m within 15 minutes**.

In a fixed-policy Google Kubernetes Engine (GKE) test, native Vertical Pod
Autoscaler (VPA) recommended 800m but the applied request stayed at 100m for
15 minutes. VPA supports StatefulSets; it did not complete this load-driven cycle
within the test window.

This project's answer is explicit RabbitMQ resource profiles with configurable
up/down delays. The experiments did **not** run this controller or the Operator;
its convergence, throughput benefit, and cost savings remain unproven by those
results. See the [experiment evidence](docs/usage.md#experiment-evidence).

## Existing configuration behavior

1. Polls the RabbitMQ management API every **5 seconds** by default.
2. Selects the highest profile whose queue-depth **or** publish-rate threshold
   is exceeded. Queue depth means the **deepest individual queue**, not the sum.
3. Waits for that recommendation to hold: **30 seconds up / 120 seconds down**.
4. Applies paired CPU/memory requests to the `RabbitmqCluster` and its brokers.

Helm's default profiles run from `330m / 2Gi` to `2400m / 8Gi` per broker.
**Limits stay unchanged.** A larger request does not raise an existing CPU or
memory limit, or prove that more CPU became usable. A successful patch is not
proof that every broker has finished resizing.

## When to use it

Use it for bursty workloads where explicit queue/rate thresholds suit your
capacity policy. A steady workload may need only a fixed resource allocation.

Before installing, have Kubernetes 1.24+ and:

- A running `RabbitmqCluster` managed by the
  [RabbitMQ Cluster Operator](https://github.com/rabbitmq/cluster-operator).
- Reachable management API and credentials; the chart uses the Operator's
  `<serviceName>-default-user` Secret by default.
- Suitable resource limits and node capacity for your largest profile.
  **Three or more brokers with quorum queues are recommended** for redundancy.

**Restart warning:** default `auto` mode can fall back to Operator rolling
restarts. In-place resizing requires `pods/resize` support and permission;
container `resizePolicy` can still require a restart. It also sets `OnDelete`,
which changes how Operator upgrades roll out. Read the
[scale-mode trade-offs](docs/usage.md#scale-modes) before enabling it.

## Start with Helm

Requires Helm, kubectl, and access to the cluster above. From a checkout:

```bash
git clone https://github.com/ferterahadi/rmq-vertical-scaler.git
cd rmq-vertical-scaler
cp charts/rmq-vertical-scaler/values.yaml scaler-values.yaml
```

Edit `scaler-values.yaml`: set `serviceName` to your existing `RabbitmqCluster`
name, then review `profiles`, `scaling.mode`, and timing for your workload.
Installation starts automatic scaling immediately.

```bash
# Replace production with the namespace containing your RabbitmqCluster.
helm upgrade --install rvs ./charts/rmq-vertical-scaler \
  --namespace production -f scaler-values.yaml

kubectl logs -f deployment/rmq-vertical-scaler -n production
```

Check the selected mode and profile in the logs, then inspect broker resources
and resize status. See [verification steps](docs/usage.md#verify-a-deployment).

## Go deeper

- [Usage guide](docs/usage.md): profiles, credentials, resize operations,
  CLI/Docker deployment, evidence, and documentation index.
- [Helm chart reference](charts/rmq-vertical-scaler/README.md): chart values.
- [2.3 policy guide](docs/v2.3-policy.md): configuration, safety, and migration.
- [2.3 runtime results](docs/v2.3-runtime-results.md): measured behavior and
  current verification limits.

Licensed under the [MIT License](LICENSE).
