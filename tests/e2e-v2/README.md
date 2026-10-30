# Version-2 actual-controller integration

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
docker build -t rmq-vertical-scaler:v2.3-test .
kind load docker-image rmq-vertical-scaler:v2.3-test --name rmq-v23
python3 -m venv /private/tmp/rmq-v23-python
/private/tmp/rmq-v23-python/bin/pip install -r tests/e2e-v2/requirements.txt
/private/tmp/rmq-v23-python/bin/python tests/e2e-v2/run.py \
  --kubeconfig /private/tmp/rmq-v23-kubeconfig --context kind-rmq-v23 \
  --scenario native --output tests/e2e-v2/evidence/native
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

Generated evidence stays ignored under `evidence/`. Review and retain sanitized
results with checksums under `results/`. The runner leaves its namespace for
inspection. `fullAcceptanceMatrix:false` explicitly prevents treating any one runner as
proof of all restart, partial-write, missing-metric, fault and recovery cases in
the [plan](../../docs/v2.3-implementation-plan.md#acceptance-matrix).

For rolling scenarios, the harness reconnects after broker replacement and
retries an uncertain publish with the same message ID. Only a received confirmation
adds that ID to the confirmed set. Uncertain attempts and consumed duplicates are
reported; this is at-least-once accounting, not an exactly-once claim. Exhausted
retry time fails the run. Strict in-place scenarios fail immediately on uncertain
publishing. Forwarding connections reopen after convergence. Harness reconnects
do not prove that production clients automatically recover.

## Recovery and API scope

After the native cycle returns to its baseline, reuse that labeled fixture:

```bash
/private/tmp/rmq-v23-python/bin/python tests/e2e-v2/matrix.py \
  --kubeconfig /private/tmp/rmq-v23-kubeconfig --context kind-rmq-v23 \
  --output tests/e2e-v2/evidence/recovery
/private/tmp/rmq-v23-python/bin/python tests/e2e-v2/matrix.py \
  --kubeconfig /private/tmp/rmq-v23-kubeconfig --context kind-rmq-v23 \
  --extras-only --output tests/e2e-v2/evidence/paired-and-rolling
/private/tmp/rmq-v23-python/bin/python tests/e2e-v2/scope.py \
  --kubeconfig /private/tmp/rmq-v23-kubeconfig --context kind-rmq-v23 \
  --output tests/e2e-v2/evidence/scope
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

Before publication, the release guard requires complete canonical source
fingerprints plus a fingerprinted `results/gates/<gate>.json` for every required
gate. Each report contains `gate`, `passed:true` and a nonempty `evidence` list
of retained, fingerprinted supporting files. `releaseReady` stays false until
remaining acceptance and immutable-build provenance work is complete.

## Bounded coverage closure

`closure.py` executes the named platform, Operator and pagination cases in the
[closure checklist](../../docs/v2.3-coverage-closure.md). It requires exactly
`kind-rmq-v23-closure` with a loopback API endpoint. It creates/replaces only its
labeled namespace, and uses fresh fixtures for incompatible policies. The
Operator group creates temporary admission denials restricted to that namespace
and scaler service account; cleanup tracks partially applied resources.

Run groups sequentially, with a fresh output directory per run:

```bash
python tests/e2e-v2/closure.py \
  --kubeconfig /private/tmp/rmq-v23-closure-kubeconfig \
  --context kind-rmq-v23-closure \
  --image docker.io/library/rmq-vertical-scaler@sha256:04c17e5cd431ddbd5d03c7431985a28164f0f4b9e1483dc72e12943cb4df2e21 \
  --expected-image-id sha256:04c17e5cd431ddbd5d03c7431985a28164f0f4b9e1483dc72e12943cb4df2e21 \
  --group operator --output tests/e2e-v2/evidence/operator-fresh
```

Replace `operator` with `platform` or `metrics` for the other groups. Platform
also accepts `--platform-case qos|restart|infeasible` for affected-case reruns.
The digest above is the retained local Linux/arm64 build; rebuild/load the source
and supply its own immutable digest to reproduce elsewhere. The harness verifies
the actual running digest and fails immediately on a different image or live
replica overlap. The image must already be available to the disposable cluster.

The metrics group uses 502 selected durable classic queues across two vhosts
to require real pagination, with exact sum37/max20 and confirmed/consumed IDs.
These classic fixtures do not constitute quorum replication evidence.
Controlled collector/controller tests supply exact partial/stale/rate responses
separately. See [coverage results](../../docs/v2.3-coverage-results.md) for tested
scope, artifact links and source-to-image provenance.

Cleanup removes the disposable kind cluster and all its test data:

```bash
kind delete cluster --name rmq-v23 --kubeconfig /private/tmp/rmq-v23-kubeconfig
```
