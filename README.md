# RabbitMQ Vertical Scaler v3

Scale RabbitMQ CPU and memory from queue depth, publish rate, or broker utilization,
using explicit resource profiles and timing you control. Runs alongside the RabbitMQ
Cluster Operator in Kubernetes.

**v3.0.0 is unreleased.** An earlier immutable image passed the eight-group runtime
acceptance matrix. The subsequent collector fix requires fresh full acceptance
before release. See the [current release gate](docs/v3-validation.md) and
[earlier results and support boundaries](docs/v3-acceptance-results.md).

## What changes in v3

- Declare CPU/memory requests and limits together, with queue/rate or resource triggers.
- Start in recommendation-only `Off`; enable `Auto` when the policy is ready.
- Scale up on sustained demand and down one profile at a time, with fresh memory checks.
- Track desired and effective resources on every broker until the change completes.

Defaults: CPU **60%**, memory **75%** of effective requests, **15-second** polling,
**30-second** upward stabilization, and **5 minutes** per downward step.
Strict in-place operation requires a fixed memory limit on the tested platform;
changing it requires explicit rolling fallback and broker restarts.

## Switch to v3

Build this branch until v3.0.0 is published. Configure a policy for your existing
Operator-managed RabbitMQ cluster, starting in `Off`:

```bash
make build VERSION=3.0.0
dist/rmq-vertical-scaler init --config-version 3 --output policy.json
# Edit the target namespace, cluster name, profiles and triggers.
dist/rmq-vertical-scaler generate --config policy.json \
  --image rmq-vertical-scaler:local --output scaler.yaml
```

Build/load the chosen image before applying the manifest. For Helm, start with
[values-v3.example.yaml](charts/rmq-vertical-scaler/values-v3.example.yaml).
Inspect recommendations before enabling `Auto`. Use one scaler installation per
RabbitMQ cluster. See [migration](docs/v3-migration.md) and the
[policy guide](docs/v3-policy.md) for prerequisites, safe rollout and recovery.

Existing unversioned JSON and environment configurations retain their legacy
behavior. An image upgrade alone does not switch them to the v3 policy.
Use `configVersion: 3` to select the new policy.

## Configuration example

This complete [combined policy](examples/v3/combined.json) enables queue depth,
publish rate, CPU and memory triggers. Adapt the example profiles to your brokers
and platform minimum requests before deploying; these are illustrative sizes.

```json
{
  "configVersion": 3,
  "profileOrder": ["LOW", "HIGH"],
  "profiles": {
    "LOW": {
      "requests": {"cpu": "100m", "memory": "256Mi"},
      "limits": {"cpu": "1", "memory": "1Gi"}
    },
    "HIGH": {
      "requests": {"cpu": "800m", "memory": "512Mi"},
      "limits": {"cpu": "2", "memory": "1Gi"}
    }
  },
  "triggers": [
    {"type": "rabbitmq", "metadata": {
      "mode": "QueueLength", "aggregation": "sum", "thresholds": {"HIGH": 2000}
    }},
    {"type": "rabbitmq", "metadata": {
      "mode": "MessageRate", "thresholds": {"HIGH": 200}
    }},
    {"type": "cpu", "metricType": "Utilization", "metadata": {}},
    {"type": "memory", "metricType": "Utilization", "metadata": {}}
  ],
  "pollingInterval": 15,
  "updatePolicy": {"mode": "Off", "allowRollingFallback": false},
  "behavior": {
    "scaleUp": {"stabilizationSeconds": 30},
    "scaleDown": {"stepIntervalSeconds": 300, "memoryHeadroomPercent": 20},
    "metrics": {"maxAgeSeconds": 60}
  },
  "kubernetes": {"namespace": "production", "rmqServiceName": "rabbitmq"},
  "rmq": {"host": "rabbitmq.production.svc.cluster.local", "port": "15672"}
}
```

The highest recommendation wins, and each profile applies CPU and memory together
**per broker**. CPU and memory use the busiest broker's Kubernetes Metrics API
usage against its effective requests, with default targets of 60% and 75%.
Set `metadata.value` to a percentage string to change either target. Queue/rate
thresholds use strict `>` comparison; this example sums visible queue depths.

Start in `Off` to inspect recommendations. After verification, `Auto` permits
resource changes. Decreases require all enabled signals to permit the lower
profile and a full fresh memory window. Keep RabbitMQ credentials in a Kubernetes
Secret, supplied through `RMQ_USER` and `RMQ_PASS`, outside this JSON.
See the [policy guide](docs/v3-policy.md) for selectors, limits and safety rules.

## Reduce overprovisioning on GKE Autopilot

Lower resource requests during quieter periods can reduce RabbitMQ compute costs.
For general-purpose workloads using GKE Autopilot's **Pod-based billing**, Google
charges for the CPU, memory and ephemeral storage that running Pods request.
Scaling CPU and memory requests down can therefore reduce those billable resources
while a lower profile is active. See [Google's pricing documentation](https://cloud.google.com/kubernetes-engine/pricing).

Lower utilization alone does not lower request-based charges. Savings depend on
the time spent at each profile, applicable minimum requests, pricing and discounts,
and the scaler's own resource cost. Autopilot also has node-based billing options;
the same request reduction does not imply the same bill reduction there. Fixed
storage and other charges remain. Actual billing savings from this controller
are **unmeasured**, and capacity constraints can delay increases.

## Why this exists

The motivating requirement was a load-driven CPU-request cycle:
**100m → 800m → 100m within 15 minutes**. In a fixed-policy Google Kubernetes Engine
(GKE) test, native Vertical Pod Autoscaler (VPA) recommended 800m but did not apply
it during that window. VPA supports StatefulSets; the experiment established a timing
limitation in that setup, not a lack of StatefulSet support.

This controller provides explicit profiles, configurable timing and observable
resize completion. Production compatibility, throughput gains and cost savings
remain unmeasured. See the [experiment record](docs/usage.md#experiment-evidence).

## Documentation

- [v3 policy](docs/v3-policy.md): configuration, metrics, safeguards and reconciliation.
- [v3 migration](docs/v3-migration.md): version names, compatibility and release gates.
- [Legacy usage](docs/usage.md): retained unversioned configuration and deployment behavior.
- [Helm reference](charts/rmq-vertical-scaler/README.md): policy and legacy values.
- [Repository structure](docs/architecture.md#repository-structure): code, tests and evidence.
- [Acceptance results](docs/v3-acceptance-results.md): measured behavior and support boundaries.
- [Evidence bundle](docs/v3-evidence-bundle.md): retrieve and verify retained captures.

Licensed under the [MIT License](LICENSE).
