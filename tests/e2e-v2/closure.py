#!/usr/bin/env python3
"""Bounded coverage closure on an explicit disposable loopback kind cluster."""
import argparse
import copy
import datetime as dt
import hashlib
import json
from pathlib import Path
import subprocess
import time
from urllib.parse import urlparse

from matrix import Matrix
from run import ROOT, probe_module, utc

BASE = copy.deepcopy(probe_module.BASELINE)


def configuration_value(data, name):
    values = [line.partition("=")[2].strip() for text in data.values() for line in text.splitlines()
              if "=" in line and line.partition("=")[0].strip() == name]
    return values[0] if len(values) == 1 else None


def verified_image(pods, expected):
    live = [p for p in pods if not p["metadata"].get("deletionTimestamp")]
    if len(live) > 1:
        raise AssertionError("overlapping live scaler replicas")
    if len(live) != 1:
        return None
    status = live[0].get("status", {}).get("containerStatuses", [])
    if not status or not status[0].get("ready"):
        return None
    if status[0]["imageID"].rsplit("@", 1)[-1] != expected:
        raise AssertionError("running scaler image differs from pinned build")
    return {"podUID": live[0]["metadata"]["uid"], "image": live[0]["spec"]["containers"][0]["image"], "imageID": status[0]["imageID"]}


class Closure(Matrix):
    def __init__(self, args):
        output = Path(args.output)
        if output.exists() and any(output.iterdir()):
            raise FileExistsError("use a fresh output directory; retained evidence must not be overwritten")
        super().__init__(args)
        (self.output / "closure.py").write_bytes(Path(__file__).read_bytes())
        self.init_names = ["setup-container"]
        self.active_denials = set()

    def record(self, case, **facts):
        result = {"case": case, "passed": True, "time": utc(), "image": self.args.image, "expectedRuntimeDigest": self.args.expected_image_id, **facts}
        self.cases.append(result)
        self.save("closure-result.json", {"cases": self.cases, "fullAcceptanceMatrix": False})
        print("PASSED " + json.dumps(result), flush=True)

    def exists(self, kind, name, cluster=False):
        try:
            if cluster:
                return self.probe.kubectl("get", kind, name, "-o", "json", as_json=True)
            return self.probe.get(kind, name)
        except RuntimeError as error:
            if "NotFound" in str(error):
                return None
            raise

    def apply(self, resource, filename):
        path = self.output / filename
        path.write_text(json.dumps(resource, indent=2) + "\n")
        self.probe.kubectl("apply", "-f", str(path))

    def guard_disposable(self):
        config = self.probe.kubectl("config", "view", "--minify", "-o", "json", as_json=True)
        if self.args.context != "kind-rmq-v23-closure" or urlparse(config["clusters"][0]["cluster"]["server"]).hostname not in ("127.0.0.1", "localhost", "::1"):
            raise RuntimeError("closure requires the dedicated loopback kind-rmq-v23-closure context")
        ns = self.exists("namespace", self.namespace, cluster=True)
        if ns and ns["metadata"].get("labels", {}).get("rmq-v23-closure") != self.namespace:
            raise RuntimeError("existing namespace is not owned by this closure harness")

    def fresh(self, variant="normal"):
        self.close()
        self.forward = self.probe.forward = None
        ns = self.exists("namespace", self.namespace, cluster=True)
        if ns:
            if ns["metadata"].get("labels", {}).get("rmq-vertical-scaler-test") != "v23-feasibility":
                raise RuntimeError("refusing to replace an unrelated namespace")
            st = self.exists("statefulset", "rmq-probe-server")
            if st:
                self.init_names = [c["name"] for c in st["spec"]["template"]["spec"].get("initContainers", [])]
            self.probe.kubectl("delete", "deployment", "rmq-vertical-scaler", "-n", self.namespace, "--ignore-not-found", "--wait=false")
            self.probe.kubectl("delete", "rabbitmqcluster", "rmq-probe", "-n", self.namespace, "--ignore-not-found", "--wait=false")
            # Close namespace admission before waiting: the pinned Operator's
            # finalizer uses CreateOrUpdate and can recreate a default CR after
            # deleting the original. Normal namespace/CR finalizers still run.
            self.probe.kubectl("delete", "namespace", self.namespace, "--wait=false")
            self.probe.wait("old owned fixture removed", lambda: self.exists("namespace", self.namespace, cluster=True) is None,
                            timeout=600)
        low = copy.deepcopy(BASE)
        if variant == "guaranteed":
            low = {"requests": {"cpu": "100m", "memory": "1Gi"}, "limits": {"cpu": "100m", "memory": "1Gi"}}
        probe_module.BASELINE = low
        namespace = {"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": self.namespace,
            "labels": {"rmq-vertical-scaler-test": "v23-feasibility", "rmq-v23-closure": self.namespace}}}
        spec = {"replicas": 3, "image": "rabbitmq:4.4.0-management", "resources": low,
            "rabbitmq": {"additionalConfig": "disk_free_limit.absolute = 128MiB\ndefault_queue_type = quorum\nvm_memory_high_watermark.relative = 0.6\n"},
            "persistence": {"storageClassName": "standard", "storage": "1Gi"}, "service": {"type": "ClusterIP"}}
        delay = getattr(self.args, "startup_delay_seconds", None)
        if delay is not None:
            spec["delayStartSeconds"] = delay
        if variant == "guaranteed":
            spec["override"] = {"statefulSet": {"spec": {"template": {"spec": {
                "containers": [{"name": "rabbitmq", "resources": low}],
                "initContainers": [{"name": name, "resources": low} for name in self.init_names]}}}}}
        if variant == "restart":
            spec["override"] = {"statefulSet": {"spec": {"template": {"spec": {
                "containers": [{"name": "rabbitmq", "resizePolicy": [{"resourceName": "cpu", "restartPolicy": "RestartContainer"}]}]}}}}}
        period = getattr(self.args, "readiness_period_seconds", None)
        if period is not None:
            containers = spec.setdefault("override", {}).setdefault("statefulSet", {}).setdefault("spec", {}).setdefault("template", {}).setdefault("spec", {}).setdefault("containers", [{"name": "rabbitmq"}])
            next(c for c in containers if c["name"] == "rabbitmq")["readinessProbe"] = {
                "tcpSocket": {"port": 5672}, "initialDelaySeconds": 1, "periodSeconds": period,
                "timeoutSeconds": 5, "successThreshold": 1, "failureThreshold": 3}
        cluster = {"apiVersion": "rabbitmq.com/v1beta1", "kind": "RabbitmqCluster",
                   "metadata": {"name": "rmq-probe", "namespace": self.namespace}, "spec": spec}
        self.apply({"apiVersion": "v1", "kind": "List", "items": [namespace, cluster]}, f"fixture-{variant}.json")
        self.probe.wait(f"three {variant} brokers ready with effective tuples", lambda: self.probe.all_at(low), timeout=600)
        self.probe.start_api()
        self.start_amqp()
        self.confirmed, self.consumed, self.duplicates = set(), set(), 0
        self.declare()
        self.probe.platform()
        self.probe.snapshot(f"baseline-{variant}", low)
        self.save(f"baseline-{variant}-pods.json", self.current_pods())

    def base(self):
        policy = super().base()
        policy["profiles"]["LOW"] = copy.deepcopy(probe_module.BASELINE)
        policy["profiles"]["HIGH"] = copy.deepcopy(probe_module.BASELINE)
        policy["profiles"]["HIGH"]["requests"]["cpu"] = "800m"
        policy["profiles"]["HIGH"]["limits"]["cpu"] = "1"
        return policy

    def install(self, policy, name):
        result = super().install(policy, name)
        def image_ready():
            pods = self.probe.kubectl("get", "pods", "-n", self.namespace, "-l", "app=rmq-vertical-scaler",
                                      "-o", "json", as_json=True)["items"]
            record = verified_image(pods, self.args.expected_image_id)
            if record is None:
                return False
            self.save("running-image.json", record)
            return True
        self.probe.wait("pinned scaler image verified", image_ready, timeout=120)
        return result

    def broker_facts(self):
        return {name: {"uid": p["metadata"]["uid"], "containerID": self.probe.status(p).get("containerID"),
            "restarts": self.probe.status(p).get("restartCount"), "desired": self.probe.container(p)["resources"],
            "effective": self.probe.status(p).get("resources"), "qos": p["status"].get("qosClass"),
            "resizePolicy": self.probe.container(p).get("resizePolicy"), "conditions": p["status"].get("conditions")}
            for name, p in self.current_pods().items()}

    def partial(self, target, target_cpu, lower_cpu):
        operation = self.observe().get("operation") or {}
        if operation.get("target") != target or "v23 closure deliberate" not in operation.get("brokers", {}).get("rmq-probe-server-1", {}).get("error", ""):
            return False
        values = {name: self.probe.status(p).get("resources", {}).get("requests", {}).get("cpu", "0")
                  for name, p in self.current_pods().items()}
        return all(probe_module.quantity(values.get(name, "0")) == probe_module.quantity(expected)
                   for name, expected in (("rmq-probe-server-0", target_cpu), ("rmq-probe-server-2", target_cpu),
                                          ("rmq-probe-server-1", lower_cpu)))

    def plane(self):
        return {"resources": self.probe.get("rabbitmqcluster", "rmq-probe")["spec"]["resources"],
            "override": self.probe.get("rabbitmqcluster", "rmq-probe")["spec"].get("override"),
            "strategy": self.probe.get("statefulset", "rmq-probe-server")["spec"]["updateStrategy"],
            "brokers": self.broker_facts()}

    def strict_block(self, variant, reason, case):
        self.fresh(variant)
        if variant == "guaranteed" and not all(p["status"].get("qosClass") == "Guaranteed" for p in self.current_pods().values()):
            raise RuntimeError("fixture is not actually Guaranteed")
        policy = self.base()
        self.install(policy, case)
        before = self.plane()
        self.burst(50)
        self.probe.wait(f"{case} rejected before mutation", lambda: self.observe().get("reason") == reason, timeout=150)
        self.hold(30, lambda: self.plane() == before and self.status().get("operation") is None,
                  "strict block altered strategy, resources, identity or restart state")
        self.save(case + "-state.json", self.status())
        self.save(case + "-before.json", before)
        self.drain()
        if self.confirmed != self.consumed:
            raise RuntimeError("blocked-case confirmed IDs not accounted")
        self.save(case + "-message-ids.json", {"confirmed": sorted(self.confirmed), "consumed": sorted(self.consumed), "duplicates": self.duplicates})
        self.record(case, reason=reason, zeroMutation=True, confirmed=len(self.confirmed), duplicates=self.duplicates)

    def infeasible(self):
        self.fresh()
        policy = self.base()
        nodes = self.probe.kubectl("get", "nodes", "-o", "json", as_json=True)["items"]
        target = str(int(max(probe_module.quantity(n["status"]["allocatable"]["cpu"]) for n in nodes)) + 1)
        policy["profiles"]["HIGH"]["requests"]["cpu"] = target
        policy["profiles"]["HIGH"]["limits"]["cpu"] = target
        self.save("P-INFEASIBLE-node-capacity.json", nodes)
        self.install(policy, "P-INFEASIBLE")
        before = self.broker_facts()
        self.burst(50)
        self.probe.wait("actual kubelet reports infeasible resize", lambda: any(c.get("reason") == "Infeasible" and c.get("status") == "True"
            for p in self.current_pods().values() for c in p["status"].get("conditions", [])), timeout=210)
        self.probe.wait("controller reports blocked Infeasible outcomes", lambda: self.observe().get("reason") == "ResizeInfeasibleTargetRetained" and
            self.status().get("phase") == "Blocked" and all(p["outcome"] == "Infeasible" for p in self.status().get("operation", {}).get("brokers", {}).values()), timeout=90)
        operation = self.status()["operation"]
        expected_attempts = {name: p["attempts"] for name, p in operation["brokers"].items()}
        target_resources = policy["profiles"]["HIGH"]
        def retained():
            current = self.status().get("operation") or {}
            st = self.probe.get("statefulset", "rmq-probe-server")
            return (current.get("revision") == operation["revision"] and current.get("path") == "inplace" and
                probe_module.equal_resources(current.get("resources", {}), target_resources) and
                {name: p["attempts"] for name, p in current.get("brokers", {}).items()} == expected_attempts and
                probe_module.equal_resources(self.probe.get("rabbitmqcluster", "rmq-probe")["spec"]["resources"], target_resources) and
                probe_module.equal_resources(self.probe.container(st["spec"]["template"])["resources"], target_resources) and
                st["spec"]["updateStrategy"]["type"] == "OnDelete" and
                all(probe_module.equal_resources(self.probe.container(p)["resources"], target_resources) for p in self.current_pods().values()))
        self.hold(30, retained, "infeasible target drifted, repeated submissions or silently rolled")
        after = self.broker_facts()
        if any((before[n]["uid"], before[n]["containerID"], before[n]["restarts"], before[n]["effective"]) !=
               (after[n]["uid"], after[n]["containerID"], after[n]["restarts"], after[n]["effective"]) for n in before):
            raise RuntimeError("infeasible target changed effective resources or disrupted a broker")
        self.save("P-INFEASIBLE-state.json", self.status())
        self.save("P-INFEASIBLE-brokers.json", after)
        self.drain()
        if self.confirmed != self.consumed:
            raise RuntimeError("infeasible-case messages not accounted")
        self.save("P-INFEASIBLE-message-ids.json", {"confirmed": sorted(self.confirmed), "consumed": sorted(self.consumed), "duplicates": self.duplicates})
        self.record("P-INFEASIBLE", targetCPU=target, actualKubeletReason="Infeasible", targetRetained=True,
                    noImplicitRolling=True, confirmed=len(self.confirmed))

    def run_platform(self):
        self.guard_disposable()
        if self.args.platform_case in ("all", "qos"):
            self.strict_block("guaranteed", "QoSClassChange", "P-QOS")
        if self.args.platform_case in ("all", "restart"):
            self.strict_block("restart", "ContainerResizeRequiresRestart", "P-RESTART")
        if self.args.platform_case in ("all", "infeasible"):
            self.infeasible()

    def deny(self, name, kind, condition, enabled=True):
        fullname = "rvs-closure-" + name
        label = {"rmq-vertical-scaler-test": "v23-closure"}
        if not enabled:
            for resource in ("validatingadmissionpolicybinding", "validatingadmissionpolicy"):
                current = self.exists(resource, fullname, cluster=True)
                if current:
                    if current["metadata"].get("labels", {}).get("rmq-vertical-scaler-test") != "v23-closure":
                        raise RuntimeError("refusing unrelated admission-resource cleanup")
                    self.probe.kubectl("delete", resource, fullname)
            self.active_denials.discard((name, kind, condition))
            return
        for resource in ("validatingadmissionpolicybinding", "validatingadmissionpolicy"):
            if self.exists(resource, fullname, cluster=True):
                raise RuntimeError("admission test resource already exists")
        group = "rabbitmq.com" if kind == "rabbitmqclusters" else ""
        account = f"system:serviceaccount:{self.namespace}:rmq-vertical-scaler-sa"
        selector = {"matchLabels": {"rmq-v23-closure": self.namespace}}
        policy = {"apiVersion": "admissionregistration.k8s.io/v1", "kind": "ValidatingAdmissionPolicy",
            "metadata": {"name": fullname, "labels": label}, "spec": {"failurePolicy": "Fail",
            "matchConstraints": {"namespaceSelector": selector, "resourceRules": [{"apiGroups": [group],
                "apiVersions": ["v1beta1" if group else "v1"], "operations": ["CREATE", "UPDATE"], "resources": [kind]}]},
            "validations": [{"expression": f"request.userInfo.username != '{account}' || !({condition})",
                             "message": "v23 closure deliberate " + name + " denial"}]}}
        binding = {"apiVersion": "admissionregistration.k8s.io/v1", "kind": "ValidatingAdmissionPolicyBinding",
            "metadata": {"name": fullname, "labels": label}, "spec": {"policyName": fullname,
            "validationActions": ["Deny"], "matchResources": {"namespaceSelector": selector}}}
        self.active_denials.add((name, kind, condition))
        self.apply({"apiVersion": "v1", "kind": "List", "items": [policy, binding]}, fullname + ".json")

    def mutation_snapshot(self):
        st = self.probe.get("statefulset", "rmq-probe-server")
        return {"cr": self.probe.get("rabbitmqcluster", "rmq-probe")["spec"],
            "strategy": st["spec"]["updateStrategy"],
            "templateResources": self.probe.container(st["spec"]["template"])["resources"],
            "brokers": self.broker_facts()}

    def run_operator(self):
        self.guard_disposable()
        self.fresh()
        policy = self.base()
        self.install(policy, "operator-start")
        before = self.mutation_snapshot()
        baseline_pods = self.current_pods()
        intent = ("intent", "configmaps", 'object.metadata.name == "rmq-vertical-scaler-status"')
        strategy = ("strategy", "rabbitmqclusters", 'has(object.spec.override) && has(object.spec.override.statefulSet) && has(object.spec.override.statefulSet.spec) && has(object.spec.override.statefulSet.spec.updateStrategy) && object.spec.override.statefulSet.spec.updateStrategy.type == "OnDelete"')
        template = ("template", "rabbitmqclusters", 'object.spec.resources.requests.cpu == "800m"')
        pod = ("pod", "pods/resize", 'object.metadata.name == "rmq-probe-server-1"')
        for rule in (intent, strategy, template, pod):
            self.deny(*rule)
        self.burst(100)
        def logs():
            return self.probe.kubectl("logs", "deployment/rmq-vertical-scaler", "-n", self.namespace, "--tail=80")
        self.probe.wait("intent denial actually exercised", lambda: "v23 closure deliberate intent denial" in logs(), timeout=150)
        self.hold(20, lambda: self.mutation_snapshot() == before, "broker mutation preceded durable intent")
        self.save("O-INTENT-logs.json", logs())
        self.save("O-INTENT-before.json", before)
        self.record("O-INTENT", zeroMutation=True)
        self.deny(*intent, enabled=False)
        self.probe.wait("strategy denial retains Prepare operation", lambda: "v23 closure deliberate strategy denial" in self.observe().get("reason", ""), timeout=120)
        state = self.status()
        revision = state["operation"]["revision"]
        self.save("O-STRATEGY-state.json", state)
        self.hold(20, lambda: self.mutation_snapshot() == before and self.status()["operation"]["revision"] == revision,
                  "strategy failure allowed resources or lost intent")
        self.record("O-STRATEGY", retainedRevision=revision, noResourceWrites=True)
        self.deny(*strategy, enabled=False)
        self.probe.wait("template denial after actual OnDelete", lambda: "v23 closure deliberate template denial" in self.observe().get("reason", "") and
            self.probe.get("statefulset", "rmq-probe-server")["spec"]["updateStrategy"]["type"] == "OnDelete", timeout=120)
        self.save("O-TEMPLATE-state.json", self.status())
        self.hold(20, lambda: self.resources_unchanged(baseline_pods) and
            probe_module.equal_resources(self.probe.get("rabbitmqcluster", "rmq-probe")["spec"]["resources"], BASE) and
            self.status()["operation"]["revision"] == revision, "resource denial changed target or submitted Pods")
        self.record("O-TEMPLATE", retainedRevision=revision, observedOnDelete=True)
        self.restart_scaler("interrupted-template-write")
        self.deny(*template, enabled=False)
        self.probe.wait("concurrent partial submission after restart", lambda: self.partial("HIGH", "800m", "100m"), timeout=210)
        partial = self.status()["operation"]
        if partial["revision"] != revision:
            raise RuntimeError("restart replaced the retained operation")
        self.save("O-PARTIAL-state.json", self.status())
        self.deny(*pod, enabled=False)
        self.probe.wait("same operation finishes once denial removed", lambda: self.completed("up") and self.probe.all_at(policy["profiles"]["HIGH"]), timeout=150)
        done = self.status()["lastOperation"]
        if done["revision"] != revision or any(done["brokers"][n]["attempts"] != partial["brokers"][n]["attempts"] for n in ("rmq-probe-server-0", "rmq-probe-server-2")):
            raise RuntimeError("successful brokers were resubmitted or revision replaced")
        for name, old in baseline_pods.items():
            current = self.current_pods()[name]
            if (old["metadata"]["uid"], self.probe.status(old).get("containerID"), self.probe.status(old).get("restartCount")) != (current["metadata"]["uid"], self.probe.status(current).get("containerID"), self.probe.status(current).get("restartCount")):
                raise RuntimeError("strict interruption sequence unexpectedly replaced or restarted a broker")
        self.record("O-PARTIAL", retainedRevision=revision, successfulBrokersNotResubmitted=True)
        self.drain()
        self.probe.wait("baseline after interruption sequence", lambda: self.completed("down") and self.probe.all_at(BASE), timeout=240)
        self.external_and_replacement(policy)

    def external_and_replacement(self, policy):
        pod = ("replacement", "pods/resize", 'object.metadata.name == "rmq-probe-server-1"')
        self.deny(*pod)
        self.burst(100)
        self.probe.wait("pending operation before broker replacement", lambda: self.partial("HIGH", "800m", "100m"), timeout=180)
        state = self.status()
        all_before = self.broker_facts()
        old = self.current_pods()["rmq-probe-server-1"]
        self.save("O-REPLACE-before.json", state)
        config_before = self.probe.get("configmap", "rmq-probe-server-conf")["data"]
        annotations_before = self.probe.get("statefulset", "rmq-probe-server")["spec"]["template"]["metadata"].get("annotations", {})
        original_config = self.probe.get("rabbitmqcluster", "rmq-probe")["spec"]["rabbitmq"]["additionalConfig"]
        changed_config = original_config + "channel_max = 1024\n"
        checksum_key = "rmq-v23-closure/config-sha256"
        checksum = hashlib.sha256(changed_config.encode()).hexdigest()
        # This is an explicit fixture checksum override. The pinned Operator
        # does not emit a native checksum annotation on the baseline template.
        self.probe.kubectl("patch", "rabbitmqcluster", "rmq-probe", "-n", self.namespace, "--type=merge", "-p",
                           json.dumps({"spec": {"rabbitmq": {"additionalConfig": changed_config},
                               "override": {"statefulSet": {"spec": {"template": {"metadata": {"annotations": {checksum_key: checksum}}}}}}}}))
        self.probe.wait("Operator generated configuration and template checksum update", lambda:
            configuration_value(self.probe.get("configmap", "rmq-probe-server-conf")["data"], "channel_max") == "1024" and
            self.probe.get("statefulset", "rmq-probe-server")["spec"]["template"]["metadata"].get("annotations", {}).get(checksum_key) == checksum,
            timeout=120)
        self.save("O-REPLACE-generated-config.json", {"before": config_before,
            "after": self.probe.get("configmap", "rmq-probe-server-conf")["data"], "annotationsBefore": annotations_before,
            "annotationsAfter": self.probe.get("statefulset", "rmq-probe-server")["spec"]["template"]["metadata"].get("annotations", {}),
            "checksumSource": "explicit test override, not an Operator-native generated checksum"})
        after_config = self.broker_facts()
        if any(after_config[name][key] != all_before[name][key] for name in all_before for key in ("uid", "containerID", "restarts")):
            raise RuntimeError("config propagation replaced or restarted a broker before deliberate deletion")
        self.probe.kubectl("delete", "pod", "rmq-probe-server-1", "-n", self.namespace, "--wait=false")
        def replacement_ready():
            try:
                new = self.probe.get("pod", "rmq-probe-server-1")
            except RuntimeError as error:
                if "NotFound" in str(error):
                    return False
                raise
            return new["metadata"]["uid"] != old["metadata"]["uid"] and self.probe.status(new).get("ready", False)
        self.probe.wait("owned replacement broker ready", replacement_ready, timeout=300)
        self.probe.wait("retained operation confirms new broker identity", lambda: self.completed("up") and self.probe.all_at(policy["profiles"]["HIGH"]), timeout=180)
        done = self.status()["lastOperation"]
        replacement = self.current_pods()["rmq-probe-server-1"]
        if done["revision"] != state["operation"]["revision"] or done["brokers"]["rmq-probe-server-1"]["uid"] != replacement["metadata"]["uid"] or replacement["metadata"]["uid"] == old["metadata"]["uid"]:
            raise RuntimeError("old broker identity completed replacement")
        after = self.broker_facts()
        for name in ("rmq-probe-server-0", "rmq-probe-server-2"):
            if any(after[name][key] != all_before[name][key] for key in ("uid", "containerID", "restarts")):
                raise RuntimeError("replacement case disrupted an unrelated broker")
        self.save("O-REPLACE-after.json", self.status())
        self.record("O-REPLACE", retainedRevision=done["revision"], newUID=done["brokers"]["rmq-probe-server-1"]["uid"], generatedConfigAndExplicitChecksumOverrideChanged=True)
        self.deny(*pod, enabled=False)
        self.restart_forwards()
        self.drain()
        self.probe.wait("baseline before external-writer case", lambda: self.completed("down") and self.probe.all_at(BASE), timeout=240)
        self.deny(*pod)
        self.burst(100)
        self.probe.wait("pending operation before external edit", lambda: self.partial("HIGH", "800m", "100m"), timeout=180)
        retained = self.status()["operation"]
        old_revision = retained["revision"]
        before_edit = self.broker_facts()
        attempts = {name: p["attempts"] for name, p in retained["brokers"].items()}
        external = copy.deepcopy(policy["profiles"]["HIGH"])
        external["requests"]["cpu"] = "700m"
        self.probe.kubectl("patch", "rabbitmqcluster", "rmq-probe", "-n", self.namespace, "--type=merge", "-p", json.dumps({"spec": {"resources": external}}))
        self.probe.wait("external tuple causes explicit conflict", lambda: self.observe().get("reason") == "ExternalResourceChange", timeout=90)
        self.hold(30, lambda: self.status()["operation"]["revision"] == old_revision and
            probe_module.equal_resources(self.status()["operation"]["resources"], retained["resources"]) and
            {name: p["attempts"] for name, p in self.status()["operation"]["brokers"].items()} == attempts and
            all(self.broker_facts()[name][key] == before_edit[name][key] for name in before_edit for key in ("uid", "containerID", "restarts", "desired", "effective")) and
            probe_module.equal_resources(self.probe.get("rabbitmqcluster", "rmq-probe")["spec"]["resources"], external),
            "controller overwrote external resources or adopted their tuple")
        self.save("O-DRIFT-state.json", self.status())
        self.record("O-DRIFT", retainedRevision=old_revision, externalTupleNotOverwritten=True)
        self.deny(*pod, enabled=False)
        self.drain()
        if self.confirmed != self.consumed:
            raise RuntimeError("operator-case confirmed messages not accounted")
        self.save("operator-message-accounting.json", {"confirmed": sorted(self.confirmed), "consumed": sorted(self.consumed), "duplicates": self.duplicates})

    def run_metrics(self):
        from scope import Scope
        from urllib.parse import quote
        import base64
        self.guard_disposable()
        self.fresh()
        api = lambda method, path, body=None: Scope.api(self, method, path, body)
        names = [f"closure-page-{i:03d}" for i in range(501)]
        with self.connection() as connection:
            channel = connection.channel()
            for name in names:
                channel.queue_declare(name, durable=True, arguments={"x-queue-type": "classic"})
        vhost = "/closure-pagination"
        api("PUT", "/api/vhosts/" + quote(vhost, safe=""), {})
        admin = base64.b64decode(self.probe.get("secret", "rmq-probe-default-user")["data"]["username"]).decode()
        api("PUT", "/api/permissions/" + quote(vhost, safe="") + "/" + quote(admin, safe=""),
            {"configure": ".*", "write": ".*", "read": ".*"})
        expected = {}
        for host, queue, count in (("/", names[0], 10), ("/", names[-1], 20), (vhost, names[0], 7)):
            ids = []
            with Scope.connection_vhost(self, host) as connection:
                channel = connection.channel()
                channel.queue_declare(queue, durable=True, arguments={"x-queue-type": "classic"})
                channel.confirm_delivery()
                for number in range(count):
                    message_id = f"{self.run_id}-paging-{host}-{queue}-{number}"
                    channel.basic_publish("", queue, message_id.encode(), mandatory=True,
                        properties=__import__('pika').BasicProperties(message_id=message_id, delivery_mode=2))
                    ids.append(message_id)
            expected[host + "|" + queue] = ids
        policy = self.base()
        policy["updatePolicy"]["mode"] = "Off"
        policy["triggers"] = [{"type": "rabbitmq", "metadata": {"mode": "QueueLength", "aggregation": aggregation,
            "selector": {"queueNameRegex": "^closure-page-"}, "thresholds": {"HIGH": 10}}} for aggregation in ("sum", "max")]
        self.install(policy, "M-PAGING")
        initial = self.current_pods()
        self.probe.wait("actual controller traverses pages and counts vhosts", lambda:
            all(s["complete"] for s in self.observe()["observation"]["signals"]) and
            [s["value"] for s in self.status()["observation"]["signals"]] == [37, 20], timeout=150)
        self.save("M-PAGING-state.json", self.status())
        pages = [api("GET", f"/api/queues?pagination=true&page_size=500&page={i}&columns=name,vhost,messages") for i in (1, 2)]
        all_queues = [q for page in pages for q in page["items"]]
        selected = [q for q in all_queues if q["name"].startswith("closure-page-")]
        keys = {(q["vhost"], q["name"]) for q in selected}
        if len(keys) != 502 or len(selected) != 502 or sum(q["messages"] for q in selected) != 37 or pages[0]["page_count"] < 2:
            raise RuntimeError("independent real API pagination/count proof differs")
        self.save("M-PAGING-api-pages.json", pages)
        consumed = {}
        for key, ids in expected.items():
            host, queue = key.split("|", 1)
            actual = []
            with Scope.connection_vhost(self, host) as connection:
                channel = connection.channel()
                while True:
                    method, properties, body = channel.basic_get(queue, auto_ack=False)
                    if method is None:
                        break
                    actual.append(properties.message_id)
                    if body.decode() != properties.message_id:
                        raise RuntimeError("paging message ID/body mismatch")
                    channel.basic_ack(method.delivery_tag)
            if set(actual) != set(ids) or len(actual) != len(ids):
                raise RuntimeError("paging confirmed/consumed accounting differs")
            consumed[key] = actual
        self.save("M-PAGING-message-ids.json", {"confirmed": expected, "consumed": consumed,
            "boundary": "Durable classic queues exercise pagination and ID accounting; this is not quorum replication proof."})
        if not self.resources_unchanged(initial):
            raise RuntimeError("Off metrics case changed broker resources")
        self.record("M-PAGING", realAPIPages=pages[0]["page_count"], selectedQueues=502, sum=37, maximum=20,
                    confirmedMessages=37, brokerResourcesUnchanged=True)
        api("DELETE", "/api/vhosts/" + quote(vhost, safe=""))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kubeconfig", required=True)
    parser.add_argument("--context", required=True)
    parser.add_argument("--image", required=True)
    parser.add_argument("--expected-image-id", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--group", choices=("platform", "operator", "metrics"), required=True)
    parser.add_argument("--platform-case", choices=("all", "qos", "restart", "infeasible"), default="all")
    args = parser.parse_args()
    args.scenario, args.load_seconds, args.down_seconds = "native", 60, 60
    args.cooldown_seconds, args.settle_seconds, args.reuse = 600, 60, True
    run = Closure(args)
    succeeded = False
    try:
        getattr(run, "run_" + args.group)()
        succeeded = True
    except Exception as error:
        run.save("closure-error.json", {"time": utc(), "error": str(error), "group": args.group})
        print("FAILED CLOSURE: " + str(error), flush=True)
    finally:
        cleanup_errors = []
        try:
            for rule in list(run.active_denials):
                try:
                    run.deny(*rule, enabled=False)
                except Exception as error:
                    cleanup_errors.append(str(error))
        finally:
            run.close()
        if cleanup_errors:
            run.save("cleanup-errors.json", cleanup_errors)
            raise RuntimeError("test admission cleanup incomplete; inspect cleanup-errors.json")
    if succeeded:
        run.save("closure-complete.json", {"passed": True, "group": args.group, "time": utc(),
            "expectedRuntimeDigest": args.expected_image_id, "cases": [c["case"] for c in run.cases],
            "workloadAccountingAndCleanupCompleted": True})
    return 0 if succeeded else 1


if __name__ == "__main__":
    raise SystemExit(main())
