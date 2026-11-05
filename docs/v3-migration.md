# Switch to RabbitMQ Vertical Scaler v3

The next release is **v3.0.0**, followed by the **3.0.x** patch series. It replaces
the unpublished 2.3.x release plan. `v3.0.0x` is not a release tag; use `v3.0.0`,
`v3.0.1`, and so on. No v3 image or release has been published yet.

## One version vocabulary

| Surface | v3 name |
|---|---|
| Product and release | RabbitMQ Vertical Scaler v3; tag `v3.0.0` |
| Container image | `ferterahadi/rmq-vertical-scaler:3.0.0` once published; major alias `:3` |
| Go module | `github.com/ferterahadi/rmq-vertical-scaler/v3` |
| New policy / CLI scaffold | `configVersion: 3` / `init --config-version 3` |
| Policy guide and examples | `docs/v3-policy.md` / `examples/v3/` |
| Controller and tests | `internal/v3/` / `tests/e2e-v3/` |
| Helm chart/app version | `3.0.0`; Helm's own `apiVersion: v2` is unchanged |

The major release communicates a new policy and Go module contract. Legacy
unversioned JSON/environment behavior remains supported; upgrading the image alone
does not enable the v3 policy or its safer defaults. No KEDA or VPA installation is
required. Third-party dependency major versions are unrelated to this product rename.

## From a released v2 installation

1. Keep your existing configuration and status backed up. Declare v3 profiles matching
   the brokers' actual requests **and limits**. Recalibrate queue thresholds: v3 defaults
   to summed backlog, while legacy behavior uses the deepest queue.
2. Build/load a v3 image, or use a published v3 image once available. Generate a complete
   v3 policy or use the Helm v3 example. Resource-only triggers need Kubernetes Metrics
   API access; queue/rate triggers need RabbitMQ credentials too. Every decrease needs
   fresh memory metrics.
3. Stop the legacy scaler before starting the v3 controller against that target.
   Use a distinct scaler/status ConfigMap name for the v3 installation so old operation
   state is never treated as v3 state. Preserve the old status for inspection.
4. Deploy in `Off`, inspect recommendations and current allocations, then explicitly
   enable `Auto`. Do not run two controllers against the same cluster.

In-place mode changes the Operator-managed StatefulSet strategy to `OnDelete`,
which also affects later Operator upgrades. Coordinate those separately. Full
memory-limit changes require explicit rolling fallback on the measured platform;
rolling needs at least three healthy brokers and the Operator's quorum-aware guards.

## From the unpublished v2.3 development draft

Change `configVersion: 2` to `configVersion: 3`, update example/schema paths and Go
imports, and regenerate the manifests with the v3 CLI. The draft discriminator is
rejected rather than silently interpreted as legacy configuration.

The controller status format is also version 3. Stop the draft controller and inspect
pending operations before migration. Use a new status ConfigMap; do not edit an old
status version number or erase an unresolved operation to force migration. Wait for
accepted work to converge and resolve mixed resources before enabling v3 `Auto`.

## Evidence and release

Historical reports, image digests, artifact names and capture paths keep their v2.3
identity under `docs/history/v2.3/`. They are not v3 acceptance. The original source
commit remains necessary for historical reconstruction; retain it with a merge commit.

The v3 release gate applies to all `v3.*` tags. It stays blocked until a new immutable
v3 image completes the eight-group acceptance matrix and its source/image fingerprints
and reviewed evidence are recorded. Current unit checks cannot replace that run.

```bash
python3 scripts/check-v3-release-evidence.py v3.0.0
# Expected to fail while R3 remains open.
python3 scripts/package-v3-evidence.py --historical
# Reproduces the original v2.3 bundle only; cannot clear the v3 gate.
```

Rollback requires stopping v3, inspecting effective resources and pending operations,
and restoring a configuration/image compatible with the observed cluster. Switching
the image tag does not roll broker allocations back. Keep old status as evidence;
never run old and new controllers concurrently.
