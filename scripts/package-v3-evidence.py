#!/usr/bin/env python3
"""Retrieve the pinned v3 evidence artifact or reconstruct historical evidence."""
import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import zipfile

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


def download_actions_bundle(artifact, output):
    if type(artifact) is not int or artifact <= 0:
        raise ValueError("evidence requires a pinned GitHub Actions artifact ID")
    # gh uses the caller's normal GitHub authentication; never embed a token.
    with tempfile.TemporaryDirectory(prefix="rmq-v3-download-") as directory:
        download = Path(directory) / "artifact.zip"
        with download.open("wb") as stream:
            subprocess.run(["gh", "api", f"repos/ferterahadi/rmq-vertical-scaler/actions/artifacts/{artifact}/zip"],
                           stdout=stream, check=True)
        with zipfile.ZipFile(download) as archive:
            members = archive.infolist()
            if len(members) != 1 or members[0].filename != "rmq-v3-evidence.tar.gz" or members[0].file_size > 64 * 1024 * 1024:
                raise ValueError("unexpected evidence artifact contents")
            output.parent.mkdir(parents=True, exist_ok=True)
            output.write_bytes(archive.read(members[0]))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--historical", action="store_true", help="Reproduce the pre-v3 evidence; never authorize a v3 release")
    args = parser.parse_args()
    summary_path = "tests/e2e-v3/historical-v23-acceptance-summary.json" if args.historical else SUMMARY
    prefixes = ("tests/e2e-v2/results/", "tests/feasibility/results/") if args.historical else PREFIXES
    summary = json.loads((ROOT / summary_path).read_text())
    if "bundle" not in summary:
        raise ValueError("v3 has no accepted immutable-image evidence bundle yet")
    commit = summary["bundle"]["sourceCommit"]
    if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("evidence requires a full immutable source commit")
    output = args.output or ROOT / summary["bundle"]["localPath"]
    descriptor = summary["bundle"]
    if descriptor.get("storage") == "github-actions":
        download_actions_bundle(descriptor.get("artifactId"), output)
        files_count = descriptor["files"]
    elif descriptor.get("storage") in (None, "git-history"):
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
        write_bundle(files, output)
        files_count = len(files)
    else:
        raise ValueError("unknown evidence storage mode")
    digest = hashlib.sha256(output.read_bytes()).hexdigest()
    if digest != summary["bundle"]["sha256"]:
        output.unlink()
        raise ValueError("reproduced evidence differs from the pinned bundle checksum")
    print(f"Evidence bundle verified: {output} (sha256:{digest}, {files_count} files)")


if __name__ == "__main__":
    main()
