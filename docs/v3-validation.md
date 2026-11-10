# v3 validation and release requirements

**v3.0.0 introduced the new policy contract after v2.2.x.** The v3.0.1
diagnostic-logging source passed a fresh immutable-image run of all eight groups
and 31 requirements on 10 November 2026. Subsequent production source changes
require renewed acceptance. Private deployment tests do not replace the matrix.
See [measured results](v3-acceptance-results.md) and the [retained bundle](v3-evidence-bundle.md).

## Required acceptance

Run all eight groups: CPU cycle, recovery, deferred capacity, paired/rolling,
API scope, platform constraints, Operator recovery, and pagination. Retain the
source revision, immutable image digest, full request/limit observations, broker
identities, readiness, message accounting and timings. Controlled tests complement
runtime evidence; they do not replace it. The current readiness record is
[acceptance-summary.json](../tests/e2e-v3/acceptance-summary.json).

## Platform constraints

Current acceptance used Kubernetes 1.35.0, RabbitMQ Cluster Operator 2.16.1,
RabbitMQ 4.4.0 and Metrics Server 0.8.0 on Linux/arm64. Other platforms and production
compatibility remain unverified.

- Changing memory limits in place left RabbitMQ's running budget stale. Strict
  mode blocks those transitions before writes; profiles must keep a fixed memory
  limit or explicitly allow rolling fallback.
- Rolling requires at least three healthy brokers and the Operator's quorum-aware
  shutdown guards. Startup, readiness and DNS behavior affect the 120-second
  scale-up objective. Default Operator settings missed that objective in development;
  the scaler does not apply low-latency tuning. Revalidate timing on the intended setup.
- Readiness was sampled. No uninterrupted client availability, throughput improvement,
  infrastructure saving or exactly-once delivery claim follows from these experiments.

## Development evidence

The machine-readable [descriptor](../tests/e2e-v3/development-evidence.json) pins
the original source and summary bytes. Historical source files and raw captures
are fetched from those commits only when verifying evidence; they are not maintained
as a versioned documentation directory or represented as a published release.

```bash
python3 scripts/package-v3-evidence.py --historical
python3 scripts/package-v3-evidence.py --verify-historical
python3 scripts/check-v3-release-evidence.py v3.0.1
```

The first command recreates `dist/evidence/development.tar.gz` and checks its original
checksum. The second verifies the recorded source/image linkage against the original
source export. The third verifies the fresh v3 evidence after retrieving its
[bundle](v3-evidence-bundle.md); it cannot use development captures instead. Keep the
pinned development commits in history with a merge commit; do not relabel old captures as v3 proof.
