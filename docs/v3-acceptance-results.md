# v3.0.0 acceptance results

**The 6 November 2026 local run passed all eight runtime groups, nine evidence gates
and 31 requirements.** R3 is verified. This is pre-release evidence; v3.0.0 has not
been published and production compatibility remains unverified.

## Build and platform

The tested Linux/arm64 image is
`sha256:4f05710ecb2ebf8823fbd9d5c91ed965a86dbb01e0fe7e11d435f6e8d8519628`,
built as version `3.0.0` from source
`f0d92a661fdae91ad6db03411239da9e7cf7623a`.
All 28 production inputs and five runtime drivers still match that build. The later
controlled source snapshot refresh changes one nonproduction manifest test; current
Go checks cover it. Documentation and narrowly reviewed evidence-retention changes
do not change the tested binary.

Platform: Kubernetes **1.35.0**, RabbitMQ Cluster Operator **2.16.1**, RabbitMQ
**4.4.0-management**, Metrics Server **0.8.0**, cert-manager **1.19.2**, Linux/arm64.
Three brokers ran on one local kind node with 4 CPU and about 6 GiB RAM. Metrics
Server used test-only `--kubelet-insecure-tls`. Other versions, architectures and
production topologies remain unverified.

The [machine-readable summary](../tests/e2e-v3/acceptance-summary.json) pins the
archive and source checks. [Retrieve the full evidence](v3-evidence-bundle.md) for
policies, identities, resource observations, logs and message accounting.

## Results

Scale-up timing starts at the first independently observed sustained overload and
ends when all brokers have effective target resources. It includes stabilization.

| Transition | Independent elapsed time | Result |
|---|---:|---|
| Native in-place CPU | 77.532247 s | Within 120 s |
| Paired resources in place | 78.452323 s | Within 120 s |
| Explicit rolling | 110.227682 s | Within 120 s with the tuning below |

Here “native” means this controller's in-place path, not a new GKE VPA comparison.

| Runtime group | Evidence |
|---|---|
| CPU cycle | 100m → 800m → 100m; default 300-second load/down intervals; 18,669 message IDs accounted once; no broker replacement or restart. |
| Recovery | Seven cases; 2,600 IDs; retained revisions/targets/attempts; restarted down window 105.20 s and subsequent step gap 105.00 s. |
| Deferred capacity | Actual kubelet Deferred on all three brokers; revision 9 / attempt 1 retained; 100 IDs. |
| Paired / strict / rolling | Paired requests and CPU limits in place; strict memory change blocked; rolling reloads broker budgets both ways; 12,310 IDs, one uncertain publish retry and zero observed duplicates. |
| API scope | Admin queue total 50 vs restricted 20; restricted overview unavailable; 110 ingress messages / 200 fanout copies with exact per-scope accounting. |
| Platform constraints | QoS and RestartContainer policies blocked before mutation; actual kubelet Infeasible retains target without implicit rolling; 50 IDs per case. |
| Operator recovery | Six interrupted-write, partial-progress, replacement and external-drift cases; 300 IDs; successful attempts retained, replacement adopted, external drift held. |
| Pagination | Two actual RabbitMQ API pages, 502 selected queues, summed depth 37 / maximum 20; all 37 IDs consumed once. |

Supplementary live checks matched Metrics API timestamps, windows, CPU and memory
to all three current broker containers. Four `Off` policies exercised CPU-only,
memory-only, paired and combined resource projections; independently recomputed
busiest-resource maxima matched, with no resource or strategy writes. These used
low test targets and do not establish resource-driven `Auto` load-cycle performance.

Refreshed controlled checks passed: 282 Go test cases under the race detector,
`go vet`, module tidy diff, Helm lint/render, 51 Python harness/evidence tests and
12 feasibility tests. Controlled API/client tests establish error and arithmetic
semantics; they do not imply real provider failures occurred in the cluster.
GPT-6 Astra at high reasoning reviewed the source, runtime evidence and final
upload/download integrity and found no actionable issue. This is internal review,
not a formal GitHub approval.

## Rolling prerequisites and support limits

The rolling result used explicit fixture tuning:

- Operator `delayStartSeconds: 0`.
- Existing TCP port 5672 readiness handler: initial delay 1 s, period 2 s,
  timeout 5 s and failure threshold 3.
- CoreDNS TTL/cache 5 s, with success/denial caching disabled for `cluster.local`.
- Headless service publication of not-ready addresses.

The Operator's normal quorum-aware preStop and sequential replacement remained in
place. The scaler does not apply these tunings. Earlier default-setting timing
misses remain development diagnostics; revalidate the 120-second objective on the
intended setup.

Strict in-place **memory-limit changes remain unsupported**. They block before
resource/strategy writes; keep a fixed memory limit or explicitly permit rolling.
Read-only RabbitMQ HTTP API `/api/nodes` `mem_limit` observations confirmed that
rolling reloads broker budgets. No broker exec or Erlang calculation was used.

Readiness/availability was sampled, not continuously proven for clients. Accounted
IDs, one uncertain retry and zero observed duplicates do not establish exactly-once
delivery. Throughput gains, cost savings and production behavior remain unmeasured.
The disposable test cluster was removed after traffic stopped and operations were idle.

## Evidence continuity

Two orchestration interruptions are retained with their original logs and sources.
An obsolete namespace assertion stopped the coordinator after five successful
groups; a guarded continuation verified the same source/image and completed the
remaining three. A supplementary observer then missed a transient simultaneous
queue/provider window; a separate post-drain retake verified the live provider
mapping. It makes no simultaneous queue-total claim. Neither interruption changed
the compiled sources or relabelled failed observations as passed.

The original runtime source snapshot remains intact. A separate validated snapshot
records the subsequent test-only cleanup and refreshed controlled checks. Astra
verified all production/runtime-driver hashes and 229 runtime/projection files
unchanged across that refresh. The archive records both snapshots and the review.

The [downloadable bundle](v3-evidence-bundle.md) was retrieved independently and
passed its checksum, exact membership and full semantic gate. GitHub CI repeats
source checks and that evidence gate. Publication and merge are separate actions.
