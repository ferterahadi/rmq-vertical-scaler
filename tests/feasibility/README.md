# 2.3 platform feasibility probe

This is the implementation plan's Phase 0 gate. It exercises real three-broker
resource changes through the RabbitMQ Cluster Operator, without installing the
scaler. It does not prove scaler policy, recovery, timing under load, throughput,
message safety, or production compatibility.

The test deliberately diagnoses the broker's memory budget even though memory
watermarks are not scaling signals. A working-set headroom check alone cannot
show that RabbitMQ's running alarm threshold stays below its new hard limit.

## Prerequisites

Use a disposable local kind cluster with at least 4 CPUs and 6 GiB RAM in its
container runtime. The probe accepts only an explicit `kind-*` context whose API
server is on loopback. It never uses the current context implicitly.

The recorded platform uses:

- kind node image `kindest/node:v1.35.0`, Linux/arm64, cgroup v2.
- RabbitMQ Cluster Operator `2.16.1` and RabbitMQ `4.4.0-management`.
- cert-manager `v1.19.2` and Metrics Server `v0.8.0`.
- Three persistent brokers with Burstable quality of service (QoS); no explicit
  container `resizePolicy` (the Kubernetes `NotRequired` default).

Example setup, from the repository root, with Docker already running:

```bash
kind create cluster --name rmq-v23 --image kindest/node:v1.35.0 \
  --kubeconfig /private/tmp/rmq-v23-kubeconfig --wait 120s

kubectl --kubeconfig /private/tmp/rmq-v23-kubeconfig --context kind-rmq-v23 \
  apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.19.2/cert-manager.yaml
kubectl --kubeconfig /private/tmp/rmq-v23-kubeconfig --context kind-rmq-v23 \
  wait deployment --all -n cert-manager --for=condition=Available --timeout=180s
kubectl --kubeconfig /private/tmp/rmq-v23-kubeconfig --context kind-rmq-v23 \
  apply -f https://github.com/rabbitmq/cluster-operator/releases/download/v2.16.1/cluster-operator.yml
kubectl --kubeconfig /private/tmp/rmq-v23-kubeconfig --context kind-rmq-v23 \
  wait deployment/rabbitmq-cluster-operator -n rabbitmq-system \
  --for=condition=Available --timeout=180s
kubectl --kubeconfig /private/tmp/rmq-v23-kubeconfig --context kind-rmq-v23 \
  apply -f https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.8.0/components.yaml
```

For this disposable kind cluster only, permit its self-signed kubelet certificate:

```bash
kubectl --kubeconfig /private/tmp/rmq-v23-kubeconfig --context kind-rmq-v23 \
  patch deployment metrics-server -n kube-system --type=json \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
kubectl --kubeconfig /private/tmp/rmq-v23-kubeconfig --context kind-rmq-v23 \
  wait deployment/metrics-server -n kube-system --for=condition=Available --timeout=180s
```

If using Colima, point `DOCKER_HOST` at its socket for `kind` commands. Keep all
kubectl commands bound to the explicit test kubeconfig and context.

## Run and interpret

```bash
python3 -m unittest discover -s tests/feasibility -p 'test_*.py'
python3 tests/feasibility/run.py \
  --kubeconfig /private/tmp/rmq-v23-kubeconfig --context kind-rmq-v23
```

| Exit | Meaning |
|---|---|
| 0 | This fixture's request/limit cycle and running-budget checks passed. The rest of Phase 0 remains separate. |
| 1 | Not proven: setup, observation, safety, or execution failed. Inspect `error.json`. |
| 2 | The cycle completed, but the broker-budget or no-restart feasibility gate failed. Inspect `result.json` and snapshots. |

The harness creates only namespace `rmq-v23-feasibility` and its fixture. It
refuses an existing namespace by default. `--reuse` permits only the labeled,
unchanged, converged baseline after inspecting it; it does not resume a partial
cycle. Use a fresh output directory for every run.

The sequence is:

1. Wait for all three owned brokers to be ready with current effective status.
2. Observe the generated StatefulSet's `OnDelete` strategy before resource edits.
3. Update the custom resource, wait for its StatefulSet template, and submit all
   three UID/resource-version conditioned Pod resize requests concurrently.
4. Confirm desired and effective CPU/memory requests and limits, observed
   generation, readiness, and absence of active pending/in-progress conditions.
5. Record source-timestamped Metrics API memory samples for at least 60 seconds.
   Require the observed peak to fit below 80% of the proposed smaller limit.
6. Repeat the resize in the downward direction, settle for at least 60 seconds,
   and compare broker identities, restart counts, Kubernetes effective resources,
   the generated memory budget, and API-reported running memory alarm thresholds.

The fixture starts with a 1 GiB memory limit, increases it to 1.5 GiB, then
reduces it to 384 MiB. The final limit is deliberately below the initial running
alarm threshold, exposing a stale broker budget on an otherwise idle broker.
This is an isolated diagnostic, not permission for the scaler to make that change.
The 60-second probe observation is not the planned controller's five-minute
scale-down policy and does not validate safety under load.

The current harness reads `GET /api/nodes` and Kubernetes APIs only. It executes
no broker or Erlang commands and needs no `pods/exec` permission. The pinned
64-bit fixture declares relative0.6; assertions independently calculate the
Operator2.16.1 override and exact expected thresholds. No watermark update or
restart is used to make the test pass. Older retained captures used read-only
exec diagnostics for mounted files and kernel limits; those are historical proof,
not commands in the current harness.

## Evidence and cleanup

Generated output is ignored under `evidence/`. Reviewed captures live under
`results/`; [the feasibility report](../../docs/v2.3-feasibility.md) explains
their scope. Output contains no management passwords or Secret data.

The fixture remains running for inspection. Remove this disposable kind cluster
when finished (this deletes its test data):

```bash
kind delete cluster --name rmq-v23 --kubeconfig /private/tmp/rmq-v23-kubeconfig
```
