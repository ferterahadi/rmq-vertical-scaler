# v1 (Node.js) vs v2 (Go) — Footprint Benchmark

> **Thesis:** v2 is *not* faster. This tool is an idle-99.9%, I/O-bound control
> loop — a GET every few seconds and an occasional PATCH. The win is **resource
> footprint**, which is the whole point of a tool that exists to *save* cluster
> resources. We measure idle memory, image size, and steady-state behaviour, not
> throughput.

## Methodology

Run both versions against the **same** RabbitMQ cluster, with the **same**
generated config (same profiles/thresholds/interval), in the **same** namespace,
under the **same** synthetic load profile. Deploy them side by side (different
`--scaler-name`) so they observe identical metrics.

Metrics, and how to capture each:

| Metric | How to measure | Tool |
|---|---|---|
| **Idle RSS** | Steady-state working set of the scaler pod over a 24h idle run | `kubectl top pod` and/or cgroup `memory.current` |
| **Container image size** | On-disk and compressed (pull) size | `docker images` / registry |
| **Cold start** | Container start → first completed scaling decision (log line) | pod logs + start timestamp |
| **Idle CPU** | Steady-state CPU of the scaler pod | `kubectl top pod` |
| **24h memory stability** | RSS sampled over 24h — flat (Go) vs sawtooth GC; check for leaks | periodic `kubectl top pod` |

`scripts/benchmark.sh` automates the image-size, cold-start, and `kubectl top`
sampling steps; run it after both versions are deployed. Raw samples land under
`research/` as CSVs.

## Expected results (hypotheses to verify on a cluster)

| Metric | v1 (Node 20) | v2 (Go) | Expected change |
|---|---|---|---|
| Idle RSS | ~50–80 MB (V8 heap + runtime) | ~10–15 MB | **~70–80% smaller** |
| Idle CPU | low, but non-zero (V8 GC/timers) | negligible | smaller |
| 24h memory | sawtooth (V8 GC) | flat | more predictable |
| Cold start | node startup + require graph | single static binary | faster |

## Measured so far (no cluster/Docker required)

These are the figures obtainable on a dev machine without a live cluster:

| Metric | v1 (Node.js) | v2 (Go) | Change |
|---|---|---|---|
| Build artifact (deps/binary) | `node_modules` **154 MB** | static binary **40.1 MiB** | — |
| Compressed image layer (binary) | n/a (runtime + modules) | **10.6 MiB** gzipped | — |
| Runtime base image | `node:20-alpine` ~130 MB | `distroless/static:nonroot` ~2 MB | **~98% smaller base** |
| Approx. image (base + payload) | ~150–280 MB | **~42 MB** | **~70–85% smaller** |
| Runtime dependencies | axios, @kubernetes/client-node, commander | client-go (one real dep) + stdlib | fewer, vendored at build |

> **Honest note on image size:** `client-go` is a large dependency, so the v2
> binary (~40 MiB) is bigger than a trivial Go program. The headline win is still
> **idle RSS** and **attack surface** (no shell, no package manager, no
> interpreted runtime), not a 10 MB image. Don't oversell the image number.

## Pending (require a Kubernetes cluster + RabbitMQ Cluster Operator)

- Idle RSS over a 24h steady run (v1 and v2).
- Idle CPU (`kubectl top pod`).
- Cold-start latency from logs.
- 24h memory-stability curve (leak check).

Fill the "Expected results" table with measured numbers once a cluster is
available; see `scripts/benchmark.sh`.
