"""Packaging retains acceptance semantics and rejects unsafe archives."""
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import shutil
import subprocess
import tarfile
import unittest

import test_release_gate as fixtures

ROOT = Path(__file__).resolve().parents[2]


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


class EvidenceBundleTests(unittest.TestCase):
    def setUp(self):
        fixture = fixtures.ReleaseGateTests("test_complete_matching_evidence_passes")
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        fixture.full["boundaries"] = ["Local fixture only; rolling requires explicit deployment tuning."]
        fixture.store(fixture.full_path, fixture.full)
        self.root = fixture.root
        self.gate = module("bundle_gate", fixture.script)
        self.package = module("bundle_package", ROOT / "scripts/package-v3-evidence.py")
        for name in self.gate.PACKAGING_SOURCES:
            path = self.root / name
            if not path.exists():
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("new packaging-only source")
        report = "tests/e2e-v3/results/acceptance.json"
        (self.root / report).write_text(json.dumps(fixture.report))
        self.files = {p: (self.root / p).read_bytes() for p in fixture.report["artifactSHA256"]}
        self.files[report] = (self.root / report).read_bytes()
        self.bundle = self.root / "evidence.tar.gz"
        self.summary = {"schemaVersion": 1, "passed": True, "imageDigest": fixture.report["finalImageDigest"],
            "requirements": len(fixture.full["cases"]), "runtimeGroups": sorted(fixture.full["runs"]),
            "gates": dict(fixture.report["gates"]),
            "boundaries": list(fixture.full["boundaries"]),
            "coverage": {name: {"passed": record["passed"], "source": record["source"]}
                         for name, record in fixture.full["cases"].items()},
            "independentScaleUpSeconds": {name: 61.0 for name in fixture.full["timing"]},
            "packagingSourcesSHA256": {p: hashlib.sha256((self.root / p).read_bytes()).hexdigest()
                for p in self.gate.PACKAGING_SOURCES},
            "bundle": {"sourceCommit": "a" * 40}}
        self.repack()

    def repack(self):
        self.package.write_bundle(self.files, self.bundle)
        self.summary["bundle"]["sha256"] = hashlib.sha256(self.bundle.read_bytes()).hexdigest()
        self.summary["bundle"]["bytes"] = self.bundle.stat().st_size
        self.summary["bundle"]["files"] = len(self.files)

    def test_complete_bundle_passes_without_raw_checkout_captures(self):
        shutil.rmtree(self.root / "tests/e2e-v3/results")
        self.gate.verify_bundle(self.summary, self.bundle)

    def test_reproduction_has_identical_bytes(self):
        second = self.root / "different-name.tar.gz"
        self.package.write_bundle(dict(reversed(list(self.files.items()))), second)
        self.assertEqual(self.bundle.read_bytes(), second.read_bytes())

    def test_reconstruction_requires_retained_ancestry_after_merge(self):
        repo = self.root / "history"
        repo.mkdir()
        def git(*args):
            return subprocess.check_output(["git", "-c", "user.name=Evidence test", "-c",
                "user.email=evidence-test@example.invalid", *args], cwd=repo, stderr=subprocess.DEVNULL)
        git("init", "-b", "main")
        git("commit", "--allow-empty", "-m", "evidence source")
        commit = git("rev-parse", "HEAD").decode().strip()
        self.package.require_source_history(repo, commit)
        git("checkout", "--orphan", "squashed")
        git("commit", "--allow-empty", "-m", "squash without source ancestry")
        with self.assertRaisesRegex(ValueError, "merge commit"):
            self.package.require_source_history(repo, commit)

    def test_missing_or_corrupt_bundle_blocks(self):
        with self.assertRaises(ValueError):
            self.gate.verify_bundle(self.summary, self.root / "missing.tar.gz")
        with self.bundle.open("ab") as output:
            output.write(b"corrupt")
        with self.assertRaises(ValueError):
            self.gate.verify_bundle(self.summary, self.bundle)

    def test_historical_bundle_cannot_clear_current_source_release_block(self):
        self.summary["currentSourceAccepted"] = False
        with self.assertRaisesRegex(ValueError, "current sources await"):
            self.gate.verify_bundle(self.summary, self.bundle)
        self.summary["currentSourceAccepted"] = True
        self.summary["releaseBlockers"] = ["new controller requires final-image runtime acceptance"]
        with self.assertRaisesRegex(ValueError, "current sources await"):
            self.gate.verify_bundle(self.summary, self.bundle)

    def test_missing_or_extra_member_blocks_even_with_new_archive_hash(self):
        name = next(p for p in self.files if p.endswith("proof.json"))
        content = self.files.pop(name)
        self.repack()
        with self.assertRaises(ValueError):
            self.gate.verify_bundle(self.summary, self.bundle)
        self.files[name] = content
        self.files["tests/e2e-v3/results/extra.json"] = b"{}"
        self.repack()
        with self.assertRaises(ValueError):
            self.gate.verify_bundle(self.summary, self.bundle)

    def test_tampered_captures_block_even_with_new_archive_hash(self):
        self.files["tests/e2e-v3/results/proof.json"] = b'{"passed":false}'
        self.repack()
        with self.assertRaises(ValueError):
            self.gate.verify_bundle(self.summary, self.bundle)

    def test_archive_rejects_traversal_links_and_duplicates(self):
        for name, link in (("../outside", False), ("/absolute", False),
                           ("tests/e2e-v3/results/link", True),
                           ("tests/e2e-v3/results/proof.json", False)):
            with self.subTest(name=name):
                with tarfile.open(self.bundle, "w:gz") as archive:
                    if name.endswith("proof.json"):
                        archive.addfile(tarfile.TarInfo(name), io.BytesIO())
                    info = tarfile.TarInfo(name)
                    if link:
                        info.type = tarfile.SYMTYPE
                        info.linkname = "../../outside"
                    archive.addfile(info, io.BytesIO())
                self.summary["bundle"]["sha256"] = hashlib.sha256(self.bundle.read_bytes()).hexdigest()
                with self.assertRaises(ValueError):
                    self.gate.verify_bundle(self.summary, self.bundle)
                self.assertFalse((self.root.parent / "outside").exists())

    def test_new_production_source_or_changed_controller_blocks(self):
        path = self.root / "cmd/main.go"
        original = path.read_bytes()
        path.write_text("changed controller")
        with self.assertRaises(ValueError):
            self.gate.verify_bundle(self.summary, self.bundle)
        self.summary["packagingSourcesSHA256"]["cmd/main.go"] = hashlib.sha256(path.read_bytes()).hexdigest()
        with self.assertRaises(ValueError):
            self.gate.verify_bundle(self.summary, self.bundle)
        del self.summary["packagingSourcesSHA256"]["cmd/main.go"]
        path.write_bytes(original)
        (self.root / "cmd/new.go").write_text("new controller input")
        with self.assertRaises(ValueError):
            self.gate.verify_bundle(self.summary, self.bundle)

    def test_changed_unpinned_packaging_source_or_summary_image_blocks(self):
        path = self.root / "scripts/package-v3-evidence.py"
        old = path.read_bytes()
        path.write_text("unverified packager")
        with self.assertRaises(ValueError):
            self.gate.verify_bundle(self.summary, self.bundle)
        path.write_bytes(old)
        self.summary["imageDigest"] = "sha256:" + "f" * 64
        with self.assertRaises(ValueError):
            self.gate.verify_bundle(self.summary, self.bundle)

    def test_concise_summary_cannot_misreport_counts_coverage_or_timing(self):
        for key, value in (("requirements", 99), ("coverage", {}), ("runtimeGroups", []),
                           ("gates", {}), ("boundaries", []), ("independentScaleUpSeconds", {"rolling": 1.0})):
            with self.subTest(key=key):
                original = self.summary[key]
                self.summary[key] = value
                with self.assertRaises(ValueError):
                    self.gate.verify_bundle(self.summary, self.bundle)
                self.summary[key] = original

    def test_descriptor_size_and_member_count_must_match_archive(self):
        for key in ("bytes", "files"):
            with self.subTest(key=key):
                original = self.summary["bundle"][key]
                self.summary["bundle"][key] = 0
                with self.assertRaises(ValueError):
                    self.gate.verify_bundle(self.summary, self.bundle)
                self.summary["bundle"][key] = original

    def test_oversized_archive_is_rejected_before_extraction(self):
        with tarfile.open(self.bundle, "w:gz") as archive:
            info = tarfile.TarInfo("tests/e2e-v3/results/oversized.json")
            info.size = 65 * 1024 * 1024
            # Write only a header; the verifier must reject before reading data.
            archive.fileobj.write(info.tobuf())
        self.summary["bundle"]["sha256"] = hashlib.sha256(self.bundle.read_bytes()).hexdigest()
        with self.assertRaises(ValueError):
            self.gate.verify_bundle(self.summary, self.bundle)


    def test_download_actions_bundle_uses_pinned_id_and_normal_auth(self):
        from unittest.mock import patch
        import zipfile
        payload = io.BytesIO()
        with zipfile.ZipFile(payload, "w") as archive:
            archive.writestr("rmq-v3-evidence.tar.gz", b"pinned bytes")
        target = self.root / "downloaded.tar.gz"
        def download(command, stdout, check):
            self.assertEqual(command, ["gh", "api", "repos/ferterahadi/rmq-vertical-scaler/actions/artifacts/123/zip"])
            self.assertTrue(check)
            stdout.write(payload.getvalue())
        with patch.object(self.package.subprocess, "run", side_effect=download):
            self.package.download_actions_bundle(123, target)
        self.assertEqual(target.read_bytes(), b"pinned bytes")

    def test_download_actions_bundle_rejects_invalid_ids_and_members(self):
        from unittest.mock import patch
        import zipfile
        target = self.root / "downloaded.tar.gz"
        for value in (0, -1, True, "123", "123/zip?other=456"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                self.package.download_actions_bundle(value, target)
        for names in (["../escape"], ["unexpected"], ["rmq-v3-evidence.tar.gz", "extra"]):
            payload = io.BytesIO()
            with zipfile.ZipFile(payload, "w") as archive:
                for name in names:
                    archive.writestr(name, b"content")
            def download(command, stdout, check):
                stdout.write(payload.getvalue())
            with self.subTest(names=names), patch.object(self.package.subprocess, "run", side_effect=download):
                with self.assertRaisesRegex(ValueError, "unexpected evidence"):
                    self.package.download_actions_bundle(123, target)
            self.assertFalse(target.exists())

    def test_downloaded_bundle_checksums_without_git_history(self):
        from contextlib import redirect_stdout
        from unittest.mock import patch
        original = self.bundle.read_bytes()
        self.summary["bundle"].update(storage="github-actions", artifactId=123,
                                      localPath="downloaded.tar.gz")
        path = self.root / "tests/e2e-v3/acceptance-summary.json"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(self.summary))
        for data, valid in ((original, True), (b"corrupt bytes", False)):
            def download(artifact, output):
                self.assertEqual(artifact, 123)
                output.write_bytes(data)
            with self.subTest(valid=valid), patch.object(self.package, "ROOT", self.root), \
                    patch.object(self.package, "download_actions_bundle", side_effect=download), \
                    patch.object(self.package, "require_source_history") as history, \
                    patch("sys.argv", ["package-v3-evidence.py"]), redirect_stdout(io.StringIO()):
                if valid:
                    self.package.main()
                    self.assertEqual((self.root / "downloaded.tar.gz").read_bytes(), original)
                else:
                    with self.assertRaisesRegex(ValueError, "checksum"):
                        self.package.main()
                    self.assertFalse((self.root / "downloaded.tar.gz").exists())
                history.assert_not_called()


if __name__ == "__main__":
    unittest.main()
