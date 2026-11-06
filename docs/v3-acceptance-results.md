# v3.0.0 acceptance results

**The refreshed 6 November 2026 run passed all eight runtime groups and 31
requirements on the latest collector source.** The source-linked release gate is
verified. These measurements were collected before publication of v3.0.0.

## Build and platform

The tested Linux/arm64 image is `sha256:7b3ef6c21c7ddc905848fb68e2e02ad9084e42d03949c4d498505da0bfa8409a`, built as version `3.0.0`
from source `d669c5ae15f1dfdbb6eaa30848b61e9e5ea25eca`. The complete production source fingerprint,
OCI index/manifest/config, binary-layer hash and an independent comparison with
the container filesystem link this build to the runtime observations.
Every scaler installation was checked against the same immutable digest.

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
| Native in-place CPU | 79.642344 s | Within 120 s |
| Paired resources in place | 64.599429 s | Within 120 s |
| Explicit rolling | 92.540565 s | Within 120 s with the tuning below |

“Native” means this controller's in-place path, not a new GKE VPA comparison.

| Runtime group | Passing evidence |
|---|---|
| CPU cycle | 100m → 800m → 100m; default 300-second load/down windows; 20,484 confirmed/consumed IDs; no broker replacement or restart. |
| Recovery | Seven cases, including resource-only/combined Off, missing metrics, restart, partial progress and supersession; 2,600 accounted IDs. |
| Deferred capacity | Real kubelet Deferred; committed revision and successful submission attempts retained until capacity returned; 100 accounted IDs. |
| Paired / strict / rolling | Paired requests and CPU limits in place; strict memory-limit change blocked before writes; explicit rolling reloads budgets both ways; 13,055 accounted IDs. |
| API scope | Admin depth 50 versus restricted 20; selected rate and global ingress remain distinct; 110 ingress / 200 fanout copies, plus scoped depth fixtures; 250 durable queue copies accounted. |
| Platform constraints | QoS and RestartContainer policies blocked before mutation; real kubelet Infeasible retains target without implicit rolling; 150 accounted IDs across three cases. |
| Operator recovery | Six interrupted-write, partial-progress, replacement and external-drift cases; 300 accounted IDs. |
| Pagination | Two real API pages, 502 selected queues, sum 37 / maximum 20; 37 accounted IDs. |

All named groups were rerun against the new image; predecessor captures were not
relabelled. Raw confirmed and consumed IDs were compared separately from the
harness summaries. Retained resource signals were independently recomputed from
Kubernetes CPU/memory samples and effective requests. These local resource
projection checks use Off; they are not resource-driven Auto pressure tests.

Current local checks passed: 285 Go test cases under race detection, `go vet`,
module tidy diff, Helm lint/render, 51 Python harness/evidence tests and 12
feasibility tests. Controlled tests establish error and arithmetic semantics;
they do not imply that every simulated provider error occurred in the cluster.
The retained review records its reviewer, scope and limitations; it is not a
formal GitHub approval.

## Rolling prerequisites and limits

Healthy rolling timing uses explicit fixture settings:

- Operator `delayStartSeconds: 0`.
- TCP 5672 readiness: initial delay 1 s, period 2 s, timeout 5 s, failure threshold 3.
- CoreDNS TTL/cache 5 s with success/denial caching disabled for `cluster.local`.
- Headless service publication of not-ready addresses.

The normal quorum-aware preStop and sequential Operator replacement remain.
The scaler does not apply this tuning. Revalidate the 120-second objective on
the intended platform; node capacity can defer increases beyond it.

Strict in-place **memory-limit changes remain unsupported**. Keep a fixed memory
limit or explicitly permit rolling. Read-only RabbitMQ HTTP API observations
verify broker-budget reload on rolling. Changing Kubernetes requests alone does
not increase RabbitMQ's configured memory watermark. No broker exec was used.

Readiness is sampled. The rolling publisher recorded 1 uncertain publish attempt,
retained in the raw evidence; all confirmed IDs were accounted for, with zero
observed duplicates. Confirmed/consumed IDs and retry accounting do not establish
exactly-once delivery or uninterrupted client availability. Cost savings and
throughput gains are unmeasured. This matrix makes no universal production
latency guarantee and performs no production deployment.

## Evidence continuity

The previous image's acceptance remains a historical result. The queue-total
collector correction invalidated its authority for current sources, so this run
rebuilt the image and repeated every group. Its current source/image fingerprints
and independently verified bundle replace the earlier release pin.

The full archive retains setup logs and any interrupted or failed attempts,
with successful qualification clearly distinguished. Evidence retrieval and
retention details are in the [bundle guide](v3-evidence-bundle.md).

Publication preparation updated installation documentation only. The chart README
fingerprint was refreshed after review and fresh local checks; all 28 production
inputs and captured runtime drivers still match the tested image. The eight
runtime groups above were retained unchanged, not represented as new runs.
