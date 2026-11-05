# Acceptance evidence bundle

> Historical v2.3 development evidence. The release is now v3.0.0; these results do not validate the v3 build. See [v3 migration](../../v3-migration.md).

**Current release status:** the collector was corrected after this image's
acceptance. The bundle remains valid historical evidence; it cannot authorize
publication of the changed binary. `currentSourceAccepted:false` and explicit
blockers in the tracked summary keep the release gate closed until new
immutable-build runtime evidence exists. PR CI separately checks the current
source and verifies the recorded evidence against its original source.

Git keeps a [concise acceptance summary](../../../tests/e2e-v3/historical-v23-acceptance-summary.json)
and the [human-readable results](acceptance-results.md). The 713 raw acceptance
and feasibility captures are packaged as one deterministic archive, outside the
tracked source tree. Both `tests/e2e-v2/results/` and `tests/feasibility/results/`
are ignored; existing local captures are preserved.

## Reproduce and verify

The historical bundle contains 713 files (1,749,907 bytes), SHA-256
`a2b3d0d18b105b7bd000a16dd23bc5e52efa2a1e0844f49265eb6d73c4652b35`.
Its original source commit is `44e4a1a3e088c53966f7c5e085ffe6080cb1a7a1`.

From a full clone containing that commit:

```bash
python3 scripts/package-v3-evidence.py --historical
```

This recreates `dist/acceptance/2.3.0/rmq-v23-evidence.tar.gz` and verifies its
unchanged checksum. Original capture paths remain inside the archive.
The current PR workflow separately exports the original commit and runs its original
`scripts/check-v23-release-evidence.py v2.3.0` against the original sources.
It then checks that the current v3 gate remains blocked. The v3 release workflow
requires a separate current bundle and never treats this historical pass as v3 acceptance.

For the same historical source validation locally, choose an empty export directory:

```bash
mkdir -p /private/tmp/rmq-historical-source
git archive 44e4a1a3e088c53966f7c5e085ffe6080cb1a7a1 | tar -x -C /private/tmp/rmq-historical-source
(cd /private/tmp/rmq-historical-source && python3 scripts/check-v23-release-evidence.py v2.3.0)
```

Keep the source commit in history by merging with a merge commit. The packager checks
ancestry, so squash/rebase merging can break reconstruction. Packaging fixes cannot
waive production-source/image matching.

## Stored artifact

The [original approved artifact](https://github.com/ferterahadi/rmq-vertical-scaler/actions/runs/41010495542/artifacts/12956419670)
is named `rmq-v23-evidence` and was recorded to expire on 28 January 2027. The updated
workflow labels newly uploaded copies `rmq-v23-historical-evidence`, with 90-day
retention. Each run has its own immutable artifact ID. The checksum above identifies
the inner archive rather than GitHub's transport ZIP. Preserve source history or an
independent copy beyond artifact retention.

## Browsing captures

The verifier safely extracts into a temporary directory, then checks all original
source/image, message-accounting, runtime and timing requirements. To inspect
captures locally, verify first and then extract:

```bash
python3 scripts/package-v3-evidence.py --historical
tar -xzf dist/acceptance/2.3.0/rmq-v23-evidence.tar.gz
```

Original paths remain inside the archive, including:

- `tests/e2e-v2/results/acceptance.json`: original fingerprint manifest.
- `tests/e2e-v2/results/full-acceptance.json`: detailed 31-requirement evidence map.
- `tests/e2e-v2/results/final-acceptance-2026-10-30/`: final build and eight runtime groups.
- `tests/e2e-v2/results/rolling-latency-diagnostics-2026-10-30/`: retained timing failures and tuning proof.
- `tests/feasibility/results/`: broker-budget feasibility evidence.

## Source provenance after packaging

The original captures and their source fingerprints remain byte-for-byte
unchanged. Six packaging/verification files are separately fingerprinted in the
tracked summary. This narrow amendment covers the archive tooling, its tests and
workflows; controller, policy, schema, chart and other tested inputs must still
match the original acceptance snapshot. Packaging tests and CI validate these
new files separately. The accepted controller image is unchanged.

The packaging change passed all 45 harness/gate regressions, including twelve new
archive tests. The original 33-test result remains unchanged in the bundle.
