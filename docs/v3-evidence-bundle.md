# Verify the v3 evidence bundle

The current v3.0.0 acceptance captures live outside Git in a checksummed GitHub
Actions artifact. Git keeps the concise [summary](../tests/e2e-v3/acceptance-summary.json)
and [results](v3-acceptance-results.md).

## Current archive

- [Download artifact 13496691411](https://github.com/ferterahadi/rmq-vertical-scaler/actions/runs/42205168722/artifacts/13496691411),
  uploaded by successful [run 42205168722](https://github.com/ferterahadi/rmq-vertical-scaler/actions/runs/42205168722).
- Inner file: `rmq-v3-evidence.tar.gz`, **271 members**, **894,763 bytes**.
- SHA-256: `e97243218e820ba53728620951650222edbe2040dde5038cdd2fe71ec26e0c1c`.
- Retention: **90 days**, expires **04 February 2027 at 10:46:06 UTC**
  (04 February at 18:46:06 Singapore time).

The checksum covers the inner gzip archive, not GitHub's transport ZIP. Local and
independently downloaded copies matched byte-for-byte and passed the full gate.

## Retrieve and check

Use the GitHub CLI authenticated with access to this repository, and Python 3:

```bash
python3 scripts/package-v3-evidence.py
python3 scripts/check-v3-release-evidence.py v3.0.0
```

The first command downloads the pinned artifact into
`dist/acceptance/3.0.0/rmq-v3-evidence.tar.gz`, and verifies its checksum. The second safely extracts the expected files in a
temporary directory and checks the current source/image
linkage, all 31 requirements, eight runtime groups, timings and reviewed boundaries.
The release workflow performs these checks before publication. A missing, expired
or altered artifact fails closed.

An independent copy can be checked offline:

```bash
python3 scripts/check-v3-release-evidence.py v3.0.0 --bundle /path/to/rmq-v3-evidence.tar.gz
```

Retain an independent copy before expiry. Fresh raw captures are ignored and are
not committed: Git source alone cannot recreate measured timestamps, identities or
message evidence byte-for-byte. After expiry, re-upload the same checked archive
and update its pin, or perform a new acceptance run. Rebuilding a binary is not a
replacement for the retained runtime proof.

## Development evidence

Earlier development evidence has a separate
[descriptor](../tests/e2e-v3/development-evidence.json). Its original commits remain
necessary for exact reconstruction; retain those commits through a merge commit.

```bash
python3 scripts/package-v3-evidence.py --historical
python3 scripts/package-v3-evidence.py --verify-historical
```

These commands reproduce and verify the older development bundle only. They cannot
clear the current v3 gate. The fresh v3 artifact is retrieved by its pin and does not
depend on reconstructing old raw captures from Git history.
