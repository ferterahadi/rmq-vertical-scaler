#!/usr/bin/env python3
"""Reproduce the immutable evidence bundle from its recorded Git commit."""
import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import re
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[1]
SUMMARY = "tests/e2e-v2/acceptance-summary.json"
PREFIXES = ("tests/e2e-v2/results/", "tests/feasibility/results/")


def require_source_history(root, commit):
    result = subprocess.run(["git", "merge-base", "--is-ancestor", commit, "HEAD"],
                            cwd=root, capture_output=True)
    if result.returncode:
        raise ValueError("evidence source is absent from HEAD history; merge this PR with a merge commit "
                         "(not squash/rebase), or supply an independently retained bundle to the verifier")


def write_bundle(files, output):
    """Stable paths, metadata, ordering and gzip headers produce identical bytes."""
    output = Path(output)
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w") as archive:
                for name, data in sorted(files.items()):
                    info = tarfile.TarInfo(name)
                    info.size = len(data)
                    info.mode = 0o644
                    archive.addfile(info, io.BytesIO(data))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    summary = json.loads((ROOT / SUMMARY).read_text())
    commit = summary["bundle"]["sourceCommit"]
    if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("evidence requires a full immutable source commit")
    require_source_history(ROOT, commit)
    raw = subprocess.check_output(["git", "archive", "--format=tar", commit, "--",
        *[p.rstrip("/") for p in PREFIXES]], cwd=ROOT)
    files = {}
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:") as archive:
        for member in archive:
            if member.isdir():
                continue
            if not member.isfile() or not member.name.startswith(PREFIXES):
                raise ValueError("invalid evidence object in historical commit")
            files[member.name] = archive.extractfile(member).read()
    if not files:
        raise ValueError("evidence commit has no valid captures")
    output = args.output or ROOT / summary["bundle"]["localPath"]
    write_bundle(files, output)
    digest = hashlib.sha256(output.read_bytes()).hexdigest()
    if digest != summary["bundle"]["sha256"]:
        output.unlink()
        raise ValueError("reproduced evidence differs from the pinned bundle checksum")
    print(f"Evidence bundle verified: {output} (sha256:{digest}, {len(files)} files)")


if __name__ == "__main__":
    main()
