# Tasks — rmq-vertical-scaler v2 (Go rewrite)

Ordered by dependency. Behaviour parity with v1 is the acceptance bar throughout.

## Phase 0 — Scaffolding
- [ ] `go mod init github.com/ferterahadi/rmq-vertical-scaler`; set latest stable Go in `go.mod`.
- [ ] Create layout: `cmd/rmq-vertical-scaler/main.go`, `internal/{config,metrics,scaling,k8s,controller}/`, `internal/manifests/`.
- [ ] Add `cobra`; wire root command with two subcommands: `generate` (CLI manifest gen) and `run` (in-cluster controller; default CMD in the image).
- [ ] Add `client-go` + `apimachinery` + `k8s.io/api` deps; `go mod tidy`.

## Phase 1 — Config (port `lib/ConfigManager.js`)
- [ ] `internal/config`: load all env vars (see plan "Configuration") with the same defaults.
- [ ] Build `Profiles` (name→{cpu,memory}), `QueueThresholds`, `RateThresholds` from `PROFILE_NAMES` + per-profile env vars; first profile has no thresholds.
- [ ] Build `cpuToProfileMap` (cpu string → profile name).
- [ ] Unit test: given a known env-var set, assert parsed profiles/thresholds/debounce/interval match v1.

## Phase 2 — Metrics (port `lib/MetricsCollector.js`)
- [ ] `internal/metrics`: stdlib `net/http` GET `/api/overview` and `/api/queues` with basic auth, 10s timeout.
- [ ] Decode JSON into structs (`queue_totals.messages`, `message_stats.publish_details.rate`, `message_stats.deliver_get_details.rate`, `queues[].messages`). On error return zero-value + skip signal (parity with v1 empty-response skip).
- [ ] `WaitForRabbitMQ()` retry loop (5s backoff, log every attempt).
- [ ] Unit test with `httptest.Server` returning a sample overview/queues payload.

## Phase 3 — Scaling engine (port `lib/ScalingEngine.js`) — HIGHEST parity risk
- [ ] `CalculateScaleProfile(metrics, cfg)` — exact threshold scan high→low (queue OR rate), floor = `profileNames[0]`.
- [ ] `GetProfilePriority`, `GenerateScalingMessage` (keep the same tiered messages/emoji or document the change).
- [ ] `CheckProfileStability(current, recommended, stabilityState, now)` — reset-on-change, already-at-target reset+true, scale-up vs scale-down debounce thresholds. Keep this pure (inject `now`) for testability.
- [ ] Port the existing `tests/` cases to Go table tests; add cases at each threshold boundary and a debounce-timer case.

## Phase 4 — Kubernetes client (port `lib/KubernetesClient.js`)
- [ ] `internal/k8s`: `rest.InClusterConfig()`; dynamic client for the CRD, typed CoreV1 for ConfigMap.
- [ ] `GetCurrentProfile()` — get `rabbitmqclusters/{RMQ_SERVICE_NAME}` (rabbitmq.com/v1beta1), read `spec.resources.requests.cpu`, map via cpuToProfileMap (→ `UNKNOWN` on miss/err).
- [ ] `GetStabilityState()` / `UpdateStabilityTracking(profile)` — read ConfigMap `data.stable_profile`/`stable_since`; JSON-Patch `/data/...` (use `time.Now().Unix()`).
- [ ] `ApplyPatch(resources)` — JSON-Patch the CR `/spec/resources/requests/cpu` + `/memory`.
- [ ] Manual/integration check against a real RabbitMQ-operator cluster (or envtest) — verify patch shape is accepted.

## Phase 5 — Controller loop (port `lib/RabbitMQVerticalScaler.js` + `lib/index.js`)
- [ ] `internal/controller`: wire config+metrics+scaling+k8s; `ApplyScale()` mirroring v1's order and log lines.
- [ ] `Run(ctx)`: `WaitForRabbitMQ`, then `time.Ticker` at `CHECK_INTERVAL_SECONDS`; per-iteration error isolation (log + continue).
- [ ] `signal.NotifyContext` for SIGTERM/SIGINT graceful shutdown.
- [ ] Optional: tiny `/healthz` HTTP handler on :8080 to keep the Dockerfile HEALTHCHECK meaningful (or drop the healthcheck).

## Phase 6 — CLI manifest generator (port `bin/rmq-vertical-scaler`)
- [ ] `generate` subcommand: same flags (`--config`, `--namespace`, `--service-name`, `--output`, `--image`, `--scaler-name`).
- [ ] Read the same JSON config shape (`examples/*.json`); keep `schema/config-schema.json` valid.
- [ ] `internal/manifests`: `embed` templates for ServiceAccount, Role, RoleBinding, ConfigMap, Deployment; render env vars from config (the v1 env-var contract).
- [ ] Parity check: generate from `examples/production-config.json` with v1 and v2, diff the YAML; reconcile until functionally identical (allow cosmetic ordering diffs, document them).

## Phase 7 — Container + build
- [ ] New multi-stage Dockerfile: builder `golang:1.x` → `CGO_ENABLED=0 go build -ldflags="-s -w"`; final stage `FROM scratch` (or `gcr.io/distroless/static:nonroot`) with the static binary + CA certs, non-root, `ENTRYPOINT ["/rmq-vertical-scaler","run"]`.
- [ ] Confirm signal handling works without dumb-init (Go handles it natively).
- [ ] `make` targets / scripts: `build`, `test`, `image`, `bench`.

## Phase 8 — Benchmark (the v1-vs-v2 story)
- [ ] Write `docs/v2-go-migration/benchmark.md` methodology: same cluster, same config, same RabbitMQ load profile.
- [ ] Measure & record for v1 and v2:
  - [ ] Idle RSS (`kubectl top pod` / cgroup `memory.current`) over a 24h steady run.
  - [ ] Container image size (`docker images`).
  - [ ] Cold start: container start → first completed scaling decision (from logs).
  - [ ] Idle CPU (`kubectl top pod`).
  - [ ] Memory stability over 24h (no leak; flat/sawtooth GC).
- [ ] Produce a results table + short interpretation (footprint win, not speed). Capture raw data under `research/`.

## Phase 9 — Docs
- [ ] Rewrite `README.md` for Go: badges, install (`docker pull`, `go install`, binary release), `generate` usage, `run`/deploy, config, **the honest "why Go (footprint, not speed)" section**, the benchmark table, architecture, contributing (Go toolchain).
- [ ] Keep the existing quorum-queue / message-loss safety warnings verbatim.
- [ ] Note v1→v2 upgrade path (swap image tag to `2.x`; env-var contract unchanged).

## Phase 10 — Release v2.0.0
- [ ] Update `.github/workflows/release.yml`: Go build, `docker build` from new Dockerfile, push `:2.x` + `:latest`; add `goreleaser` (or `go build` matrix) to attach `linux/{amd64,arm64}` binaries to the GH release.
- [ ] Remove/retire Node-specific files (`package.json`, `lib/`, `bin/`, `webpack.config.js`, `package-lock.json`) — or move under a `v1/` archive dir / rely on `v1.x` tags. Decide and execute.
- [ ] CHANGELOG `## [2.0.0]`: "Rewritten in Go — smaller footprint & image, no behaviour change."
- [ ] Final parity gate: side-by-side run v1 vs v2 against the same cluster for a load cycle; confirm identical scaling decisions.
- [ ] Tag `v2.0.0`, push, verify the release workflow publishes image + binaries + GH release.
