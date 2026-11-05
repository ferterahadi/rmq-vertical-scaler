# RabbitMQ Vertical Scaler v3

Scale RabbitMQ CPU and memory from queue depth, publish rate, or broker utilization,
using explicit resource profiles and timing you control. Runs alongside the RabbitMQ
Cluster Operator in Kubernetes.

**v3.0.0 is unreleased.** The v3 source still needs a complete acceptance run on one
immutable image. Earlier development results do not validate this release.

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
- [Validation status](docs/v3-validation.md): release requirements and support boundaries.

Licensed under the [MIT License](LICENSE).
