# RabbitMQ Vertical Scaler v3

[Releases](https://github.com/ferterahadi/rmq-vertical-scaler/releases) · [Docker Hub](https://hub.docker.com/r/ferterahadi/rmq-vertical-scaler) · [Go documentation](https://pkg.go.dev/github.com/ferterahadi/rmq-vertical-scaler/v3)

Scale RabbitMQ CPU and memory from queue depth, publish rate, or broker utilization,
using explicit resource profiles and timing you control. Runs alongside the RabbitMQ
Cluster Operator in Kubernetes.

**v3.0.0** passed the eight-group immutable-image acceptance matrix.
Get the CLI from [GitHub Releases](https://github.com/ferterahadi/rmq-vertical-scaler/releases/tag/v3.0.0)
and use `ferterahadi/rmq-vertical-scaler:3.0.0` for the controller.
See the [release gate](docs/v3-validation.md) and
[measured results and support boundaries](docs/v3-acceptance-results.md).

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

Download and extract the CLI for your platform from the release. Rename the
executable to `rmq-vertical-scaler` and put it on your `PATH`. Configure a policy
for your existing Operator-managed RabbitMQ cluster, starting in `Off`:

```bash
rmq-vertical-scaler init --config-version 3 --output policy.json
# Edit the target namespace, cluster name, profiles and triggers.
rmq-vertical-scaler generate --config policy.json \
  --image ferterahadi/rmq-vertical-scaler:3.0.0 --output scaler.yaml
```

The versioned image is available from [Docker Hub](https://hub.docker.com/r/ferterahadi/rmq-vertical-scaler). For Helm, start with
[values-v3.example.yaml](charts/rmq-vertical-scaler/values-v3.example.yaml).
Inspect recommendations before enabling `Auto`. Use one scaler installation per
RabbitMQ cluster. See [migration](docs/v3-migration.md) and the
[policy guide](docs/v3-policy.md) for prerequisites, safe rollout and recovery.

Existing unversioned JSON and environment configurations retain their legacy
behavior. An image upgrade alone does not switch them to the v3 policy.
Use `configVersion: 3` to select the new policy.

## Configuration example

This complete [four-profile policy](docs/examples/v3-four-profile.json) combines queue depth,
publish rate, CPU and memory triggers. Adapt the example profiles to your brokers
and platform minimum requests before deploying; these are illustrative sizes.

```json
{
  "configVersion": 3,
  "profileOrder": ["LOW", "MEDIUM", "HIGH", "CRITICAL"],
  "profiles": {
    "LOW": {
      "requests": {"cpu": "500m", "memory": "1Gi"},
      "limits": {"cpu": "5", "memory": "8Gi"}
    },
    "MEDIUM": {
      "requests": {"cpu": "1000m", "memory": "2Gi"},
      "limits": {"cpu": "5", "memory": "8Gi"}
    },
    "HIGH": {
      "requests": {"cpu": "2000m", "memory": "4Gi"},
      "limits": {"cpu": "5", "memory": "8Gi"}
    },
    "CRITICAL": {
      "requests": {"cpu": "4000m", "memory": "8Gi"},
      "limits": {"cpu": "5", "memory": "8Gi"}
    }
  },
  "triggers": [
    {"type": "rabbitmq", "metadata": {
      "mode": "QueueLength", "aggregation": "max",
      "thresholds": {"MEDIUM": 2000, "HIGH": 10000, "CRITICAL": 50000}
    }},
    {"type": "rabbitmq", "metadata": {
      "mode": "MessageRate",
      "thresholds": {"MEDIUM": 200, "HIGH": 1000, "CRITICAL": 5000}
    }},
    {"type": "cpu", "metricType": "Utilization", "aggregation": "max", "metadata": {"value": "60"}},
    {"type": "memory", "metricType": "Utilization", "aggregation": "max", "metadata": {"value": "75"}}
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

Set CPU `metadata.value` to `"60"` for a 60% utilization target and memory to
`"75"` for 75%. These are percentage strings; omitting them uses the same defaults.
Utilization is the busiest broker's Kubernetes Metrics API usage divided by its
**effective resource request**, not its limit or the node's capacity. With a 500m
CPU request, 400m of sustained usage is 80%; the example therefore recommends MEDIUM
to bring utilization within the 60% target. Exactly 60% does not exceed the target.

**Triggers have no fixed priority, and their order in the array does not matter.**
Each independently recommends a profile. The highest recommendation wins
according to `profileOrder`, and that profile applies CPU and memory together
**per broker**. CPU and memory each choose the smallest profile whose request
can keep the measured usage within the configured utilization target.

With the publish-rate trigger recommending LOW:

| Queue recommendation | CPU recommendation | Memory recommendation | Selected profile |
|---|---|---|---|
| LOW | MEDIUM | LOW | MEDIUM |
| HIGH | MEDIUM | LOW | HIGH |
| LOW | LOW | CRITICAL | CRITICAL |
| MEDIUM | HIGH | HIGH | HIGH; CPU and memory tie |
| LOW | LOW | LOW | LOW recommendation; decrease one step at a time |

See the [four-profile decision walkthrough](docs/v3-policy-simulation.md) for
worked examples, threshold boundaries and the step-by-step decrease path.

The same rule includes the publish-rate trigger. Any enabled trigger can require
an increase; a decrease requires **all** enabled triggers to permit the smaller
profile. Increases must satisfy the configured 30-second stabilization window.
Queue/rate thresholds use strict `>` comparison; this example uses the deepest visible queue.

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
