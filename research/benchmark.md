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

## Measured (v2 Go)

Build/image figures (host) plus a live minikube run (1-replica RabbitMQ Cluster
Operator cluster, scaler idle at the LOW profile), 2026-06-13:

| Metric | v1 (Node.js) | v2 (Go) | Source |
|---|---|---|---|
| **Idle RSS** | ~50–80 MB (documented baseline) | **8 MiB** | `kubectl top pod` (idle, LOW) |
| **Idle CPU** | non-trivial (V8) | **1m** (~0) | `kubectl top pod` |
| Container image (on disk) | ~150–280 MB | **59.9 MB** | `docker build` + `docker images` |
| Static binary | n/a | 40.1 MiB (10.6 MiB gz) | `go build -ldflags="-s -w"` |
| Build deps | `node_modules` 154 MB | client-go + stdlib | — |
| Runtime base image | `node:20-alpine` ~130 MB | `distroless/static:nonroot` ~2 MB | — |

**Idle RSS dropped from a documented ~50–80 MB (Node) to a measured 8 MiB (Go)
— a ~6–10× reduction**, which is the headline result for a tool whose purpose
is saving cluster resources. For reference the RabbitMQ pod it manages used
88 MiB / 10m at the same moment.

> **Honest note on image size:** `client-go` is a large dependency, so the v2
> binary (~40 MiB) and image (~60 MB) are bigger than a trivial Go program. The
> real wins are **idle RSS** and **attack surface** (no shell, no package
> manager, no interpreted runtime) — not a 10 MB image. Don't oversell the image.

### Functional validation on the live cluster
The full control loop was exercised end-to-end against the real RabbitMQ
operator CRD:
- Backlog crossed the HIGH queue threshold → after the 10s scale-up debounce the
  scaler JSON-patched the `RabbitmqCluster` from LOW (100m/512Mi) → HIGH
  (400m/1Gi).
- Queue purged → after the 20s scale-down debounce it patched back to LOW.
- Confirms metrics fetch, profile selection, asymmetric debounce, ConfigMap
  stability tracking, and the JSON-Patch shape against a live operator.

## Pending (longer-running, optional)

- 24h memory-stability curve (leak check) — needs a sustained run.
- Cold-start latency captured precisely from timestamps.
- Strict v1-vs-v2 side-by-side (needs a published v1 image to deploy alongside).

Use `scripts/benchmark.sh` to automate the sampling.
