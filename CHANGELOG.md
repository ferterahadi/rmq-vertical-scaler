# Changelog

All notable changes to this project will be documented in this file.

## [3.0.0] - 2026-11-06

### Added
- Four-profile configuration example with explicit CPU/memory targets, trigger priority
  and a worked decision simulation.
- Opt-in `configVersion: 3` JSON and Helm policies with paired CPU/memory requests
  and limits, queue depth/publish rate and busiest-broker utilization triggers.
- Recommendation-only `Off`, configurable stabilization and conservative downward
  steps, fresh memory safety checks, and durable asynchronous resize reconciliation.
- Confirm desired and effective resources on every broker; retain targets and
  successful partial changes across deferred operations and controller restarts.

### Changed
- Publish the new policy/status contract, `/v3` Go module, CLI, examples and schema
  as v3.0.0, the next major release after v2.2.x. Legacy unversioned behavior remains
  supported. See [migration](docs/v3-migration.md).
- Require in-place operation by default. Strict mode blocks unsupported memory-limit
  changes before writes; rolling fallback requires explicit configuration. No broker exec.
- Use one scaler replica with `Recreate`; separate controller-owned status from Helm policy.

### Fixed
- Judge metric freshness at collection completion while keeping poll time for
  stabilization. Collect overview rates independently from queue pagination and
  skip unused enumeration. Request explicit queue totals when detailed statistics
  have not initialized, while retaining selected-queue publish-rate statistics.

### Validation
- Fresh immutable-image acceptance passed all eight runtime groups and 31 requirements
  on the pinned local platform. Rolling scale-up took 92.54 seconds with explicit
  startup/readiness/DNS tuning; default rolling timing is not established by this run.
  Strict memory-limit changes remain unsupported. See [results](docs/v3-acceptance-results.md)
  and the [retained evidence](docs/v3-evidence-bundle.md).

## [2.2.1] - 2026-07-10

### Added
- **In-place RBAC preflight.** At startup the scaler runs a `SelfSubjectAccessReview` for `pods/resize` (patch) and `pods` (list) — testing *permission*, not just the API *capability* that discovery reports. `SCALE_MODE=auto` degrades to rolling with a warning that names the missing RBAC; explicit `SCALE_MODE=inplace` **fails fast** at startup with a message naming the exact verbs to grant, instead of a silent 403 at the first resize.

### Fixed
- **No stranded `OnDelete` override on missing RBAC.** In `auto` mode a resize rejected for lack of `pods/resize` RBAC (HTTP 403 Forbidden) is now treated like an infeasible resize — the scaler reverts the override and falls back to rolling — where before it hard-failed after already flipping the cluster to `OnDelete`. The startup preflight normally prevents this path from being reached at all.
- Startup log now reads `Scale mode: inplace (pods/resize permitted)` — "permitted" (permission) rather than "supported" (capability).

### Compatibility
- No change to the Helm chart or `generate` RBAC output (both already grant `pods/resize`), env-var contract, or `SCALE_MODE` semantics. A hand-rolled deployment missing the `pods/resize` grant now surfaces the gap loudly instead of failing silently. Patching resource *limits* for Guaranteed-QoS pods (request ≤ limit) is out of scope here and deferred to a later release.

## [2.2.0] - 2026-07-05

### Added
- **Zero-restart vertical scaling** via Kubernetes in-place pod resize (`pods/resize` subresource, K8s ≥ 1.33). New `scaling.mode` config / `SCALE_MODE` env: `auto` (default — in-place when the cluster supports it, rolling otherwise), `inplace`, `rolling` (v2.0.0 behaviour).
- In in-place mode the scaler sets the RabbitmqCluster's StatefulSet override to `updateStrategy: OnDelete`, resizes each pod's resource **requests** live (both scale-up and scale-down, CPU and memory), and still patches the CR so it remains the source of truth — recreated pods come back at the current profile's size.
- **Infeasible fallback**: if the kubelet reports an in-place resize as `Infeasible`, `auto` mode reverts the override and falls back to a rolling scale for that action.
- Generated manifests and the Helm chart grant `pods` get/list and `pods/resize` patch, and emit `SCALE_MODE`; kind-based e2e suite under `tests/e2e/`. No `pods/exec` needed: RabbitMQ's memory high-watermark derives from the memory *limit*, which the scaler never changes, so it stays correct across in-place resizes.

### Changed
- Rolling mode (and pre-1.33 clusters) behave exactly as v2.0.0: one CR patch, the operator rolls pods.

### Compatibility
- Env-var contract is backward compatible; without `SCALE_MODE` the scaler defaults to `auto`. On clusters without `pods/resize` it behaves identically to v2.1.0.
- **`OnDelete` trade-off** (in-place mode only): operator-driven pod template changes (e.g. RabbitMQ image bumps) no longer roll pods automatically — delete pods one at a time, or set `scaling.mode: rolling`.

## [2.1.0] - 2026-07-05

### Added
- **Helm chart** at `charts/rmq-vertical-scaler` — ordered profile list, credentials secret name/key overrides, `pdb.enabled` toggle, values validated by `values.schema.json`.
- **`init` subcommand** scaffolds a starter config; Quick Start no longer needs a repo clone.
- **Config validation in `generate`**: configs are checked against `schema/config-schema.json` (embedded in the binary); unknown keys, missing `cpu`/`memory`, and malformed values now fail with a list of violations instead of silently generating empty env values.
- **`--no-pdb` flag** to skip the PodDisruptionBudget for operators managing their own.

### Changed
- Default image in generated manifests is **pinned to the release version** (fallback `:2`) instead of `:latest`.
- Example configs reference the schema by absolute URL, so IDE validation works outside a checkout.
- The generated PDB's comment now correctly describes it (protects the RabbitMQ nodes during scaling restarts; requires 3+ nodes).

### Compatibility
- Stricter `generate` validation may reject configs that previously "worked" by silently falling back to defaults — fix the reported fields. The legacy top-level `thresholds` block remains supported. Runtime env-var contract unchanged.

## [2.0.0] - 2026-06-13

### Changed
- **Rewritten in Go.** The runtime controller and the manifest generator are now a single static Go binary shipped in a `distroless/static:nonroot` image — smaller idle memory footprint (~10–15 MB RSS vs ~50–80 MB), much smaller attack surface, and no interpreted runtime. **No change to scaling behaviour.**
- Kubernetes integration now uses the official `client-go` (dynamic client for the `RabbitmqCluster` CRD, typed CoreV1 for the state ConfigMap).
- Container signal handling is now native to Go; `dumb-init` is no longer needed.
- CI now builds and publishes a multi-arch image (`linux/amd64`, `linux/arm64`) and attaches prebuilt release binaries.

### Compatibility
- **Behaviour parity** with v1: identical scaling decisions for the same metrics + config (enforced by tests).
- **Env-var contract unchanged**: a v1-generated Deployment drives v2 — swap the image tag to `:2`.
- **Generated manifests** are byte-for-byte compatible with v1 (golden-tested).
- **Config format unchanged**: existing `examples/*.json` work as-is.
- The Node.js v1 remains available via `v1.x` git tags and `:1.x` Docker images / npm `1.x`.

## [1.0.2] - 2025-08-17

### Fixed
- Added namespace environment variable to scaler deployment

### Changed
- Reorganized test files from k8s-test to tests/k8s directory
- Monitor script now displays individual pod CPU allocations for better visibility
- Test dependencies moved to devDependencies

## [1.0.1] - 2025-08-17

### Fixed
- RMQ_HOST environment variable generation for cluster deployments
- Added comprehensive unit tests for configuration

## [1.0.0] - 2025-08-17

### Added
- Initial release of RabbitMQ Vertical Scaler
- Automatic vertical scaling based on queue depth and message rates
- Support for multiple scaling profiles (LOW, MEDIUM, HIGH, CRITICAL)
- Kubernetes deployment with RBAC configuration
- Docker support
- CLI tool for generating deployment manifests
