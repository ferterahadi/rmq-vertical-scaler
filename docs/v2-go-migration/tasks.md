# Tasks — rmq-vertical-scaler v2 (Go rewrite)

Ordered by dependency. Behaviour parity with v1 is the acceptance bar throughout.

Status legend: `[x]` done · `[ ]` open · `[~]` partial. Verified live on a
minikube + RabbitMQ-Cluster-Operator cluster on 2026-06-13.

## Phase 0 — Scaffolding
- [x] `go mod init github.com/ferterahadi/rmq-vertical-scaler`; set latest stable Go in `go.mod`.
- [x] Create layout: `cmd/rmq-vertical-scaler/main.go`, `internal/{config,metrics,scaling,k8s,controller}/`, `internal/manifests/`.
- [x] Add `cobra`; wire root command with two subcommands: `generate` and `run` (default CMD in the image).
- [x] Add `client-go` + `apimachinery` + `k8s.io/api` deps; `go mod tidy`.

## Phase 1 — Config (port `lib/ConfigManager.js`)
- [x] `internal/config`: load all env vars with the same defaults.
- [x] Build `Profiles`, `QueueThresholds`, `RateThresholds`; first profile has no thresholds.
- [x] Build `cpuToProfileMap`.
- [x] Unit test parity — **100% coverage**.

## Phase 2 — Metrics (port `lib/MetricsCollector.js`)
- [x] `internal/metrics`: stdlib `net/http` GET `/api/overview` + `/api/queues`, basic auth, 10s timeout.
- [x] Decode JSON; on error return zero-value + skip signal (v1 empty-response parity).
- [x] `WaitForRabbitMQ()` retry loop (5s backoff).
- [x] `httptest.Server` unit tests — **100% coverage**.

## Phase 3 — Scaling engine (port `lib/ScalingEngine.js`)
- [x] `CalculateScaleProfile` — threshold scan high→low (queue OR rate), floor = `profileNames[0]`.
- [x] `GetProfilePriority`, `GenerateScalingMessage` (same tiered messages).
- [x] `CheckProfileStability` — pure, `now`-injected (reset-on-change, already-at-target, scale-up/down debounce).
- [x] Ported v1 tests to Go table tests + boundary + debounce cases — **100% coverage**.

## Phase 4 — Kubernetes client (port `lib/KubernetesClient.js`)
- [x] `internal/k8s`: `rest.InClusterConfig()`; dynamic client for the CRD, typed CoreV1 for ConfigMap.
- [x] `GetCurrentProfile()` (→ `UNKNOWN` on miss/err).
- [x] `GetStabilityState()` / `UpdateStabilityTracking()` — JSON-Patch `/data/...`.
- [x] `ApplyPatch()` — JSON-Patch `/spec/resources/requests/cpu` + `/memory`.
- [x] Patch shape verified against a **real RabbitMQ-operator cluster** (live scale up & down) + fake-clientset unit tests.

## Phase 5 — Controller loop (port `RabbitMQVerticalScaler.js` + `index.js`)
- [x] `internal/controller`: `ApplyScale()` mirroring v1's order + log lines.
- [x] `Run(ctx)`: `WaitForRabbitMQ` then `time.Ticker`; per-iteration error isolation.
- [x] `signal.NotifyContext` for SIGTERM/SIGINT graceful shutdown.
- [x] Dropped the `/healthz` HEALTHCHECK (Go native signals; no HTTP server needed). **100% coverage.**

## Phase 6 — CLI manifest generator (port `bin/rmq-vertical-scaler`)
- [x] `generate` subcommand: same flags.
- [x] Same JSON config shape; `schema/config-schema.json` unchanged.
- [x] `internal/manifests`: `embed`ed template; v1 env-var contract.
- [x] Parity check: generated YAML is **byte-for-byte identical** to v1 for every example config + the no-config default (golden test in `go test`). Zero cosmetic diffs.

## Phase 7 — Container + build
- [x] Multi-stage Dockerfile: `golang:1.26` → `gcr.io/distroless/static:nonroot`, static stripped binary, non-root, `ENTRYPOINT ["/rmq-vertical-scaler"]`, `CMD ["run"]`.
- [x] Signals work without dumb-init (Go native).
- [x] `Makefile`: `build`, `test`, `cover`, `vet`, `fmt`, `tidy`, `image`, `bench`, `clean` (run `make help`).
- [x] `docker build` verified via colima → **59.9 MB** image.

## Phase 8 — Benchmark (the v1-vs-v2 story)
- [x] Methodology written in `research/benchmark.md` (note: not `docs/v2-go-migration/benchmark.md`) + `scripts/benchmark.sh` harness.
- [~] Measured (live minikube run):
  - [x] Idle RSS — **8 MiB** (v2) vs documented Node ~50–80 MB.
  - [x] Container image size — **59.9 MB** (v2).
  - [x] Idle CPU — **1m** (v2).
  - [ ] Cold start from logs — not formally timed (optional).
  - [ ] 24h memory stability — needs a sustained run (optional).
  - [~] Direct v1-vs-v2 numbers — in progress (building the v1 `:1.0.2` image to deploy side by side).
- [x] Results table + interpretation written.

## Phase 9 — Docs
- [x] `README.md` rewritten for Go (badges, install, usage, honest "why Go" section, benchmark, architecture, contributing).
- [x] Quorum-queue / message-loss safety warnings kept verbatim.
- [x] v1→v2 upgrade path documented (swap image tag to `:2`; env-var contract unchanged).
- [x] Bonus: `docs/local-testing.md` (minikube end-to-end runbook).

## Phase 10 — Release v2.0.0
- [x] `.github/workflows/release.yml`: tests gate → multi-arch `docker buildx` (amd64/arm64) → `:2`/`:VERSION`/`:latest` → release binaries via `softprops/action-gh-release`.
- [x] Retire Node files — **decided: tag v1 then delete.** Created `v1.0.2` tag; deleted `package.json`, `lib/`, `bin/`, `webpack.config.js`, `package-lock.json`, `tests/unit/`, `scripts/build.sh`.
- [x] CHANGELOG `## [2.0.0]`.
- [x] Final parity gate — v2 validated end-to-end on a live cluster (scale up LOW→HIGH + down HIGH→LOW with debounce + real CR patch).
- [ ] **Tag `v2.0.0`, push, verify the release publishes image + binaries + GH release. — DEFERRED** (outward-facing; awaiting go-ahead, and holding merge to `master` until tests are signed off).

## Open / deferred (not blocking code-completeness)
- [ ] Push `release/2.0.0` + the `v1.0.2` tag to `origin`; merge to `master`. **Held until the test sign-off.**
- [x] Canary Lab feature + test cases — `rmq_vertical_scaler_scaling` in the canary-lab workspace. **3/3 passing**: idle→LOW, backlog→HIGH, drain→LOW, against a live minikube cluster, rebuilding the scaler image from source each run. (First run surfaced a test-scenario flaw — the drain deleted the only queue, tripping the scaler's intended "no queues → skip"; fixed by pre-declaring a durable queue + perf-test `--predeclared`.)
- [ ] Optional: cold-start timing, 24h memory-stability sample.
