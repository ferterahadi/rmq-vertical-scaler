#!/usr/bin/env python3
"""Fail 2.3 publication unless its checked-in acceptance evidence is complete."""
import hashlib
import json
import datetime as dt
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
REQUIRED = {"local_checks", "astra_review", "native_cycle", "paired_inplace", "strict_memory_block",
            "recovery", "queue_rate_scope", "explicit_rolling", "full_acceptance"}
ACCEPTANCE_RUNS = {"native", "recovery", "deferred", "paired_and_rolling", "scope", "platform", "operator", "pagination"}
ACCEPTANCE_CASES = {
    "legacy", "off", "queue_scope", "resource_triggers", "multiple_triggers", "jitter",
    "scale_up", "paired_resources", "concurrent_submissions", "scale_down", "missing_metrics",
    "duplicate_samples", "broker_census", "partial_deferred", "infeasible_permission",
    "transient_errors", "supersession", "scaler_restart", "partial_decrease_restart",
    "committed_up_restart", "off_pending", "identity_changes", "mixed_startup", "write_order",
    "persistence", "rolling", "no_disruption", "operator_convergence", "messages", "status", "packaging"}
FIXED_SOURCES = {"go.mod", "go.sum", "Dockerfile", "Makefile", ".github/workflows/release.yml",
                 "scripts/check-v23-release-evidence.py"}
SOURCE_GLOBS = ("cmd/**/*.go", "internal/**/*", "schema/**/*", "examples/v2/*.json",
                "examples/*.json", "examples/embed.go", "charts/rmq-vertical-scaler/**/*", "tests/feasibility/*.py",
                "tests/feasibility/*.yaml", "tests/e2e-v2/*.py", "tests/e2e-v2/requirements.txt")
RUN_RESULTS = {"native": "result.json", "recovery": "matrix-result.json", "deferred": "matrix-result.json",
    "paired_and_rolling": "matrix-result.json", "scope": "scope-result.json",
    "platform": "closure-result.json", "operator": "closure-result.json", "pagination": "closure-result.json"}
RUN_CASES = {
    "recovery": {"cpu-only", "memory-only", "resources", "combined-Off",
        "queue-only missing-memory hold and restart reset", "partial up, supersession, Off, restart without renewed overload",
        "partial down restart and per-step interval"},
    "deferred": {"real Deferred capacity recovery"},
    "paired_and_rolling": {"paired CPU/memory requests and CPU limits in place", "strict memory transition blocks before writes",
        "explicit full-memory rolling cycle"},
    "platform": {"P-QOS", "P-RESTART", "P-INFEASIBLE"},
    "operator": {"O-INTENT", "O-STRATEGY", "O-TEMPLATE", "O-PARTIAL", "O-DRIFT", "O-REPLACE"},
    "pagination": {"M-PAGING"}}
CASE_TESTS = {
    "jitter": {"TestBoundaryJitterAndDuplicateSamples"},
    "duplicate_samples": {"TestClosureCollectorCachedStaleAndOldContainerSamplesCannotStabilize"},
    "transient_errors": {"TestLostResizeReplyRecoversFromLiveStateWithoutResubmission", "TestReviewFailedReadInterruptsStabilization"},
    "mixed_startup": {"TestReviewMissingStateMixedPodsBlockWrites", "TestResourceValidityIsIndependentAndMemoryStillGuardsQueueOnly"}}
CASE_RUNS = {
    "off": {"recovery"}, "queue_scope": {"scope", "pagination"}, "resource_triggers": {"recovery"},
    "multiple_triggers": {"recovery"}, "scale_up": {"native", "paired_and_rolling"},
    "paired_resources": {"paired_and_rolling"}, "concurrent_submissions": {"operator"},
    "scale_down": {"native", "recovery"}, "missing_metrics": {"recovery"},
    "broker_census": {"operator", "pagination"}, "partial_deferred": {"recovery", "deferred"},
    "infeasible_permission": {"platform", "operator"}, "supersession": {"recovery"},
    "scaler_restart": {"recovery", "operator"}, "partial_decrease_restart": {"recovery"},
    "committed_up_restart": {"recovery"}, "off_pending": {"recovery"}, "identity_changes": {"operator"},
    "write_order": {"operator"}, "persistence": {"operator"}, "rolling": {"paired_and_rolling"},
    "no_disruption": {"native", "recovery"}, "operator_convergence": {"native", "operator"},
    "messages": ACCEPTANCE_RUNS, "status": {"platform", "operator", "recovery"}}


def canonical_sources(root):
    return FIXED_SOURCES | {p.relative_to(root).as_posix() for pattern in SOURCE_GLOBS
                            for p in root.glob(pattern) if p.is_file() and "__pycache__" not in p.parts}


def production_sources(root):
    patterns = ("cmd/**/*.go", "internal/**/*", "schema/**/*", "examples/*.json", "examples/v2/*.json", "examples/embed.go")
    return {"go.mod", "go.sum"} | {p.relative_to(root).as_posix() for pattern in patterns for p in root.glob(pattern)
        if p.is_file() and not p.name.endswith("_test.go") and "testdata" not in p.parts and "__pycache__" not in p.parts}


def checked_path(name):
    candidate = (ROOT / name).resolve()
    if not candidate.is_relative_to(ROOT) or not candidate.is_file():
        raise ValueError(f"missing or invalid evidence path: {name}")
    return candidate


def verify_full_acceptance(report, artifacts):
    if report.get("fullAcceptanceMatrix") is not True or report.get("releaseBlockers"):
        raise ValueError("full acceptance matrix is incomplete or has unresolved blockers")
    digest = report.get("finalImageDigest", "")
    if not isinstance(digest, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        raise ValueError("final acceptance requires an immutable image digest")
    name = "tests/e2e-v2/results/full-acceptance.json"
    if name not in artifacts:
        raise ValueError("missing full acceptance report")
    full = json.loads(checked_path(name).read_text())
    if (full.get("passed") is not True or full.get("imageDigest") != digest or
        full.get("sourceSHA256") != report.get("sourceSHA256")):
        raise ValueError("full acceptance differs from the final source/image snapshot")
    artifact_root = full.get("artifactRoot", "")
    if not isinstance(artifact_root, str) or not re.fullmatch(r"tests/e2e-v2/results/final-acceptance-[a-z0-9-]+", artifact_root):
        raise ValueError("invalid canonical acceptance artifact root")
    def read(name):
        if not isinstance(name, str) or name not in artifacts:
            raise ValueError("unfingerprinted acceptance artifact")
        return json.loads(checked_path(name).read_text())
    build = read(full.get("buildProvenance"))
    production = build.get("productionSourcesSHA256", {})
    if (build.get("imageIndexDigest") != digest or set(production) != production_sources(ROOT) or
        any(report["sourceSHA256"].get(p) != value for p, value in production.items()) or
        build.get("binaryMatchesOCILayer") is not True):
        raise ValueError("tested build provenance differs from current production sources/image")
    index = build.get("ociIndex")
    if index not in artifacts or "sha256:" + artifacts[index] != digest:
        raise ValueError("tested build OCI index differs from final image digest")
    def references(record):
        paths = record.get("evidence")
        if not isinstance(paths, list) or not paths or any(not isinstance(p, str) or p not in artifacts for p in paths):
            raise ValueError("full acceptance requires fingerprinted supporting evidence")
    runs = full.get("runs", {})
    if set(runs) != ACCEPTANCE_RUNS:
        raise ValueError("full acceptance is missing required runtime runs")
    results = {}
    for run_name, run in runs.items():
        if run.get("passed") is not True or run.get("imageDigest") != digest:
            raise ValueError("a runtime run failed or used a different image")
        references(run)
        path = f"{artifact_root}/{run_name}/{RUN_RESULTS[run_name]}"
        if run.get("result") != path or path not in run["evidence"]:
            raise ValueError("runtime result must be the named run's canonical artifact")
        result = read(path)
        results[run_name] = result
        if result.get("passed") is False or result.get("allConfirmedIDsAccounted") is False or result.get("error"):
            raise ValueError("supporting runtime result failed")
        if run_name in RUN_CASES:
            entries = result.get("cases", [])
            key = "case" if run_name in ("platform", "operator", "pagination") else "name"
            if (len(entries) != len(RUN_CASES[run_name]) or
                {c.get(key) for c in entries} != RUN_CASES[run_name] or any(c.get("passed") is not True for c in entries)):
                raise ValueError("runtime result is missing required passing cases")
        if run_name in ("native", "recovery", "deferred", "paired_and_rolling"):
            if (result.get("allConfirmedIDsAccounted") is not True or
                not isinstance(result.get("confirmed"), int) or result["confirmed"] <= 0 or
                result["confirmed"] != result.get("consumedUnique")):
                raise ValueError("runtime message accounting failed")
        if run_name == "native" and (result.get("scenario") != "native" or
            result.get("healthyUpObjectiveMet") is not True or result.get("withoutBrokerReplacementOrRestart") is not True):
            raise ValueError("native cycle failed allocation/timing/disruption checks")
        if run_name == "scope":
            if (result.get("adminQueueTotal") != 50 or result.get("restrictedQueueTotal") != 20 or
                result.get("overviewPublishDelta") != 110 or result.get("selectedQueuePublishCopies") != 200 or
                result.get("actualSelectedRateObserved") is not True or result.get("withoutBrokerResourceChanges") is not True or
                not result.get("durableMessageAccounting") or not all(v is True for v in result["durableMessageAccounting"].values())):
                raise ValueError("queue/rate scope or accounting failed")
        if run_name in ("platform", "operator", "pagination"):
            prefix = f"{artifact_root}/{run_name}/"
            terminal = read(prefix + "closure-complete.json")
            if (terminal.get("passed") is not True or terminal.get("workloadAccountingAndCleanupCompleted") is not True or
                terminal.get("expectedRuntimeDigest") != digest or
                terminal.get("group") != ("metrics" if run_name == "pagination" else run_name) or
                set(terminal.get("cases", [])) != RUN_CASES[run_name]):
                raise ValueError("closure group did not complete accounting and cleanup")
            def accounting(filename, expected_count, nested=False):
                account = read(prefix + filename)
                confirmed, consumed = account.get("confirmed"), account.get("consumed")
                if nested:
                    if not isinstance(confirmed, dict) or not isinstance(consumed, dict) or set(confirmed) != set(consumed):
                        raise ValueError("closure message accounting scope differs")
                    if any(not isinstance(ids, list) or not isinstance(consumed[key], list) or
                        len(ids) != len(consumed[key]) or set(ids) != set(consumed[key]) for key, ids in confirmed.items()):
                        raise ValueError("closure messages were consumed from a different scope")
                    confirmed = [item for group in confirmed.values() for item in group]
                    consumed = [item for group in consumed.values() for item in group]
                if (not isinstance(confirmed, list) or not isinstance(consumed, list) or
                    len(confirmed) != expected_count or len(set(confirmed)) != expected_count or
                    len(consumed) != expected_count or set(confirmed) != set(consumed) or account.get("duplicates", 0) != 0):
                    raise ValueError("closure message accounting is incomplete or mismatched")
            if run_name == "platform":
                for case in RUN_CASES[run_name]:
                    accounting(case + "-message-ids.json", 50)
            elif run_name == "operator":
                accounting("operator-message-accounting.json", 300)
            else:
                accounting("M-PAGING-message-ids.json", 37, nested=True)
        images = run.get("runningImages")
        if not isinstance(images, list) or not images:
            raise ValueError("runtime run lacks image readback")
        for path in images:
            if path not in artifacts or not path.startswith(f"{artifact_root}/{run_name}/"):
                raise ValueError("unfingerprinted runtime image readback")
            image = json.loads(checked_path(path).read_text())
            if image.get("imageID", "").rsplit("@", 1)[-1] != digest:
                raise ValueError("runtime image readback differs from final build")
    cases = full.get("cases", {})
    if set(cases) != ACCEPTANCE_CASES:
        raise ValueError("full acceptance is missing named matrix cases")
    checks = read(full.get("localChecks"))
    if checks.get("passed") is not True or checks.get("sourceSHA256") != report["sourceSHA256"]:
        raise ValueError("local checks differ from the final source snapshot")
    test_log = checks.get("goTestsJSONL")
    if test_log not in artifacts:
        raise ValueError("missing structured Go test evidence")
    events = [json.loads(line) for line in checked_path(test_log).read_text().splitlines()]
    if not events or any(e.get("Action") == "fail" for e in events):
        raise ValueError("Go regression evidence failed")
    passed_tests = {e["Test"] for e in events if e.get("Action") == "pass" and e.get("Test")}
    passed_packages = {e.get("Package", "") for e in events if e.get("Action") == "pass" and not e.get("Test")}
    if not passed_tests or not all(any(p.endswith(suffix) for p in passed_packages) for suffix in
        ("/internal/v2", "/internal/policy", "/internal/manifests", "/internal/config", "/internal/scaling", "/cmd/rmq-vertical-scaler")):
        raise ValueError("complete compatibility and controller regression packages were not tested")
    for case_name, case in cases.items():
        if case.get("passed") is not True or case.get("source") not in ("live", "controlled", "local", "live and controlled"):
            raise ValueError("invalid or failing acceptance case")
        references(case)
        if case_name in CASE_RUNS:
            if case["source"] not in ("live", "live and controlled") or any(
                runs[name]["result"] not in case["evidence"] for name in CASE_RUNS[case_name]):
                raise ValueError("matrix case lacks its required live run evidence")
        else:
            if case["source"] not in ("controlled", "local") or test_log not in case["evidence"]:
                raise ValueError("matrix case lacks controlled/local test evidence")
            if not CASE_TESTS.get(case_name, set()).issubset(passed_tests):
                raise ValueError("matrix case lacks its named passing regressions")
    timing = full.get("timing", {})
    if set(timing) != {"native", "paired", "rolling"}:
        raise ValueError("all healthy scale-up paths require timing evidence")
    layouts = {"native": ("native", "result.json", "running-image.json", "inplace"),
        "paired": ("paired_and_rolling", "paired-timing.json", "01-paired-inplace/running-image.json", "inplace"),
        "rolling": ("paired_and_rolling", "rolling-operations.json", "03-explicit-rolling/running-image.json", "rolling")}
    for label, record in timing.items():
        run_name, filename, image_file, expected_path = layouts[label]
        path = f"{artifact_root}/{run_name}/{filename}"
        if record.get("evidence") != path or path not in runs[run_name]["evidence"]:
            raise ValueError("timing evidence belongs to another runtime run")
        measured = read(path)
        image_path = f"{artifact_root}/{run_name}/{image_file}"
        if image_path not in runs[run_name]["runningImages"]:
            raise ValueError("timing policy lacks matching runtime image readback")
        image = read(image_path)
        operation = measured["up"]
        if (operation.get("path") != expected_path or operation.get("policyHash") != image.get("policyHash") or
            operation.get("clusterUID") != image.get("clusterUID") or not operation.get("policyHash") or
            not operation.get("clusterUID") or not isinstance(operation.get("revision"), int) or operation["revision"] <= 0):
            raise ValueError("timing operation differs from the tested path/policy/cluster")
        if label in ("paired", "rolling"):
            case_name = "paired CPU/memory requests and CPU limits in place" if label == "paired" else "explicit full-memory rolling cycle"
            case = next(c for c in results[run_name]["cases"] if c["name"] == case_name)
            revision_key = "stableCompletedRevision" if label == "paired" else "upRevision"
            if case.get(revision_key) != operation["revision"]:
                raise ValueError("timing operation revision differs from the retained run result")
        start = dt.datetime.fromisoformat(operation["episodeStart"].replace("Z", "+00:00"))
        completed = dt.datetime.fromisoformat(operation["completedAt"].replace("Z", "+00:00"))
        observed = dt.datetime.fromisoformat(measured["upConfirmedAt"].replace("Z", "+00:00"))
        elapsed = (completed - start).total_seconds()
        upper_bound = (observed - start).total_seconds()
        if (operation.get("stage") != "Completed" or operation.get("direction") != "up" or
            operation.get("deadlineMissed") is not False or not 0 <= elapsed <= upper_bound <= 120):
            raise ValueError("healthy scale-up failed the 120-second objective")


def verify_evidence(report):
    sources = report.get("sourceSHA256", {})
    if set(sources) != canonical_sources(ROOT):
        raise ValueError("source fingerprint must cover the complete canonical source set")
    artifacts = report.get("artifactSHA256", {})
    if any(not name.startswith(("tests/e2e-v2/results/", "tests/feasibility/results/")) or
           name == "tests/e2e-v2/results/acceptance.json" for name in artifacts):
        raise ValueError("artifact fingerprints must name retained evidence, separate from sources and acceptance")
    mapping = report.get("gateEvidence", {})
    if set(mapping) != REQUIRED:
        raise ValueError("every required gate must name its evidence report")
    for gate in sorted(REQUIRED):
        name = mapping[gate]
        if name != f"tests/e2e-v2/results/gates/{gate}.json" or name not in artifacts:
            raise ValueError(f"missing canonical report for {gate}")
        evidence = json.loads(checked_path(name).read_text())
        if evidence.get("gate") != gate or evidence.get("passed") is not True:
            raise ValueError(f"invalid or failing evidence report for {gate}")
        references = evidence.get("evidence")
        if not isinstance(references, list) or not references or any(not isinstance(p, str) for p in references):
            raise ValueError(f"{gate} requires retained supporting evidence")
        for reference in references:
            if reference not in artifacts or not reference.startswith(("tests/e2e-v2/results/", "tests/feasibility/results/")) or reference == name:
                raise ValueError(f"unfingerprinted supporting evidence for {gate}: {reference}")
    for name, expected in {**sources, **artifacts}.items():
        if not isinstance(expected, str) or not re.fullmatch(r"[0-9a-f]{64}", expected):
            raise ValueError(f"invalid fingerprint for {name}")
        if hashlib.sha256(checked_path(name).read_bytes()).hexdigest() != expected:
            raise ValueError(f"source or artifact differs: {name}")
    verify_full_acceptance(report, artifacts)


def main():
    version = sys.argv[1] if len(sys.argv) == 2 else ""
    if not re.match(r"^v?2\.3\.", version):
        print("Version is outside the 2.3 evidence gate.")
        return 0
    path = ROOT / "tests/e2e-v2/results/acceptance.json"
    report = json.loads(path.read_text())
    missing = sorted(key for key in REQUIRED if report.get("gates", {}).get(key) is not True)
    if report.get("releaseReady") is not True or missing:
        print("2.3 publication blocked: " + (", ".join(missing) or "releaseReady is false"))
        return 1
    try:
        verify_evidence(report)
    except (ValueError, TypeError, OSError, KeyError, AttributeError) as error:
        print(f"2.3 publication blocked: {error}")
        return 1
    print("2.3 acceptance evidence matches the reviewed sources and retained artifacts.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
