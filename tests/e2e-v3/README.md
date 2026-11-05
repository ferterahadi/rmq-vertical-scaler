# v3 controller integration and acceptance

This suite runs the repository's new controller against three persistent
RabbitMQ Cluster Operator brokers on an **explicit disposable local kind
cluster**. It keeps traffic confirmations, consumed IDs, source observations,
operation state, effective resources and broker identities. It does not reuse
the native/custom GKE experiments as controller evidence.

Install the pinned platform from [the feasibility setup](../feasibility/README.md)
first: kind/Kubernetes1.35.0, Operator2.16.1, RabbitMQ4.4.0, cert-manager1.19.2,
and Metrics Server0.8.0. Use at least4 CPUs/6GiB in the local container runtime.
The probe namespace may be removed after retaining its evidence to free resources.

Build the normal scaler image from this source and load it into the test cluster:

```bash
docker build -t rmq-vertical-scaler:v3-test .
kind load docker-image rmq-vertical-scaler:v3-test --name rmq-v3
python3 -m venv /private/tmp/rmq-v3-python
/private/tmp/rmq-v3-python/bin/pip install -r tests/e2e-v3/requirements.txt
/private/tmp/rmq-v3-python/bin/python tests/e2e-v3/run.py \
  --kubeconfig /private/tmp/rmq-v3-kubeconfig --context kind-rmq-v3 \
  --scenario native --output tests/e2e-v3/evidence/native
```

When using Colima, explicitly set `DOCKER_HOST` for Docker/kind commands. The
runner never uses the normal kubeconfig implicitly. It requires Go and kubectl,
refuses an existing test namespace, and performs no broker exec or Erlang calls.
Credentials stay in memory. Strict in-place runs fail on an uncertain AMQP
publish; rolling runs use the explicit retry accounting described below.

| Scenario | Declared policy | Required result |
|---|---|---|
| `native` | CPU request100m→800m→100m; fixed1CPU limit and fixed memory | Actual controller comparison cycle, no broker replacement/restart, IDs accounted |
| `inplace` | CPU/memory requests and CPU limit vary; fixed memory ceiling | Full declared tuples become effective in both directions, no disruption |
| `blocked` | Memory limit varies; strict in-place | Entire transition blocked before broker resource/strategy writes |
| `rolling` | CPU/memory requests/limits vary; rolling explicitly allowed | Operator rolling convergence, reloaded API budget, IDs accounted; restarts reported separately |

Default workload is five minutes of real, confirmed, persistent AMQP publishes
to a quorum queue, followed by consumption with acknowledgements and a bounded
ten-minute cooldown. Default polling15s, up stabilization30s, down interval300s.
`--load-seconds` and `--down-seconds` may be reduced to at least60 for diagnostic
runs; such results are not the default/native comparison. The first observation
of sustained demand and confirmed completion come from the controller's persisted
operation timestamps. Detection delay from workload start is recorded separately.

The native comparison is on a three-broker Operator topology; the old retained
single-broker GKE native test used a different topology. A controlled comparison
still requires rerunning native VPA on the same fixture. No throughput or cost
benefit is inferred from a successful allocation cycle.

Generated evidence stays ignored under `evidence/` and `results/`. Retain reviewed
captures in a [checksummed evidence bundle](../../docs/v3-validation.md);
keep only the concise summary in Git. The runner leaves its namespace for
inspection. `fullAcceptanceMatrix:false` explicitly prevents treating any one runner as
proof of all restart, partial-write, missing-metric, fault and recovery cases in
the [plan](../../docs/v3-implementation-plan.md#acceptance-matrix).

For rolling scenarios, the harness reconnects after broker replacement and
retries an uncertain publish with the same message ID. Only a received confirmation
adds that ID to the confirmed set. Uncertain attempts and consumed duplicates are
reported; this is at-least-once accounting, not an exactly-once claim. Exhausted
retry time fails the run. Strict in-place scenarios fail immediately on uncertain
publishing. Forwarding connections reopen after convergence. Harness reconnects
do not prove that production clients automatically recover.

## Release identity

`acceptance-summary.json` records v3 readiness and currently blocks publication.
`development-evidence.json` pins the recorded development evidence without claiming a release. Use
`python3 scripts/package-v3-evidence.py --historical` only to reconstruct that older
bundle. Its result paths and image digest must never be relabelled as v3 evidence.
A fresh v3 run must record new fingerprints and a new immutable image/bundle.

## Recovery and API scope

After the native cycle returns to its baseline, reuse that labeled fixture:

```bash
/private/tmp/rmq-v3-python/bin/python tests/e2e-v3/matrix.py \
  --kubeconfig /private/tmp/rmq-v3-kubeconfig --context kind-rmq-v3 \
  --output tests/e2e-v3/evidence/recovery
/private/tmp/rmq-v3-python/bin/python tests/e2e-v3/matrix.py \
  --kubeconfig /private/tmp/rmq-v3-kubeconfig --context kind-rmq-v3 \
  --extras-only --output tests/e2e-v3/evidence/paired-and-rolling
/private/tmp/rmq-v3-python/bin/python tests/e2e-v3/scope.py \
  --kubeconfig /private/tmp/rmq-v3-kubeconfig --context kind-rmq-v3 \
  --output tests/e2e-v3/evidence/scope
```

The recovery matrix covers Off, missing metrics, scaler restart, partial resize
denial, target supersession and fresh down-step windows. Its diagnostic down
interval is 60 seconds. Extras cover paired requests/CPU limits, strict memory
blocking and explicitly authorized rolling. Run them sequentially on the same
fixture; concurrent runs would compete for its policy and message queue.

The scope runner creates temporary vhosts and a restricted user, verifies queue
totals and visibility, and accounts for fanout queue copies versus overview
ingress (including intentionally unroutable publishes). It observes a positive
selected rate through the actual controller. Exact live rate ratios are not
asserted because independently sampled rates need not align; aggregation is
also covered by controlled HTTP tests. Temporary credentials are not retained.

Before publication, the release guard checks the pinned archive checksum, safely
extracts the captures and verifies every source/image, message-accounting and
acceptance assertion. Six narrowly scoped packaging/verification amendments are
fingerprinted separately; production inputs must still match the accepted image.
The [development evidence descriptor](development-evidence.json) preserves the earlier
31-row coverage and timing results. The [v3 summary](acceptance-summary.json) records
current release blockers; it has no accepted v3 image yet.


## Bounded coverage closure

`closure.py` executes the named platform, Operator and pagination cases in the
[closure checklist](../../docs/v3-validation.md). It requires exactly
`kind-rmq-v3-closure` with a loopback API endpoint. It creates/replaces only its
labeled namespace, and uses fresh fixtures for incompatible policies. The
Operator group creates temporary admission denials restricted to that namespace
and scaler service account; cleanup tracks partially applied resources.

Run groups sequentially, with a fresh output directory per run:

```bash
# Set V3_IMAGE to the rebuilt v3 repository@sha256:digest and V3_IMAGE_DIGEST
# to its independently recorded runtime image ID before running this command.
python tests/e2e-v3/closure.py \
  --kubeconfig /private/tmp/rmq-v3-closure-kubeconfig \
  --context kind-rmq-v3-closure \
  --image "$V3_IMAGE" \
  --expected-image-id "$V3_IMAGE_DIGEST" \
  --group operator --output tests/e2e-v3/evidence/operator-fresh
```

Replace `operator` with `platform` or `metrics` for the other groups. Platform
also accepts `--platform-case qos|restart|infeasible` for affected-case reruns.
Rebuild/load the v3 source and supply its own immutable image reference and ID.
Do not reuse the earlier development image digest. The harness verifies
the actual running digest and fails immediately on a different image or live
replica overlap. The image must already be available to the disposable cluster.

The metrics group uses 502 selected durable classic queues across two vhosts
to require real pagination, with exact sum37/max20 and confirmed/consumed IDs.
These classic fixtures do not constitute quorum replication evidence.
Controlled collector/controller tests supply exact partial/stale/rate responses
separately. See [coverage results](../../docs/v3-validation.md) for tested
scope, artifact links and source-to-image provenance.

Cleanup removes the disposable kind cluster and all its test data:

```bash
kind delete cluster --name rmq-v3 --kubeconfig /private/tmp/rmq-v3-kubeconfig
```
