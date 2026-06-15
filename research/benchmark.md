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

## Measured — real v1-vs-v2 side by side

Both versions built and deployed to the **same** minikube cluster, same config,
both idle at the LOW profile after a fresh restart + 2 min settle, 2026-06-14.
v1 = the `1.0.2` Node image built from the `v1.0.2` tag; v2 = the `2.0.0` Go image.

| Metric | v1 (Node 1.0.2) | v2 (Go 2.0.0) | Change | Source |
|---|---|---|---|---|
| **Idle RSS** | **45 MiB** | **5 MiB** | **~89% less (9×)** | `kubectl top pod` |
| **Idle CPU** | 2m | 1m | lower | `kubectl top pod` |
| **Container image** | **351 MB** | **59.9 MB** | **~83% less** | `docker images` |
| **Cold start** (pod start → first metrics activity) | ~4.4 s | ~0.76 s | **~6× faster** | pod `startTime` vs first log ts |
| Static binary / bundle | webpack `dist` + `node_modules` | single 40.1 MiB static binary | — | — |
| Runtime base image | `node:20-alpine` ~194 MB | `distroless/static:nonroot` ~6 MB | — | `docker images` |

**Idle RSS dropped from 45 MiB (Node) to 5 MiB (Go) — a 9× reduction**, which is
the headline result for a tool whose whole purpose is saving cluster resources.
(Under sustained activity the Go process settles higher — observed ~33 MiB after
an 8 h run with a scaling cycle — still well below Node's idle baseline.) For
reference the RabbitMQ pod it manages used ~45–88 MiB at the same time, i.e. v2
is now negligible next to its workload. Both versions made identical scaling
decisions and showed the same empty-queue skip behaviour.

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

Use `scripts/benchmark.sh` to automate the sampling.
