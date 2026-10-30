#!/usr/bin/env python3
"""Exercise policy isolation and recovery on a completed local v2 fixture.

Run only after run.py's cycle has returned to the baseline. Every resource is
bound to an explicit loopback kind context and the labeled test namespace.
"""
import argparse
import datetime as dt
import hashlib
import json
from pathlib import Path
import subprocess
import time

import pika
from run import Cycle, ROOT, probe_module, utc


class Matrix(Cycle):
    def __init__(self, args):
        super().__init__(args)
        (self.output / "matrix.py").write_bytes(Path(__file__).read_bytes())
        self.sequence = 0
        self.message_sequence = 0
        self.cases = []
        self.policy_data = None
        self.admission_name = "rmq-v23-deny-native"

    def event(self, name, data=None):
        print(f"Case: {name}", flush=True)
        with (self.output / "events.jsonl").open("a") as file:
            file.write(json.dumps({"time": utc(), "case": name, "data": data}) + "\n")

    def base(self):
        policy = super().policy()
        policy["behavior"]["scaleDown"]["stepIntervalSeconds"] = 60
        return policy

    def install(self, policy, name):
        self.sequence += 1
        folder = self.output / f"{self.sequence:02d}-{name}"
        folder.mkdir()
        path = folder / "policy.json"
        path.write_text(json.dumps(policy, indent=2) + "\n")
        manifest = folder / "scaler.yaml"
        subprocess.run(["go", "run", "./cmd/rmq-vertical-scaler", "generate", "--config", str(path),
            "--image", self.args.image, "--output", str(manifest)], cwd=ROOT, check=True,
            stdout=subprocess.DEVNULL)
        documents = [json.loads(doc) for doc in manifest.read_text().split("\n---\n")]
        policy_bytes = next(d["data"]["policy.json"] for d in documents if d["kind"] == "ConfigMap")
        expected = hashlib.sha256(policy_bytes.encode()).hexdigest()
        self.probe.kubectl("apply", "-f", str(manifest))
        self.probe.wait(f"new {name} policy observed", lambda: self.observe()["policyHash"] == expected, timeout=180)
        self.policy_data = json.loads(json.dumps(policy))
        return self.status()

    def current_pods(self):
        return {p["metadata"]["name"]: p for p in self.probe.pods()}

    def resources_unchanged(self, baseline):
        current = self.current_pods()
        return all(p["metadata"]["uid"] == current[name]["metadata"]["uid"] and
            self.probe.status(p).get("containerID") == self.probe.status(current[name]).get("containerID") and
            probe_module.equal_resources(self.probe.container(current[name])["resources"], self.probe.container(p)["resources"]) and
            probe_module.equal_resources(self.probe.status(current[name]).get("resources", {}), self.probe.status(p).get("resources", {}))
            for name, p in baseline.items())

    def hold(self, seconds, check, description):
        end = time.monotonic() + seconds
        while True:
            self.observe()
            if not check():
                raise RuntimeError(description)
            if time.monotonic() >= end:
                break
            time.sleep(5)

    def burst(self, count):
        with self.connection() as connection:
            channel = connection.channel()
            channel.confirm_delivery()
            for _ in range(count):
                message_id = f"matrix-{self.message_sequence}"
                channel.basic_publish(exchange="", routing_key="v23-accounted",
                    body=(message_id + ":" + "x" * 200).encode(),
                    properties=pika.BasicProperties(message_id=message_id, delivery_mode=2), mandatory=True)
                self.confirmed.add(message_id)
                self.message_sequence += 1
        self.save("matrix-confirmed-ids.json", sorted(self.confirmed))

    def metrics_access(self, enabled):
        role = self.probe.get("role", "rmq-vertical-scaler-role")
        if enabled:
            if not any("metrics.k8s.io" in r.get("apiGroups", []) for r in role["rules"]):
                role["rules"].append({"apiGroups": ["metrics.k8s.io"], "resources": ["pods"], "verbs": ["get", "list"]})
        else:
            role["rules"] = [r for r in role["rules"] if "metrics.k8s.io" not in r.get("apiGroups", [])]
        path = self.output / "role-change.json"
        path.write_text(json.dumps(role))
        self.probe.kubectl("replace", "-f", str(path))

    def restart_scaler(self, key):
        before = self.probe.kubectl("get", "pods", "-n", self.namespace, "-l", "app=rmq-vertical-scaler",
            "-o", "json", as_json=True)["items"]
        old = {p["metadata"]["uid"] for p in before}
        self.probe.kubectl("patch", "deployment", "rmq-vertical-scaler", "-n", self.namespace, "--type=merge", "-p",
            json.dumps({"spec": {"template": {"metadata": {"annotations": {"test/restart": key + utc()}}}}}))
        def ready():
            pods = self.probe.kubectl("get", "pods", "-n", self.namespace, "-l", "app=rmq-vertical-scaler", "-o", "json", as_json=True)["items"]
            if len([p for p in pods if not p["metadata"].get("deletionTimestamp")]) > 1:
                raise RuntimeError("overlapping live scaler replicas")
            return len(pods) == 1 and pods[0]["metadata"]["uid"] not in old and any(
                s.get("ready") for s in pods[0].get("status", {}).get("containerStatuses", []))
        self.probe.wait("replacement scaler ready without rollout overlap", ready, timeout=180)
        return self.scaler_started()

    def deny_one(self, enabled):
        name = self.admission_name
        if not enabled:
            for kind in ("validatingadmissionpolicybinding", "validatingadmissionpolicy"):
                try:
                    resource = self.probe.kubectl("get", kind, name, "-o", "json", as_json=True)
                except RuntimeError as error:
                    if "NotFound" in str(error):
                        continue
                    raise
                if resource["metadata"].get("labels", {}).get("rmq-vertical-scaler-test") != "v23-matrix":
                    raise RuntimeError("refusing to delete an unrelated admission resource")
                self.probe.kubectl("delete", kind, name)
            return
        self.probe.kubectl("label", "namespace", self.namespace, f"rmq-v23-matrix={self.namespace}", "--overwrite")
        labels = {"rmq-vertical-scaler-test": "v23-matrix"}
        selector = {"matchLabels": {"rmq-v23-matrix": self.namespace}}
        policy = {"apiVersion": "admissionregistration.k8s.io/v1", "kind": "ValidatingAdmissionPolicy",
            "metadata": {"name": name, "labels": labels}, "spec": {"failurePolicy": "Fail",
            "matchConstraints": {"namespaceSelector": selector, "resourceRules": [{"apiGroups": [""],
                "apiVersions": ["v1"], "operations": ["UPDATE"], "resources": ["pods/resize"]}]},
            "validations": [{"expression": 'object.metadata.name != "rmq-probe-server-1"',
                "message": "v23 deliberate one-broker resize denial"}]}}
        binding = {"apiVersion": "admissionregistration.k8s.io/v1", "kind": "ValidatingAdmissionPolicyBinding",
            "metadata": {"name": name, "labels": labels}, "spec": {"policyName": name,
            "validationActions": ["Deny"], "matchResources": {"namespaceSelector": selector}}}
        for resource in (policy, binding):
            try:
                existing = self.probe.kubectl("get", resource["kind"], name, "-o", "json", as_json=True)
                if existing["metadata"].get("labels", {}).get("rmq-vertical-scaler-test") != "v23-matrix":
                    raise RuntimeError("admission test-name collision")
            except RuntimeError as error:
                if "NotFound" not in str(error):
                    raise
            path = self.output / (resource["kind"] + ".json")
            path.write_text(json.dumps(resource, indent=2))
            self.probe.kubectl("apply", "-f", str(path))

    def off_cases(self):
        baseline = self.current_pods()
        for name, types in (("cpu-only", ["cpu"]), ("memory-only", ["memory"]), ("resources", ["cpu", "memory"])):
            self.event(name + "-Off")
            policy = self.base()
            policy["updatePolicy"]["mode"] = "Off"
            policy.pop("rmq")
            policy["triggers"] = [{"type": typ, "metricType": "Utilization", "metadata": {}} for typ in types]
            self.install(policy, name)
            self.probe.wait("independent resource sources available", lambda: all(s["complete"] for s in self.observe()["observation"]["signals"]), timeout=120)
            self.hold(15, lambda: self.resources_unchanged(baseline), "Off changed broker resources")
            role = self.probe.get("role", "rmq-vertical-scaler-role")
            if any("pods/resize" in r["resources"] for r in role["rules"]):
                raise RuntimeError("Off retained resize privileges")
            self.cases.append({"name": name, "passed": True})
        self.event("combined-Off")
        policy = self.base()
        policy["updatePolicy"]["mode"] = "Off"
        policy["triggers"] += [{"type": typ, "metricType": "Utilization", "metadata": {}} for typ in ("cpu", "memory")]
        self.install(policy, "combined-Off")
        self.burst(500)
        self.probe.wait("Off recommends HIGH while resources stay LOW", lambda: self.observe()["decision"]["recommendedProfileIndex"] == 1, timeout=90)
        self.hold(30, lambda: self.resources_unchanged(baseline), "combined Off mutated brokers")
        self.drain()
        self.cases.append({"name": "combined-Off", "passed": True})

    def missing_metrics_and_restart(self):
        self.event("queue-up-without-memory-metrics")
        policy = self.base()
        self.install(policy, "queue-Auto")
        self.metrics_access(False)
        self.burst(500)
        self.probe.wait("queue demand reaches HIGH without Metrics API access", lambda: self.completed("up") and
            self.probe.all_at(policy["profiles"]["HIGH"]), timeout=210)
        self.drain()
        high = self.current_pods()
        self.hold(75, lambda: self.resources_unchanged(high) and self.status().get("operation") is None,
                  "decreased without fresh memory metrics")
        self.metrics_access(True)
        self.probe.wait("fresh memory starts decrease observation", lambda: self.observe()["reason"] in
            ("ObservingDecrease", "ObservingMemoryCoverage"), timeout=90)
        self.hold(20, lambda: self.resources_unchanged(high), "down step ignored observation interval")
        boot = self.restart_scaler("down-observation-reset")
        self.probe.wait("fresh full down window after restart", lambda: self.completed("down") and
            self.probe.all_at(probe_module.BASELINE), timeout=240)
        down = self.status()["lastOperation"]
        elapsed = (dt.datetime.fromisoformat(down["started"].replace("Z", "+00:00")) -
                   dt.datetime.fromisoformat(boot.replace("Z", "+00:00"))).total_seconds()
        if elapsed < 60:
            raise RuntimeError("restart restored old downscale eligibility")
        self.cases.append({"name": "queue-only missing-memory hold and restart reset", "passed": True,
                           "downStartedAfterNewContainerSeconds": elapsed})

    def partial(self, target, target_cpu, lower_cpu):
        state = self.observe()
        operation = state.get("operation") or {}
        if operation.get("target") != target:
            return False
        progress = operation.get("brokers", {}).get("rmq-probe-server-1", {})
        if "v23 deliberate" not in progress.get("error", ""):
            return False
        values = {name: self.probe.status(p).get("resources", {}).get("requests", {}).get("cpu")
                  for name, p in self.current_pods().items()}
        return (probe_module.quantity(values.get("rmq-probe-server-0", "0")) == probe_module.quantity(target_cpu) and
                probe_module.quantity(values.get("rmq-probe-server-2", "0")) == probe_module.quantity(target_cpu) and
                probe_module.quantity(values.get("rmq-probe-server-1", "0")) == probe_module.quantity(lower_cpu))

    def partial_and_supersession(self):
        self.event("partial-up-and-higher-supersession")
        policy = self.base()
        medium = json.loads(json.dumps(policy["profiles"]["HIGH"]))
        medium["requests"]["cpu"] = "400m"
        policy["profiles"]["MEDIUM"] = medium
        policy["profileOrder"] = ["LOW", "MEDIUM", "HIGH"]
        policy["triggers"][0]["metadata"]["thresholds"] = {"MEDIUM": 10, "HIGH": 1000}
        self.install(policy, "three-profile-recovery")
        self.deny_one(True)
        self.burst(400)
        self.probe.wait("two brokers MEDIUM; one denied and retained", lambda: self.partial("MEDIUM", "400m", "100m"), timeout=210)
        medium_operation = self.status()["operation"]
        self.save("partial-medium-operation.json", medium_operation)
        self.burst(1200)
        self.probe.wait("higher HIGH target supersedes unresolved MEDIUM", lambda: self.partial("HIGH", "800m", "100m"), timeout=210)
        high_operation = self.status()["operation"]
        if high_operation["revision"] <= medium_operation["revision"]:
            raise RuntimeError("superseding target reused its revision")
        self.save("partial-high-operation.json", high_operation)
        self.drain()  # demand is gone BEFORE process restart and target repair
        baseline = self.current_pods()
        off = json.loads(json.dumps(policy))
        off["updatePolicy"]["mode"] = "Off"
        self.install(off, "Off-with-pending-operation")
        attempts = {name: p["attempts"] for name, p in self.status()["operation"]["brokers"].items()}
        self.hold(35, lambda: self.resources_unchanged(baseline) and
            {name: p["attempts"] for name, p in self.status()["operation"]["brokers"].items()} == attempts,
            "Off retried or changed the retained operation")
        self.install(policy, "Auto-rebuild-committed-up-window")
        boot = self.scaler_started()
        self.deny_one(False)
        self.hold(15, lambda: probe_module.quantity(self.probe.status(self.current_pods()["rmq-probe-server-1"])
            .get("resources", {}).get("requests", {}).get("cpu", "0")) == probe_module.quantity("100m"),
            "restart repaired unsent broker before fresh up interval")
        self.probe.wait("committed HIGH finishes even after queue demand falls", lambda: self.completed("up") and
            self.probe.all_at(policy["profiles"]["HIGH"]), timeout=180)
        recovered = self.status()["lastOperation"]
        if recovered["revision"] != high_operation["revision"]:
            raise RuntimeError("recovery replaced the committed target revision")
        for name in ("rmq-probe-server-0", "rmq-probe-server-2"):
            if recovered["brokers"][name]["attempts"] != high_operation["brokers"][name]["attempts"]:
                raise RuntimeError("recovery resubmitted an already successful broker")
        self.cases.append({"name": "partial up, supersession, Off, restart without renewed overload", "passed": True,
                           "scalerContainerStartedAt": boot, "recoveredRevision": recovered["revision"]})
        self.event("partial-down-recovery-rebuilds-window")
        self.deny_one(True)
        self.probe.wait("two brokers decrease to MEDIUM; one remains HIGH", lambda: self.partial("MEDIUM", "400m", "800m"), timeout=240)
        down_operation = self.status()["operation"]
        self.save("partial-down-operation.json", down_operation)
        boot = self.restart_scaler("partial-down-recovery")
        self.deny_one(False)
        self.hold(35, lambda: probe_module.quantity(self.probe.status(self.current_pods()["rmq-probe-server-1"])
            .get("resources", {}).get("requests", {}).get("cpu", "0")) == probe_module.quantity("800m"),
            "partial down restarted decreasing writes before rebuilt safety window")
        self.probe.wait("retained MEDIUM down target finishes without deadlock", lambda: self.completed("down") and
            self.probe.all_at(medium), timeout=240)
        medium_down = self.status()["lastOperation"]
        if medium_down["revision"] != down_operation["revision"]:
            raise RuntimeError("down recovery skipped to a different target")
        self.save("recovered-medium-down.json", medium_down)
        self.probe.wait("next LOW step follows its own complete interval", lambda: self.completed("down") and
            self.probe.all_at(probe_module.BASELINE), timeout=240)
        low_down = self.status()["lastOperation"]
        gap = (dt.datetime.fromisoformat(low_down["started"].replace("Z", "+00:00")) -
               dt.datetime.fromisoformat(medium_down["completedAt"].replace("Z", "+00:00"))).total_seconds()
        if gap < 60:
            raise RuntimeError("next downward step borrowed the earlier interval")
        self.cases.append({"name": "partial down restart and per-step interval", "passed": True,
                           "nextStepStartGapSeconds": gap})

    def paired_and_blocked(self):
        self.event("paired-requests-and-CPU-limits")
        policy = self.base()
        policy["profiles"]["HIGH"]["requests"]["memory"] = "512Mi"
        policy["profiles"]["HIGH"]["limits"]["cpu"] = "1500m"
        self.install(policy, "paired-inplace")
        self.burst(500)
        self.probe.wait("all declared paired fields effective", lambda: self.completed("up") and
            self.probe.all_at(policy["profiles"]["HIGH"]) and self.status().get("operation") is None, timeout=210)
        up = self.status()["lastOperation"]
        revision = up["revision"]
        self.hold(35, lambda: self.status().get("operation") is None and self.status()["revision"] == revision,
                  "completed profile spawned redundant no-op operations")
        self.save("paired-up-operation.json", up)
        self.drain()
        self.probe.wait("paired tuple safely returns to baseline", lambda: self.completed("down") and
            self.probe.all_at(probe_module.BASELINE), timeout=240)
        self.cases.append({"name": "paired CPU/memory requests and CPU limits in place", "passed": True,
                           "withoutMemoryCeilingChange": True, "stableCompletedRevision": revision})
        self.event("strict-memory-limit-block")
        policy["profiles"]["HIGH"]["limits"]["memory"] = "1536Mi"
        self.install(policy, "strict-memory-block")
        baseline = self.current_pods()
        resource_before = self.probe.get("rabbitmqcluster", "rmq-probe")["spec"]["resources"]
        strategy_before = self.probe.get("statefulset", "rmq-probe-server")["spec"]["updateStrategy"]
        self.burst(500)
        self.probe.wait("unsupported whole profile blocked", lambda: self.observe()["reason"] ==
            "MemoryLimitRequiresBrokerBudgetReload", timeout=120)
        self.hold(30, lambda: self.resources_unchanged(baseline) and self.status().get("operation") is None and
            self.probe.get("rabbitmqcluster", "rmq-probe")["spec"]["resources"] == resource_before and
            self.probe.get("statefulset", "rmq-probe-server")["spec"]["updateStrategy"] == strategy_before,
            "strict memory gate changed part of the target or strategy")
        self.drain()
        self.cases.append({"name": "strict memory transition blocks before writes", "passed": True})

    def rolling_availability(self):
        st = self.probe.get("statefulset", "rmq-probe-server")
        pods = self.probe.kubectl("get", "pods", "-n", self.namespace, "-o", "json", as_json=True)["items"]
        owned = [p for p in pods if probe_module.owns(p, "StatefulSet", st["metadata"]["uid"])]
        ready = sum(not p["metadata"].get("deletionTimestamp") and self.probe.status(p).get("ready", False)
                    for p in owned)
        record = {"time": utc(), "expected": 3, "owned": len(owned), "readyNonTerminating": ready}
        with (self.output / "rolling-availability.jsonl").open("a") as file:
            file.write(json.dumps(record) + "\n")
        if ready < 2:
            raise RuntimeError("rolling update left fewer than two ready brokers")
        return ready

    def explicit_rolling(self):
        import concurrent.futures
        self.event("explicit-rolling-full-memory-cycle")
        policy = self.base()
        policy["profiles"]["HIGH"] = {"requests": {"cpu": "800m", "memory": "512Mi"},
                                       "limits": {"cpu": "1500m", "memory": "1536Mi"}}
        policy["updatePolicy"]["allowRollingFallback"] = True
        self.install(policy, "explicit-rolling")
        before = self.current_pods()
        old_scenario, old_load = self.args.scenario, self.args.load_seconds
        self.args.scenario, self.args.load_seconds = "rolling", 180
        try:
            with concurrent.futures.ThreadPoolExecutor(max_workers=1) as executor:
                publishing = executor.submit(self.publish)
                while not publishing.done():
                    self.observe()
                    self.rolling_availability()
                    time.sleep(5)
                publishing.result()
            self.wait_rolling("up", policy["profiles"]["HIGH"], timeout=300)
            up = self.status()["lastOperation"]
            if up["path"] != "rolling":
                raise RuntimeError("memory limit used an unauthorized in-place path")
            self.restart_forwards()
            increased = self.probe.snapshot("rolling-increased", policy["profiles"]["HIGH"])
            if not probe_module.budget_matches(increased):
                raise RuntimeError("rolling did not reload the expected broker budget")
            self.drain()
            self.wait_rolling("down", probe_module.BASELINE, timeout=480)
            down = self.status()["lastOperation"]
            self.restart_forwards()
            decreased = self.probe.snapshot("rolling-decreased", probe_module.BASELINE)
            if not probe_module.budget_matches(decreased):
                raise RuntimeError("rolling decrease left the running budget stale")
            after = self.current_pods()
            if not all(before[name]["metadata"]["uid"] != after[name]["metadata"]["uid"] for name in before):
                raise RuntimeError("rolling memory cycle did not replace every broker")
            self.save("rolling-operations.json", {"up": up, "down": down})
            self.cases.append({"name": "explicit full-memory rolling cycle", "passed": True,
                "brokerReplacementsExpected": True, "minimumReadySampled": 2,
                "upDeadlineMissed": up["deadlineMissed"], "uncertainPublishAttempts": len(self.uncertain_attempts),
                "allConfirmedIDsAccounted": self.confirmed == self.consumed})
        finally:
            self.args.scenario, self.args.load_seconds = old_scenario, old_load

    def wait_rolling(self, direction, resources, timeout):
        # Availability assertions must run even before convergence and must not
        # be swallowed by Probe.wait's retry handling.
        end = time.monotonic() + timeout
        while True:
            self.rolling_availability()
            if self.completed(direction) and self.probe.all_at(resources):
                return
            if time.monotonic() > end:
                raise RuntimeError(f"explicit rolling {direction} did not converge")
            time.sleep(5)

    def scaler_started(self):
        pods = self.probe.kubectl("get", "pods", "-n", self.namespace, "-l", "app=rmq-vertical-scaler",
            "-o", "json", as_json=True)["items"]
        if len(pods) != 1:
            raise RuntimeError("scaler census is not exactly one")
        return next(s["state"]["running"]["startedAt"] for s in pods[0]["status"]["containerStatuses"]
                    if s["name"] == "scaler")

    def run(self):
        self.probe.guard()
        if self.status().get("operation") is not None:
            raise RuntimeError("finish the baseline cycle before starting the recovery matrix")
        self.probe.start_api()
        self.start_amqp()
        self.declare()
        initial = self.current_pods()
        if self.args.extras_only:
            self.paired_and_blocked()
            strict_final = self.current_pods()
            if not all(initial[name]["metadata"]["uid"] == strict_final[name]["metadata"]["uid"] and
                       self.probe.status(initial[name]).get("containerID") == self.probe.status(strict_final[name]).get("containerID") and
                       self.probe.status(initial[name]).get("restartCount") == self.probe.status(strict_final[name]).get("restartCount")
                       for name in initial):
                raise RuntimeError("strict paired/block cases replaced or restarted a broker")
            self.explicit_rolling()
        else:
            self.off_cases()
            self.missing_metrics_and_restart()
            self.partial_and_supersession()
        self.drain()
        if self.confirmed != self.consumed:
            raise RuntimeError("matrix confirmed/consumed IDs differ")
        if not self.args.extras_only and not all(p["metadata"]["uid"] == self.current_pods()[name]["metadata"]["uid"] and
            self.probe.status(p).get("containerID") == self.probe.status(self.current_pods()[name]).get("containerID")
            for name, p in initial.items()):
            raise RuntimeError("strict recovery matrix restarted or replaced a broker")
        result = {"time": utc(), "cases": self.cases, "confirmed": len(self.confirmed),
            "consumedUnique": len(self.consumed), "duplicates": self.duplicates,
            "strictCasesWithoutBrokerReplacement": True,
            "includesExplicitRollingReplacements": self.args.extras_only, "allConfirmedIDsAccounted": True,
            "fullAcceptanceMatrix": False, "remaining": "explicit rolling, rate/fanout scope, infeasible/QoS alternatives and full failure ordering"}
        self.save("matrix-result.json", result)
        print(json.dumps(result, indent=2), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kubeconfig", required=True)
    parser.add_argument("--context", required=True)
    parser.add_argument("--image", default="rmq-vertical-scaler:v2.3-test")
    parser.add_argument("--output", required=True)
    parser.add_argument("--extras-only", action="store_true")
    args = parser.parse_args()
    args.scenario = "native"
    args.load_seconds = 60
    args.down_seconds = 60
    args.cooldown_seconds = 600
    args.settle_seconds = 60
    args.reuse = True
    matrix = Matrix(args)
    try:
        matrix.run()
        return 0
    except Exception as error:
        matrix.save("matrix-error.json", {"time": utc(), "error": str(error)})
        print(f"FAILED MATRIX: {error}", flush=True)
        return 1
    finally:
        try:
            matrix.deny_one(False)
        finally:
            matrix.close()


if __name__ == "__main__":
    raise SystemExit(main())
