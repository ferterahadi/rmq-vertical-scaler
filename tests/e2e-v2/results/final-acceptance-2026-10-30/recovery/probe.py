#!/usr/bin/env python3
"""Probe real resize/broker-budget behavior; never claim scaler validation.

Only an explicit loopback kind context is accepted. Broker memory diagnostics
use the RabbitMQ management API. No broker exec, Erlang evaluation, or broker
configuration update is used. This test does not install the scaler.
"""

import argparse
import base64
import concurrent.futures
import datetime as dt
from decimal import Decimal
import json
from pathlib import Path
import re
import subprocess
import socket
import time
from urllib.parse import urlparse
from urllib.request import Request, build_opener, ProxyHandler
from urllib.error import URLError


NAMESPACE = "rmq-v23-feasibility"
CLUSTER = "rmq-probe"
BASELINE = {"requests": {"cpu": "100m", "memory": "256Mi"},
            "limits": {"cpu": "1", "memory": "1Gi"}}
LARGER = {"requests": {"cpu": "200m", "memory": "512Mi"},
          "limits": {"cpu": "1500m", "memory": "1536Mi"}}
SMALLER = {"requests": {"cpu": "100m", "memory": "256Mi"},
           "limits": {"cpu": "1", "memory": "384Mi"}}
BROKER_NAMES = {f"{CLUSTER}-server-{index}" for index in range(3)}
MEMORY_CONFIG = "userDefinedConfiguration.conf"
WATERMARK_RATIO = Decimal("0.6")


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def quantity(value):
    match = re.fullmatch(r"([0-9]+(?:\.[0-9]+)?)(n|u|m|Ki|Mi|Gi|Ti)?", str(value))
    if not match:
        raise ValueError(f"unsupported test quantity: {value!r}")
    factor = {None: Decimal(1), "n": Decimal("0.000000001"),
              "u": Decimal("0.000001"), "m": Decimal("0.001"),
              "Ki": Decimal(1024), "Mi": Decimal(1024 ** 2),
              "Gi": Decimal(1024 ** 3), "Ti": Decimal(1024 ** 4)}[match[2]]
    return Decimal(match[1]) * factor


def equal_resources(actual, expected):
    return all(key in actual.get(group, {}) and
               quantity(actual[group][key]) == quantity(value)
               for group, values in expected.items() for key, value in values.items())


def memory_override(config):
    match = re.search(r"^total_memory_available_override_value\s*=\s*(\d+)", config, re.M)
    if not match:
        raise RuntimeError("Operator memory override not found")
    return int(match[1])


def owns(resource, kind, uid):
    return any(owner.get("controller") and owner.get("kind") == kind and owner.get("uid") == uid
               for owner in resource["metadata"].get("ownerReferences", []))


def ready_at(pod, resources):
    status = Probe.status(pod)
    return (status.get("ready") is True and
            equal_resources(status.get("resources", {}), resources) and
            equal_resources(Probe.container(pod).get("resources", {}), resources) and
            pod.get("status", {}).get("observedGeneration", 0) >= pod["metadata"].get("generation", 1) and
            not pod["metadata"].get("deletionTimestamp") and
            not any(c["type"] in ("PodResizePending", "PodResizeInProgress") and c["status"] == "True"
                    for c in pod.get("status", {}).get("conditions", [])))


def broker_pairs(first, second):
    def census(snapshot):
        brokers = snapshot["brokers"]
        if len(brokers) != 3 or {b["name"] for b in brokers} != BROKER_NAMES:
            raise RuntimeError("snapshot broker census is incomplete or unexpected")
        return {b["name"]: b for b in brokers}
    a, b = census(first), census(second)
    return [(a[name], b[name]) for name in sorted(BROKER_NAMES)]


def expected_override(resources):
    limit = int(quantity(resources["limits"]["memory"]))
    return limit - min(limit // 5, 2 * 1024 ** 3)


def budget_matches(snapshot):
    """Exact expectation for the pinned 64-bit fixture's explicit relative 0.6."""
    override = snapshot["expectedMemoryOverride"]
    expected = int(Decimal(override) * WATERMARK_RATIO)
    return (snapshot["operatorMemoryOverride"] == override and
            len(snapshot["brokers"]) == 3 and
            {b["name"] for b in snapshot["brokers"]} == BROKER_NAMES and
            all(type(b.get("watermarkBytes")) is int and b["watermarkBytes"] == expected
                for b in snapshot["brokers"]))


class Probe:
    def __init__(self, args):
        self.args = args
        self.forward = None
        self.api_port = None
        self.authorization = None
        self.command = ["kubectl", "--kubeconfig", str(Path(args.kubeconfig).resolve()),
                        "--context", args.context, "--request-timeout=20s"]
        self.output = Path(args.output)
        self.output.mkdir(parents=True, exist_ok=True)

    def kubectl(self, *args, as_json=False):
        result = subprocess.run(self.command + list(args), text=True,
                                capture_output=True, timeout=45)
        if result.returncode:
            raise RuntimeError(f"kubectl {args[0]} failed: {result.stderr.strip()}")
        return json.loads(result.stdout) if as_json else result.stdout.strip()

    def get(self, kind, name):
        return self.kubectl("get", kind, name, "-n", NAMESPACE, "-o", "json", as_json=True)

    def wait(self, description, predicate, timeout=180):
        print(f"Waiting: {description}", flush=True)
        end = time.monotonic() + timeout
        last_error = None
        while time.monotonic() < end:
            try:
                if predicate():
                    return
            except RuntimeError as error:
                last_error = error
            time.sleep(5)
        raise RuntimeError(f"timeout: {description}; last error: {last_error}")

    def save(self, name, value):
        (self.output / name).write_text(json.dumps(value, indent=2) + "\n")

    def guard(self):
        config = self.kubectl("config", "view", "--minify", "-o", "json", as_json=True)
        server = config["clusters"][0]["cluster"]["server"]
        if not self.args.context.startswith("kind-") or urlparse(server).hostname not in (
                "127.0.0.1", "localhost", "::1"):
            raise RuntimeError("this probe only supports an explicit loopback kind test cluster")
        ns = subprocess.run(self.command + ["get", "namespace", NAMESPACE, "-o", "json"],
                            text=True, capture_output=True, timeout=30)
        if ns.returncode == 0:
            if not self.args.reuse or json.loads(ns.stdout)["metadata"].get("labels", {}).get(
                    "rmq-vertical-scaler-test") != "v23-feasibility":
                raise RuntimeError("test namespace already exists; inspect before using --reuse")
            if not equal_resources(self.get("rabbitmqcluster", CLUSTER)["spec"]["resources"], BASELINE) or not self.all_at(BASELINE):
                raise RuntimeError("--reuse requires an unchanged, converged baseline; recreate the test namespace")
        elif "NotFound" not in ns.stderr:
            raise RuntimeError(f"could not verify test namespace absence: {ns.stderr.strip()}")

    def pods(self):
        cluster = self.get("rabbitmqcluster", CLUSTER)
        sts = self.get("statefulset", f"{CLUSTER}-server")
        if not owns(sts, "RabbitmqCluster", cluster["metadata"]["uid"]) or sts["spec"]["replicas"] != 3:
            raise RuntimeError("unexpected StatefulSet owner or replica count")
        pods = self.kubectl("get", "pods", "-n", NAMESPACE, "-l",
                            f"app.kubernetes.io/name={CLUSTER}", "-o", "json",
                            as_json=True)["items"]
        if len(pods) != 3 or {p["metadata"]["name"] for p in pods} != BROKER_NAMES:
            raise RuntimeError("broker census is incomplete or unexpected")
        if not all(owns(p, "StatefulSet", sts["metadata"]["uid"]) for p in pods):
            raise RuntimeError("unexpected Pod owner")
        return pods

    @staticmethod
    def container(pod):
        return next(c for c in pod["spec"]["containers"] if c["name"] == "rabbitmq")

    @staticmethod
    def status(pod):
        return next((c for c in pod.get("status", {}).get("containerStatuses", [])
                     if c["name"] == "rabbitmq"), {})

    def all_at(self, resources):
        return all(ready_at(pod, resources) for pod in self.pods())

    def override(self):
        return memory_override(self.get("configmap", f"{CLUSTER}-server-conf")["data"][MEMORY_CONFIG])

    def start_api(self):
        # Keep credentials in memory; never log or retain the Secret contents.
        data = self.get("secret", f"{CLUSTER}-default-user")["data"]
        credentials = base64.b64decode(data["username"]) + b":" + base64.b64decode(data["password"])
        self.authorization = "Basic " + base64.b64encode(credentials).decode()
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            self.api_port = sock.getsockname()[1]
        self.forward = subprocess.Popen(self.command + ["port-forward", "-n", NAMESPACE,
            f"service/{CLUSTER}", f"{self.api_port}:15672", "--address", "127.0.0.1"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.wait("management API through loopback port-forward", lambda: bool(self.nodes()), timeout=60)

    def nodes(self):
        request = Request(f"http://127.0.0.1:{self.api_port}/api/nodes",
                          headers={"Authorization": self.authorization})
        try:
            with build_opener(ProxyHandler({})).open(request, timeout=10) as response:
                result = json.load(response)
        except (URLError, TimeoutError) as error:
            raise RuntimeError("management nodes API unavailable") from error
        expected = {f"rabbit@{name}.{CLUSTER}-nodes.{NAMESPACE}": name for name in BROKER_NAMES}
        if not isinstance(result, list) or len(result) != 3 or {n.get("name") for n in result} != set(expected):
            raise RuntimeError("management node census is incomplete or unexpected")
        if not all(n.get("running") is True and type(n.get("mem_limit")) is int and
                   n["mem_limit"] > 0 for n in result):
            raise RuntimeError("running broker budget is unavailable from management API")
        return {expected[n["name"]]: n for n in result}

    def close(self):
        if self.forward is not None:
            self.forward.terminate()
            try:
                self.forward.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.forward.kill()
                self.forward.wait(timeout=5)

    def snapshot(self, stage, resources):
        result = {"time": now(), "operatorMemoryOverride": self.override(),
                  "expectedMemoryOverride": expected_override(resources),
                  "budgetSource": "RabbitMQ GET /api/nodes mem_limit", "brokers": []}
        nodes = self.nodes()
        for pod in sorted(self.pods(), key=lambda p: p["metadata"]["name"]):
            if not ready_at(pod, resources):
                raise RuntimeError("snapshot resources have not converged")
            name = pod["metadata"]["name"]
            node = nodes[name]
            status = self.status(pod)
            after = self.get("pod", name)
            after_status = self.status(after)
            if (after["metadata"]["uid"] != pod["metadata"]["uid"] or
                    not ready_at(after, resources) or
                    (after_status.get("containerID"), after_status.get("restartCount")) !=
                    (status.get("containerID"), status.get("restartCount"))):
                raise RuntimeError("broker changed during diagnostic capture")
            result["brokers"].append({
                "name": name, "uid": pod["metadata"]["uid"],
                "containerID": status.get("containerID"), "restarts": status.get("restartCount"),
                "ready": status.get("ready"), "desired": self.container(pod)["resources"],
                "effective": status.get("resources"), "watermarkBytes": node["mem_limit"],
                "brokerMemoryBytes": node.get("mem_used"), "memoryAlarm": node.get("mem_alarm"),
                "imageID": status.get("imageID"),
                "generation": pod["metadata"].get("generation"),
                "observedGeneration": pod.get("status", {}).get("observedGeneration"),
                "qosClass": pod["status"].get("qosClass"),
                "resizePolicy": self.container(pod).get("resizePolicy", []),
            })
        self.save(f"{stage}.json", result)
        print(f"Recorded {stage}: Operator memory override={result['operatorMemoryOverride']}", flush=True)
        return result

    def transition(self, resources, stage):
        self.kubectl("patch", "rabbitmqcluster", CLUSTER, "-n", NAMESPACE, "--type=merge", "-p",
                     json.dumps({"spec": {"resources": resources}}))
        self.wait(f"{stage} Operator template", lambda: equal_resources(next(
            c for c in self.get("statefulset", f"{CLUSTER}-server")["spec"]["template"]["spec"]["containers"]
            if c["name"] == "rabbitmq")["resources"], resources))

        def submit(pod):
            name, uid = pod["metadata"]["name"], pod["metadata"]["uid"]
            started = now()
            for attempt in range(3):
                pod = self.get("pod", name)
                if pod["metadata"]["uid"] != uid:
                    raise RuntimeError("broker replaced during resize submission")
                meta = pod["metadata"]
                index = next(i for i, c in enumerate(pod["spec"]["containers"]) if c["name"] == "rabbitmq")
                patch = [{"op": "test", "path": "/metadata/uid", "value": uid},
                         {"op": "test", "path": "/metadata/resourceVersion", "value": meta["resourceVersion"]}]
                patch += [{"op": "replace", "path": f"/spec/containers/{index}/resources/{group}/{key}",
                           "value": value} for group, values in resources.items() for key, value in values.items()]
                try:
                    self.kubectl("patch", "pod", name, "-n", NAMESPACE, "--subresource=resize",
                                 "--type=json", "-p", json.dumps(patch))
                    return {"pod": name, "uid": uid, "started": started, "accepted": now()}
                except RuntimeError as error:
                    if attempt == 2 or not any(token in str(error) for token in (
                            "Conflict", "test failed", "testing value", "object has been modified")):
                        raise

        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
            submissions = list(pool.map(submit, self.pods()))
        self.save(f"{stage}-submissions.json", submissions)
        self.wait(f"{stage} effective resources on all three brokers", lambda: self.all_at(resources))

    def memory_samples(self, stage):
        samples = []
        end = time.monotonic() + self.args.settle_seconds
        while True:
            metrics = self.kubectl("get", "--raw",
                f"/apis/metrics.k8s.io/v1beta1/namespaces/{NAMESPACE}/pods", as_json=True)
            observed = {}
            sources = {}
            for item in metrics["items"]:
                sample_time = dt.datetime.fromisoformat(item["timestamp"].replace("Z", "+00:00"))
                if not 0 <= (dt.datetime.now(dt.timezone.utc) - sample_time).total_seconds() <= 60:
                    raise RuntimeError("resource metrics are stale or in the future")
                for c in item["containers"]:
                    if c["name"] == "rabbitmq":
                        observed[item["metadata"]["name"]] = int(quantity(c["usage"]["memory"]))
                        sources[item["metadata"]["name"]] = {
                            "timestamp": item["timestamp"], "window": item["window"],
                            "cpu": c["usage"]["cpu"],
                        }
            if set(observed) != BROKER_NAMES:
                raise RuntimeError("resource metric census is incomplete")
            samples.append({"time": now(), "memoryBytes": observed, "sources": sources})
            if time.monotonic() >= end:
                self.save(f"{stage}-memory.json", samples)
                return samples
            time.sleep(15)

    def platform(self):
        self.save("platform.json", {
            "versions": self.kubectl("version", "-o", "json", as_json=True),
            "nodes": [node["status"]["nodeInfo"] for node in self.kubectl(
                "get", "nodes", "-o", "json", as_json=True)["items"]],
            "operatorImages": [c["image"] for c in self.kubectl("get", "deployment", "rabbitmq-cluster-operator",
                "-n", "rabbitmq-system", "-o", "json", as_json=True)["spec"]["template"]["spec"]["containers"]],
            "metricsImages": [c["image"] for c in self.kubectl("get", "deployment", "metrics-server",
                "-n", "kube-system", "-o", "json", as_json=True)["spec"]["template"]["spec"]["containers"]],
        })

    def run(self):
        self.guard()
        self.kubectl("apply", "-f", str(Path(__file__).with_name("rabbitmq.yaml")))
        self.wait("baseline effective resources and broker readiness", lambda: self.all_at(BASELINE), timeout=600)
        self.start_api()
        baseline = self.snapshot("baseline", BASELINE)
        self.platform()
        self.kubectl("patch", "rabbitmqcluster", CLUSTER, "-n", NAMESPACE, "--type=merge", "-p",
                     json.dumps({"spec": {"override": {"statefulSet": {"spec": {
                         "updateStrategy": {"type": "OnDelete"}}}}}}))
        self.wait("owned StatefulSet OnDelete", lambda: self.get("statefulset", f"{CLUSTER}-server")
                  ["spec"]["updateStrategy"]["type"] == "OnDelete")
        self.transition(LARGER, "increase")
        samples = self.memory_samples("increase")
        increased = self.snapshot("increased", LARGER)
        safe_limit = quantity(SMALLER["limits"]["memory"]) * Decimal("0.8")
        if any(value > safe_limit for sample in samples for value in sample["memoryBytes"].values()):
            raise RuntimeError("smaller test limit lacks the required 20% observed memory headroom")
        self.transition(SMALLER, "decrease")
        self.memory_samples("decrease")
        decreased = self.snapshot("decreased", SMALLER)

        def unchanged_identity(snapshot):
            return all((a["uid"], a["containerID"], a["restarts"]) ==
                       (b["uid"], b["containerID"], b["restarts"])
                       for a, b in broker_pairs(baseline, snapshot))

        result = {
            "time": now(), "test": "phase-0-platform-probe-not-scaler-validation",
            "context": self.args.context,
            "increasedWithoutRestart": unchanged_identity(increased),
            "decreasedWithoutRestart": unchanged_identity(decreased),
            "runningBudgetFollowedIncrease": all(b["watermarkBytes"] > a["watermarkBytes"]
                for a, b in broker_pairs(baseline, increased)),
            "runningBudgetFollowedDecrease": all(b["watermarkBytes"] < a["watermarkBytes"]
                for a, b in broker_pairs(increased, decreased)),
            "runningBudgetMatchesConfiguration": all(budget_matches(snapshot)
                for snapshot in (baseline, increased, decreased)),
            "runningBudgetWithinLimits": all(0 < b["watermarkBytes"] < quantity(resources["limits"]["memory"])
                for snapshot, resources in ((baseline, BASELINE), (increased, LARGER), (decreased, SMALLER))
                for b in snapshot["brokers"]),
            "watermarkExceedsSmallerLimit": any(b["watermarkBytes"] > quantity(SMALLER["limits"]["memory"])
                                                for b in decreased["brokers"]),
        }
        result["fullMemoryGatePassed"] = all(result[k] for k in (
            "increasedWithoutRestart", "decreasedWithoutRestart",
            "runningBudgetFollowedIncrease", "runningBudgetFollowedDecrease", "runningBudgetWithinLimits",
            "runningBudgetMatchesConfiguration"))
        self.save("result.json", result)
        print(json.dumps(result, indent=2), flush=True)
        return 0 if result["fullMemoryGatePassed"] else 2


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kubeconfig", required=True)
    parser.add_argument("--context", required=True)
    parser.add_argument("--output", default=str(Path(__file__).with_name("evidence")))
    parser.add_argument("--settle-seconds", type=int, default=60)
    parser.add_argument("--reuse", action="store_true")
    args = parser.parse_args()
    if args.settle_seconds < 60:
        parser.error("--settle-seconds must be at least 60")
    probe = Probe(args)
    try:
        return probe.run()
    except (RuntimeError, ValueError, KeyError, subprocess.TimeoutExpired) as error:
        probe.save("error.json", {"time": now(), "error": str(error)})
        print(f"NOT PROVEN: {error}", flush=True)
        return 1
    finally:
        probe.close()


if __name__ == "__main__":
    raise SystemExit(main())
