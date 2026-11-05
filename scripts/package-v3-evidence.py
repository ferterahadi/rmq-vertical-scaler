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
SUMMARY = "tests/e2e-v3/acceptance-summary.json"
PREFIXES = ("tests/e2e-v3/results/", "tests/feasibility/results/")


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


def development_summary():
    descriptor = json.loads((ROOT / "tests/e2e-v3/development-evidence.json").read_text())
    commit = descriptor["summaryCommit"]
    if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("development summary requires an immutable commit")
    require_source_history(ROOT, commit)
    data = subprocess.check_output(["git", "show", commit + ":" + descriptor["summaryPath"]], cwd=ROOT)
    if hashlib.sha256(data).hexdigest() != descriptor["summarySHA256"]:
        raise ValueError("development summary checksum mismatch")
    summary = json.loads(data)
    if (summary["bundle"]["sourceCommit"] != descriptor["sourceCommit"] or
        summary["bundle"]["sha256"] != descriptor["bundleSHA256"]):
        raise ValueError("development evidence descriptor differs from recorded source")
    return summary


def verify_development(summary):
    import tempfile
    commit = summary["bundle"]["sourceCommit"]
    require_source_history(ROOT, commit)
    with tempfile.TemporaryDirectory(prefix="rmq-development-source-") as folder:
        raw = subprocess.check_output(["git", "archive", "--format=tar", commit], cwd=ROOT)
        subprocess.run(["tar", "-xf", "-", "-C", folder], input=raw, check=True)
        base = Path(folder)
        verifiers = list((base / "scripts").glob("check-*-release-evidence.py"))
        version = re.search(r'^appVersion: *["\']?([^"\'\s]+)',
                            (base / "charts/rmq-vertical-scaler/Chart.yaml").read_text(), re.M)
        if len(verifiers) != 1 or version is None:
            raise ValueError("recorded source has no unambiguous acceptance verifier/version")
        result = subprocess.run(["python3", str(verifiers[0]), version.group(1)], cwd=base,
                                capture_output=True, text=True)
        if result.returncode:
            raise ValueError("development acceptance verification failed: " + result.stdout + result.stderr)
    print("Development evidence verified against its recorded source.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--historical", action="store_true", help="Reproduce the pre-v3 evidence; never authorize a v3 release")
    parser.add_argument("--verify-historical", action="store_true", help="Verify development evidence against its recorded source")
    args = parser.parse_args()
    if args.verify_historical:
        verify_development(development_summary())
        return
    prefixes = ("tests/e2e-v2/results/", "tests/feasibility/results/") if args.historical else PREFIXES
    summary = development_summary() if args.historical else json.loads((ROOT / SUMMARY).read_text())
    if "bundle" not in summary:
        raise ValueError("v3 has no accepted immutable-image evidence bundle yet")
    commit = summary["bundle"]["sourceCommit"]
    if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("evidence requires a full immutable source commit")
    require_source_history(ROOT, commit)
    raw = subprocess.check_output(["git", "archive", "--format=tar", commit, "--",
        *[p.rstrip("/") for p in prefixes]], cwd=ROOT)
    files = {}
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:") as archive:
        for member in archive:
            if member.isdir():
                continue
            if not member.isfile() or not member.name.startswith(prefixes):
                raise ValueError("invalid evidence object in historical commit")
            files[member.name] = archive.extractfile(member).read()
    if not files:
        raise ValueError("evidence commit has no valid captures")
    output = args.output or ROOT / ("dist/evidence/development.tar.gz" if args.historical else summary["bundle"]["localPath"])
    write_bundle(files, output)
    digest = hashlib.sha256(output.read_bytes()).hexdigest()
    if digest != summary["bundle"]["sha256"]:
        output.unlink()
        raise ValueError("reproduced evidence differs from the pinned bundle checksum")
    print(f"Evidence bundle verified: {output} (sha256:{digest}, {len(files)} files)")


if __name__ == "__main__":
    main()
