"""A source/unit green result must not bypass missing runtime evidence."""
import hashlib
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class ReleaseGateTests(unittest.TestCase):
    def test_production_inputs_are_all_canonical_sources(self):
        spec = importlib.util.spec_from_file_location("real_gate", ROOT / "scripts/check-v23-release-evidence.py")
        gate = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(gate)
        self.assertTrue(gate.production_sources(ROOT).issubset(gate.canonical_sources(ROOT)))

    def setUp(self):
        self.folder = tempfile.TemporaryDirectory()
        self.addCleanup(self.folder.cleanup)
        self.root = Path(self.folder.name)
        (self.root / "scripts").mkdir()
        self.script = self.root / "scripts/check-v23-release-evidence.py"
        shutil.copyfile(ROOT / "scripts/check-v23-release-evidence.py", self.script)
        spec = importlib.util.spec_from_file_location("release_gate", self.script)
        gate = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(gate)
        (self.root / "tests/e2e-v2/results").mkdir(parents=True)
        for name in gate.FIXED_SOURCES - {"scripts/check-v23-release-evidence.py"}:
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("reviewed source")
        (self.root / "cmd").mkdir()
        (self.root / "cmd/main.go").write_text("reviewed controller")
        proof = "tests/e2e-v2/results/proof.json"
        (self.root / proof).write_text('{"passed":true}')
        digest = lambda name: hashlib.sha256((self.root / name).read_bytes()).hexdigest()
        artifacts = {proof: digest(proof)}
        mapping = {}
        for name in gate.REQUIRED:
            path = f"tests/e2e-v2/results/gates/{name}.json"
            (self.root / path).parent.mkdir(parents=True, exist_ok=True)
            (self.root / path).write_text(json.dumps({"gate": name, "passed": True, "evidence": [proof]}))
            mapping[name] = path
            artifacts[path] = digest(path)
        self.report = {"releaseReady": True,
            "gates": {name: True for name in gate.REQUIRED}, "gateEvidence": mapping,
            "sourceSHA256": {name: digest(name) for name in gate.canonical_sources(self.root)},
            "artifactSHA256": artifacts}
        artifact_root = "tests/e2e-v2/results/final-acceptance-test"
        index = artifact_root + "/build/index.json"
        (self.root / index).parent.mkdir(parents=True)
        (self.root / index).write_text('{"manifests":[]}')
        artifacts[index] = digest(index)
        image_digest = "sha256:" + digest(index)
        self.report.update(fullAcceptanceMatrix=True, releaseBlockers=[], finalImageDigest=image_digest)
        self.full_path = "tests/e2e-v2/results/full-acceptance.json"
        self.full = {"passed": True, "imageDigest": image_digest, "artifactRoot": artifact_root,
            "sourceSHA256": self.report["sourceSHA256"], "runs": {},
            "cases": {name: {"passed": True, "source": "controlled", "evidence": [proof]} for name in gate.ACCEPTANCE_CASES},
            "timing": {}}
        build = artifact_root + "/build/provenance.json"
        self.store(build, {"imageIndexDigest": image_digest, "ociIndex": index, "binaryMatchesOCILayer": True,
            "productionSourcesSHA256": {name: digest(name) for name in gate.production_sources(self.root)}})
        self.full["buildProvenance"] = build
        for name in gate.ACCEPTANCE_RUNS:
            prefix = artifact_root + "/" + name + "/"
            result = {"allConfirmedIDsAccounted": True, "confirmed": 100, "consumedUnique": 100}
            if name in gate.RUN_CASES:
                key = "case" if name in ("platform", "operator", "pagination") else "name"
                result["cases"] = [{key: case, "passed": True, "stableCompletedRevision": 2, "upRevision": 3}
                    for case in sorted(gate.RUN_CASES[name])]
            if name == "native":
                result.update(scenario="native", healthyUpObjectiveMet=True, withoutBrokerReplacementOrRestart=True)
            if name == "scope":
                result.update(adminQueueTotal=50, restrictedQueueTotal=20, overviewPublishDelta=110,
                    selectedQueuePublishCopies=200, actualSelectedRateObserved=True, withoutBrokerResourceChanges=True,
                    durableMessageAccounting={"visibleSame": True})
            primary = prefix + gate.RUN_RESULTS[name]
            self.store(primary, result)
            image = prefix + "running-image.json"
            self.store(image, {"imageID": "local@" + image_digest, "policyHash": "policy-native", "clusterUID": "cluster"})
            self.full["runs"][name] = {"passed": True, "imageDigest": image_digest, "result": primary,
                "evidence": [primary], "runningImages": [image]}
        layouts = {"native": ("native", "result.json", "running-image.json", "inplace", 1),
            "paired": ("paired_and_rolling", "paired-timing.json", "01-paired-inplace/running-image.json", "inplace", 2),
            "rolling": ("paired_and_rolling", "rolling-operations.json", "03-explicit-rolling/running-image.json", "rolling", 3)}
        for label, (name, filename, image_file, path, revision) in layouts.items():
            prefix = artifact_root + "/" + name + "/"
            measured = json.loads((self.root / (prefix + filename)).read_text()) if label == "native" else {}
            measured.update(up={"episodeStart": "2026-10-30T00:00:00Z", "completedAt": "2026-10-30T00:01:00Z",
                "deadlineMissed": False, "stage": "Completed", "direction": "up", "path": path, "revision": revision,
                "policyHash": "policy-" + label, "clusterUID": "cluster"}, upConfirmedAt="2026-10-30T00:01:01Z")
            self.store(prefix + filename, measured)
            self.store(prefix + image_file, {"imageID": "local@" + image_digest, "policyHash": "policy-" + label,
                "clusterUID": "cluster"})
            self.full["runs"][name]["evidence"].append(prefix + filename)
            self.full["runs"][name]["runningImages"].append(prefix + image_file)
            self.full["timing"][label] = {"evidence": prefix + filename}
        self.image = artifact_root + "/native/running-image.json"
        self.timing = artifact_root + "/paired_and_rolling/rolling-operations.json"
        for name in ("platform", "operator", "pagination"):
            prefix = artifact_root + "/" + name + "/"
            self.store(prefix + "closure-complete.json", {"passed": True, "workloadAccountingAndCleanupCompleted": True,
                "expectedRuntimeDigest": image_digest, "group": "metrics" if name == "pagination" else name,
                "cases": sorted(gate.RUN_CASES[name])})
            if name == "platform":
                for case in gate.RUN_CASES[name]:
                    self.store(prefix + case + "-message-ids.json", {"confirmed": list(range(50)), "consumed": list(range(50)), "duplicates": 0})
            elif name == "operator":
                self.store(prefix + "operator-message-accounting.json", {"confirmed": list(range(300)), "consumed": list(range(300)), "duplicates": 0})
            else:
                self.store(prefix + "M-PAGING-message-ids.json", {"confirmed": {"queue": list(range(37))}, "consumed": {"queue": list(range(37))}})
        test_log = artifact_root + "/local/go-tests.jsonl"
        events = [{"Action": "pass", "Package": "repo/internal/v2", "Test": name}
            for names in gate.CASE_TESTS.values() for name in names]
        events += [{"Action": "pass", "Package": "repo" + suffix} for suffix in
            ("/internal/v2", "/internal/policy", "/internal/manifests", "/internal/config", "/internal/scaling", "/cmd/rmq-vertical-scaler")]
        (self.root / test_log).parent.mkdir(parents=True)
        (self.root / test_log).write_text("\n".join(json.dumps(e) for e in events))
        artifacts[test_log] = digest(test_log)
        checks = artifact_root + "/local/checks.json"
        self.store(checks, {"passed": True, "sourceSHA256": self.report["sourceSHA256"], "goTestsJSONL": test_log})
        self.full["localChecks"] = checks
        for name, case in self.full["cases"].items():
            case["source"] = "live" if name in gate.CASE_RUNS else "controlled"
            case["evidence"] = [self.full["runs"][run]["result"] for run in sorted(gate.CASE_RUNS[name])] if name in gate.CASE_RUNS else [test_log]
        self.store(self.full_path, self.full)

    def store(self, name, value):
        (self.root / name).parent.mkdir(parents=True, exist_ok=True)
        (self.root / name).write_text(json.dumps(value))
        self.report["artifactSHA256"][name] = hashlib.sha256((self.root / name).read_bytes()).hexdigest()

    def check(self):
        (self.root / "tests/e2e-v2/results/acceptance.json").write_text(json.dumps(self.report))
        return subprocess.run([sys.executable, str(self.script), "v2.3.0"],
                              capture_output=True, text=True).returncode

    def test_complete_matching_evidence_passes(self):
        self.assertEqual(self.check(), 0)

    def test_missing_runtime_gate_blocks_publication(self):
        self.report["gates"]["explicit_rolling"] = False
        self.assertNotEqual(self.check(), 0)

    def test_full_acceptance_flag_and_blockers_cannot_be_bypassed(self):
        self.report["fullAcceptanceMatrix"] = False
        self.assertNotEqual(self.check(), 0)
        self.report["fullAcceptanceMatrix"] = True
        self.report["releaseBlockers"] = ["rolling deadline missed"]
        self.assertNotEqual(self.check(), 0)

    def test_missing_matrix_case_or_runtime_run_blocks(self):
        case = self.full["cases"].pop("write_order")
        self.store(self.full_path, self.full)
        self.assertNotEqual(self.check(), 0)
        self.full["cases"]["write_order"] = case
        self.full["runs"].pop("platform")
        self.store(self.full_path, self.full)
        self.assertNotEqual(self.check(), 0)

    def test_failed_supporting_runtime_result_cannot_be_relabelled(self):
        result = self.full["runs"]["recovery"]["result"]
        self.store(result, {"passed": False, "error": "runtime case failed"})
        self.assertNotEqual(self.check(), 0)

    def test_fast_native_timing_cannot_replace_rolling_evidence(self):
        self.full["timing"]["rolling"] = dict(self.full["timing"]["native"])
        self.store(self.full_path, self.full)
        self.assertNotEqual(self.check(), 0)

    def test_rolling_operation_path_and_identity_must_match(self):
        data = json.loads((self.root / self.timing).read_text())
        for field, value in (("path", "inplace"), ("policyHash", "other"), ("clusterUID", "other"), ("revision", 99)):
            with self.subTest(field=field):
                original = data["up"][field]
                data["up"][field] = value
                self.store(self.timing, data)
                self.assertNotEqual(self.check(), 0)
                data["up"][field] = original

    def test_old_build_cannot_gain_current_source_provenance(self):
        path = self.full["buildProvenance"]
        build = json.loads((self.root / path).read_text())
        build["productionSourcesSHA256"]["cmd/main.go"] = "0" * 64
        self.store(path, build)
        self.assertNotEqual(self.check(), 0)

    def test_live_matrix_row_cannot_use_only_controlled_evidence(self):
        self.full["cases"]["rolling"]["source"] = "controlled"
        self.store(self.full_path, self.full)
        self.assertNotEqual(self.check(), 0)

    def test_incremental_operator_cases_cannot_hide_incomplete_accounting(self):
        prefix = self.full["artifactRoot"] + "/operator/"
        terminal = prefix + "closure-complete.json"
        record = json.loads((self.root / terminal).read_text())
        self.store(terminal, {**record, "workloadAccountingAndCleanupCompleted": False})
        self.assertNotEqual(self.check(), 0)
        self.store(terminal, record)
        self.store(prefix + "operator-message-accounting.json", {"confirmed": list(range(300)), "consumed": [1], "duplicates": 0})
        self.assertNotEqual(self.check(), 0)

    def test_queue_accounting_cannot_swap_scopes_with_equal_global_ids(self):
        path = self.full["artifactRoot"] + "/pagination/M-PAGING-message-ids.json"
        self.store(path, {"confirmed": {"vhost-a": list(range(20)), "vhost-b": list(range(20, 37))},
            "consumed": {"vhost-a": list(range(20, 37)), "vhost-b": list(range(20))}})
        self.assertNotEqual(self.check(), 0)

    def test_missing_named_regression_blocks_controlled_case(self):
        checks = json.loads((self.root / self.full["localChecks"]).read_text())
        path = checks["goTestsJSONL"]
        lines = [line for line in (self.root / path).read_text().splitlines() if "TestBoundaryJitterAndDuplicateSamples" not in line]
        (self.root / path).write_text("\n".join(lines))
        self.report["artifactSHA256"][path] = hashlib.sha256((self.root / path).read_bytes()).hexdigest()
        self.assertNotEqual(self.check(), 0)

    def test_runtime_image_readback_must_match_final_image(self):
        self.store(self.image, {"imageID": "sha256:" + "2" * 64})
        self.assertNotEqual(self.check(), 0)

    def test_final_acceptance_source_snapshot_must_match(self):
        self.full["sourceSHA256"] = {}
        self.store(self.full_path, self.full)
        self.assertNotEqual(self.check(), 0)

    def test_rolling_timing_cannot_be_relabelled_with_matching_hashes(self):
        timing = json.loads((self.root / self.timing).read_text())
        for field, value in (("completedAt", "2026-10-30T00:03:15Z"), ("deadlineMissed", True)):
            with self.subTest(field=field):
                original = timing["up"][field]
                timing["up"][field] = value
                self.store(self.timing, timing)
                self.assertNotEqual(self.check(), 0)
                timing["up"][field] = original
        timing["upConfirmedAt"] = "2026-10-30T00:02:00.001Z"
        self.store(self.timing, timing)
        self.assertNotEqual(self.check(), 0)

    def test_changed_source_or_artifact_blocks_publication(self):
        (self.root / "cmd/main.go").write_text("different source")
        self.assertNotEqual(self.check(), 0)
        self.report["sourceSHA256"]["cmd/main.go"] = hashlib.sha256((self.root / "cmd/main.go").read_bytes()).hexdigest()
        (self.root / "tests/e2e-v2/results/proof.json").write_text("different evidence")
        self.assertNotEqual(self.check(), 0)

    def test_omitted_or_new_controller_source_blocks_publication(self):
        self.report["sourceSHA256"].pop("cmd/main.go")
        self.assertNotEqual(self.check(), 0)

    def test_new_source_invalidates_previously_complete_fingerprints(self):
        (self.root / "cmd/new.go").write_text("unreviewed")
        self.assertNotEqual(self.check(), 0)

    def test_missing_gate_report_or_supporting_fingerprint_blocks(self):
        self.report["gateEvidence"].pop("explicit_rolling")
        self.assertNotEqual(self.check(), 0)

    def test_artifact_map_cannot_override_a_source_fingerprint(self):
        name = "cmd/main.go"
        (self.root / name).write_text("unreviewed source")
        self.report["artifactSHA256"][name] = hashlib.sha256((self.root / name).read_bytes()).hexdigest()
        self.assertNotEqual(self.check(), 0)

    def test_failing_gate_report_blocks_even_with_matching_hash(self):
        name = self.report["gateEvidence"]["explicit_rolling"]
        (self.root / name).write_text(json.dumps({"gate": "explicit_rolling", "passed": False, "evidence": ["tests/e2e-v2/results/proof.json"]}))
        self.report["artifactSHA256"][name] = hashlib.sha256((self.root / name).read_bytes()).hexdigest()
        self.assertNotEqual(self.check(), 0)


if __name__ == "__main__":
    unittest.main()
