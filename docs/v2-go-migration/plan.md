# Plan — rmq-vertical-scaler v2 (Go rewrite)

## Goal

Ship `rmq-vertical-scaler` v2.0.0: a behaviour-identical Go rewrite of the current
Node.js Kubernetes control loop, distributed as a tiny static binary + `FROM scratch`
container, with a benchmark proving a materially smaller memory and image footprint
than v1, and a README that presents it as a professional, ecosystem-native K8s tool.

## Why Go (the actual justification — read this before doubting mid-rewrite)

This tool is a **low-frequency, I/O-bound Kubernetes control loop**: every ~5s it does
an HTTP GET against the RabbitMQ management API, compares a handful of numbers, and
maybe issues a K8s PATCH. It is idle ~99.9% of the time.

**Go is NOT being chosen for speed** — the workload is already fast enough and the
bottleneck is the network call + the sleep interval. A Go rewrite will not make any
scaling decision perceptibly faster. Do not justify this work on "performance."

Go IS being chosen because, for a K8s operator specifically, it wins on the things that
actually matter for this tool:

1. **Memory footprint** — Node idles at ~50–80 MB RSS; an equivalent Go binary idles at
   ~8–15 MB. This tool's entire pitch is *saving cluster resources* — a fat runtime
   sidecar undercuts that pitch. This is the headline benchmark.
2. **Container image** — Node image ~120–180 MB (runtime + node_modules); Go
   `FROM scratch`/distroless ~10–20 MB. Faster pulls, smaller attack surface, fewer CVEs.
3. **Ecosystem fit / credibility** — `client-go` is the official, first-class K8s client;
   the entire operator ecosystem is Go. `@kubernetes/client-node` is a second-class
   citizen that lags. A professional K8s tool is expected to be Go.
4. **Dependency hygiene** — stdlib `net/http` replaces axios; client-go is the one real
   dependency. Fewer moving parts for a long-lived infra tool.
5. **Distribution** — single cross-compiled static binary, `go install`, no "have Node 18+?"

Cost is low: the entire codebase is ~830 LOC (506 lib + 324 CLI). This is a days-not-months
rewrite.

## Context (what a cold session needs to know)

### What v1 is (current, Node.js, on `master`)
Two surfaces in one package:
- **Runtime controller** (`lib/`, ~506 LOC) — runs in-cluster as a Deployment, the scaling loop.
- **CLI manifest generator** (`bin/rmq-vertical-scaler`, ~324 LOC) — runs locally / via `npx`,
  reads a JSON config and emits the K8s YAML (ServiceAccount, Role, RoleBinding, ConfigMap,
  Deployment) that you `kubectl apply`.

### Runtime behaviour (must be reproduced EXACTLY in Go)
Control loop in `lib/RabbitMQVerticalScaler.js`:
1. `waitForRabbitMQ()` — retry GET `/api/overview` until reachable.
2. Every `CHECK_INTERVAL_SECONDS` (default 5):
   a. `MetricsCollector` — GET `http://{RMQ_HOST}:{RMQ_PORT}/api/overview` and `/api/queues`
      with basic auth (`RMQ_USER`/`RMQ_PASS`), 10s timeout. On error, return empty → loop skips.
   b. `ScalingEngine.calculateScaleProfile()` — extract `totalMessages`
      (`queue_totals.messages`), `messageRate` (`message_stats.publish_details.rate`),
      `consumeRate` (`message_stats.deliver_get_details.rate`), `maxQueueDepth`
      (max of `queues[].messages`), `backlogRate = messageRate - consumeRate`.
      Pick profile: start at lowest (`profileNames[0]`); scan profiles high→low; first profile
      whose `QUEUE_THRESHOLD` is exceeded by `maxQueueDepth` OR whose `RATE_THRESHOLD` is
      exceeded by `messageRate` wins.
   c. `KubernetesClient.getCurrentProfile()` — read the RabbitmqCluster CR's
      `spec.resources.requests.cpu`, map back to a profile name via the cpu→profile map.
   d. `ScalingEngine.checkProfileStability()` — debounce via a ConfigMap holding
      `stable_profile` + `stable_since` (unix seconds). If the recommendation changed, reset
      the timer and return false. If already at recommended, reset timer + return true.
      Otherwise require the recommendation to have been stable ≥ `DEBOUNCE_SCALE_UP_SECONDS`
      (scale-up, recommended priority > current) or ≥ `DEBOUNCE_SCALE_DOWN_SECONDS` (scale-down).
   e. If stable and not already at target → `applyPatch()` JSON-Patch the RabbitmqCluster CR's
      `/spec/resources/requests/cpu` and `/memory`, then `updateStabilityTracking()`.
3. SIGTERM/SIGINT → graceful stop.

### Kubernetes specifics
- Custom resource: group `rabbitmq.com`, version `v1beta1`, plural `rabbitmqclusters`,
  name = `RMQ_SERVICE_NAME`. Patched via **JSON Patch** (op `replace`).
- Stability ConfigMap: name = `CONFIG_MAP_NAME`, keys `stable_profile`, `stable_since`,
  patched via JSON Patch on `/data/...`.
- In-cluster auth (`loadFromCluster` → Go `rest.InClusterConfig()`).
- RBAC needed: `get`/`patch` on `rabbitmqclusters`; `get`/`patch` on `configmaps`.

### Configuration (env vars — the contract the generated YAML produces)
`RMQ_USER`, `RMQ_PASS` (default guest/guest), `RMQ_HOST`, `RMQ_PORT`, `RMQ_SERVICE_NAME`
(default rmq), `NAMESPACE` (default prod), `CONFIG_MAP_NAME` (default rmq-config),
`PROFILE_NAMES` (default "LOW MEDIUM HIGH CRITICAL"), `PROFILE_<NAME>_CPU`,
`PROFILE_<NAME>_MEMORY`, `QUEUE_THRESHOLD_<NAME>`, `RATE_THRESHOLD_<NAME>`,
`DEBOUNCE_SCALE_UP_SECONDS` (30), `DEBOUNCE_SCALE_DOWN_SECONDS` (120),
`CHECK_INTERVAL_SECONDS` (5). First profile has no thresholds (it's the floor).

The CLI reads a JSON config (`examples/*.json`: `profiles`, `thresholds`/inline thresholds,
`debounce`, `checkInterval`, `rmq`, `kubernetes`) and translates it into these env vars in
the generated Deployment.

### Release flow (existing)
`.github/workflows/release.yml`: push tag `v*` → build & push Docker image to Docker Hub
(`ferterahadi/rmq-vertical-scaler:{latest,VERSION}`) + create GitHub release. No npm publish
in CI today (the `npx` path is published manually / separately).

## Constraints

- **Behaviour parity is non-negotiable.** v2 must make byte-identical scaling decisions to v1
  given the same metrics + config. Verify with a port of the existing tests plus a side-by-side
  decision check.
- **Same repo, replace v1.** Go code replaces Node on `master`. v1 stays reachable via the
  `v1.x` git tags / npm `1.x`. Tag the Go release `v2.0.0`. Docker image namespace unchanged
  (`ferterahadi/rmq-vertical-scaler:2.x`).
- **Backward-compatible env-var contract.** A v1-generated Deployment's env vars must still
  drive v2 correctly (so existing users can swap the image). The CLI's JSON config format
  should stay compatible too.
- MIT license, single author. Keep it approachable for contributors.

## Scope

**In:**
- Port runtime (config, metrics, scaling engine, k8s client, main loop) to Go.
- Port the `generate` CLI to Go (same flags, same emitted manifests, reads same JSON config).
- `FROM scratch`/distroless multi-stage Dockerfile producing a static binary.
- Go unit tests for scaling-engine + config logic (parity with current tests).
- Benchmark harness + documented v1-vs-v2 results (idle RSS, image size, cold start, 24h memory, idle CPU).
- Rewrite README for Go (install, usage, the honest "why Go" framing, benchmark table).
- Update `.github/workflows/release.yml` to build the Go binary + image and attach binaries to the GH release.
- CHANGELOG 2.0.0 entry; tag `v2.0.0`.

**Out:**
- New scaling features / algorithm changes (parity only — change behaviour in a later minor).
- Switching to a full kubebuilder/controller-runtime operator (overkill for one control loop;
  plain client-go is the right altitude). Revisit later if it grows.
- Horizontal scaling, multi-cluster, Prometheus metrics endpoint (future, note as ideas).
- Migrating away from Docker Hub.

## Key Decisions (do not re-litigate)

- **Language:** Go. Justified on footprint + ecosystem, NOT speed (see "Why Go").
- **Location:** same repo, `master`, replace Node, tag `v2.0.0`.
- **K8s client:** `client-go`. Use the **dynamic client** (`unstructured`) for the
  `rabbitmqclusters` CRD JSON-Patch (avoids vendoring the RabbitMQ operator's API types);
  typed `CoreV1` for the ConfigMap.
- **CLI framework:** `cobra` (mirrors the existing `commander` multi-command UX, professional default).
- **HTTP:** stdlib `net/http` (replaces axios).
- **YAML generation:** Go `text/template` with `embed`ed manifest templates (mirrors v1's
  string-built YAML, easy to diff against current output). Build structs only if templates get unwieldy.
- **Module path:** `github.com/ferterahadi/rmq-vertical-scaler`.
- **Go version:** latest stable (1.2x) in `go.mod`.
- **Repo layout:** `cmd/rmq-vertical-scaler/` (CLI entrypoint, both `generate` and `run`/controller
  subcommands), `internal/{config,metrics,scaling,k8s,controller}/`, `internal/manifests/` (templates).
- **Benchmark angle:** resource footprint, NOT throughput. Headline = idle RSS + image size.

## Repo

`/Users/oddle/Documents/rmq-vertical-scaler` (git, branch `master`, clean at plan time).
This file lives at `docs/v2-go-migration/`.

## References

- v1 runtime: `lib/RabbitMQVerticalScaler.js`, `lib/ScalingEngine.js`, `lib/MetricsCollector.js`,
  `lib/KubernetesClient.js`, `lib/ConfigManager.js`
- v1 CLI: `bin/rmq-vertical-scaler`
- v1 Docker/CI: `Dockerfile`, `.github/workflows/release.yml`
- Config templates + schema: `examples/*.json`, `schema/config-schema.json`
- Existing tests: `tests/`
- client-go: https://github.com/kubernetes/client-go ; dynamic client docs
- RabbitMQ Cluster Operator CRD: https://github.com/rabbitmq/cluster-operator (rabbitmq.com/v1beta1)
- Findings: `docs/v2-go-migration/research/findings.md`
