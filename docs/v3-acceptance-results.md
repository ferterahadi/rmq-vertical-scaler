# v3.0.1 acceptance results

**The 10 November 2026 run passed all eight runtime groups and 31 requirements
against the diagnostic-logging source.** Every group was rerun against the same
immutable image. The independent Astra source review found no release-blocking defects.

## Build and platform

The tested Linux/arm64 image is `sha256:1f2264e7d328dee36e609d0ebee061de6e6a67a855380226fedfc802f8d14b80`, built as version `3.0.1`
from source `daea60d276ff29488529a747f29f7cc769cfeac3`. Production source fingerprints, the OCI
index/manifest/config and a binary-to-container-layer comparison bind the build
to these observations. Publication builds the release images from matching source;
the published multi-platform digest is distinct from this local test-image digest.

Platform: Kubernetes **1.35.0**, RabbitMQ Cluster Operator **2.16.1**, RabbitMQ
**4.4.0-management**, Metrics Server **0.8.0**, cert-manager **1.19.2**, Linux/arm64.
Three brokers ran on one disposable kind node with 4 CPU and 6 GiB RAM. Metrics
Server used test-only `--kubelet-insecure-tls`. This local matrix does not establish
compatibility or timing for every production platform.

The [machine-readable summary](../tests/e2e-v3/acceptance-summary.json) pins the
archive and source checks. [Retrieve the full evidence](v3-evidence-bundle.md) for
policies, broker identities, effective resources, messages and fault observations.

## Results

Independent scale-up elapsed time runs from the recorded sustained-demand episode
to external confirmation of effective resources on all three brokers, including
stabilization. It is not the time a resize patch was accepted.

| Transition | Independent elapsed time | Result |
|---|---:|---|
| Native in-place CPU | 92.967967 s | Within 120 s |
| Paired resources in place | 62.528159 s | Within 120 s |
| Explicit rolling | 78.665623 s | Within 120 s with the tuning below |

“Native” means this controller's in-place path, not a new GKE VPA comparison.

| Runtime group | Passing evidence |
|---|---|
| CPU cycle | 100m → 800m → 100m; default 300-second load/down windows; 18,807 confirmed/consumed IDs; no broker replacement or restart. |
| Recovery | Seven resource-only/combined Off, missing-metrics, restart, partial-progress and supersession cases; 2,600 accounted IDs. |
| Deferred capacity | Real kubelet Deferred; committed revision and successful submission attempts retained until capacity returned; 100 accounted IDs. |
| Paired / strict / rolling | Paired requests and CPU limits in place; strict memory-limit changes blocked before writes; explicit rolling reloads budgets both ways; 12,704 accounted IDs. |
| API scope | Admin depth 50 versus restricted 20; 110 ingress / 200 fanout copies; 250 durable queue copies accounted. |
| Platform constraints | QoS and RestartContainer policies blocked before mutation; real kubelet Infeasible retains the target without implicit rolling; 150 accounted IDs. |
| Operator recovery | Six interrupted-write, partial-progress, replacement and external-drift cases; 300 accounted IDs. |
| Pagination | Two real API pages, 502 selected queues, sum 37 / maximum 20; 37 accounted IDs. |

All named groups used fresh captures. Resource projection checks use Off; they are
not resource-driven Auto pressure tests. Controlled tests establish failure and
arithmetic semantics without claiming real provider failures.

Local checks passed: 306 Go test cases under race detection across 10 packages,
`go vet`, binary build, Helm lint/render and 51 Python harness/evidence tests.
The independent Astra review also ran 11 focused top-level regression tests under
race detection. The retained review describes its scope and limitations; it is
not a formal GitHub approval.

## Diagnostic logging

A local container with `LOG_LEVEL=debug` emitted one structured observation per
15-second poll. Captures include `FreshMemoryRequired` while metrics were denied
and `ObservingDecrease` with elapsed and required window seconds after a restart.
The review and regression tests checked that logging reads existing state without
advancing or resetting decision windows. Initial read failures remain error logs.
Debug logging overhead and storage cost are unmeasured.

## Rolling prerequisites and limits

Healthy rolling timing uses explicit fixture settings:

- Operator `delayStartSeconds: 0`.
- TCP 5672 readiness: initial delay 1 s, period 2 s, timeout 5 s, failure threshold 3.
- CoreDNS TTL/cache 5 s, with success/denial caching disabled for `cluster.local`.
- Headless service publication of not-ready addresses.

The normal quorum-aware preStop and sequential Operator replacement remain.
The scaler does not apply this tuning. Revalidate the 120-second objective on the
intended platform; node capacity can defer increases beyond it.

Strict in-place **memory-limit changes remain unsupported**. Keep a fixed memory
limit or explicitly permit rolling. Read-only RabbitMQ HTTP API observations
verify broker-budget reload on rolling. Changing Kubernetes requests alone does
not increase RabbitMQ's configured memory watermark. No broker exec was used.

Readiness is sampled. Confirmed/consumed IDs and uncertain publish retries do not
establish exactly-once delivery or uninterrupted client availability. Cost savings
and throughput gains are unmeasured. This matrix makes no production latency guarantee.

## Evidence continuity

The v3.0.0 result remains historical evidence. Its image does not contain the new
diagnostic paths, so v3.0.1 rebuilt the image and repeated the complete matrix.
The current source/image fingerprints and checked archive replace the earlier pin.
Documentation and archive-location updates do not change the tested controller or
runtime harness. Retrieval and retention details are in the [bundle guide](v3-evidence-bundle.md).
