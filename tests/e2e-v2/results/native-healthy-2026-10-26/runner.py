#!/usr/bin/env python3
"""Run the actual version-2 scaler against three local Operator-managed brokers.

Explicit loopback kind only. Keep generated evidence and test namespace for
inspection. This runner never uses the normal kubeconfig implicitly.
"""
import argparse
import base64
import concurrent.futures
import datetime as dt
import hashlib
import importlib.util
import json
from pathlib import Path
import socket
import subprocess
import sys
import time

import pika

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("feasibility_probe", ROOT / "tests/feasibility/run.py")
probe_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe_module)


def utc():
    return dt.datetime.now(dt.timezone.utc).isoformat()


class Cycle:
    def __init__(self, args):
        self.args = args
        self.namespace = f"rmq-v23-e2e-{args.scenario}"
        probe_module.NAMESPACE = self.namespace
        self.probe = probe_module.Probe(args)
        self.output = Path(args.output)
        (self.output / "runner.py").write_bytes(Path(__file__).read_bytes())
        (self.output / "probe.py").write_bytes((ROOT / "tests/feasibility/run.py").read_bytes())
        self.forward = None
        self.amqp_port = None
        self.confirmed = set()
        self.consumed = set()
        self.duplicates = 0
        self.uncertain_attempts = []

    def save(self, name, data):
        self.probe.save(name, data)

    def status(self):
        return json.loads(self.probe.get("configmap", "rmq-vertical-scaler-status")["data"]["status.json"])

    def observe(self):
        state = self.status()
        try:
            pods = self.probe.pods()
            census_reason = None
        except RuntimeError:
            pods = []
            census_reason = "Broker census incomplete during transition"
        value = {"observedAt": utc(), "status": state, "censusReason": census_reason, "brokers": [{
            "name": p["metadata"]["name"], "uid": p["metadata"]["uid"],
            "desired": self.probe.container(p)["resources"],
            "effective": self.probe.status(p).get("resources"),
            "containerID": self.probe.status(p).get("containerID"),
            "restarts": self.probe.status(p).get("restartCount"),
            "ready": self.probe.status(p).get("ready"),
        } for p in pods]}
        with (self.output / "observations.jsonl").open("a") as file:
            file.write(json.dumps(value) + "\n")
        return state

    def completed(self, direction):
        state = self.observe()
        last = state.get("lastOperation") or {}
        return last.get("stage") == "Completed" and last.get("direction") == direction

    def start_amqp(self):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            self.amqp_port = sock.getsockname()[1]
        self.forward = subprocess.Popen(self.probe.command + ["port-forward", "-n", self.namespace,
            "service/rmq-probe", f"{self.amqp_port}:5672", "--address", "127.0.0.1"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.probe.wait("AMQP loopback connection", self.amqp_ready, timeout=60)

    def amqp_ready(self):
        try:
            with socket.create_connection(("127.0.0.1", self.amqp_port), timeout=2):
                return True
        except OSError:
            return False

    def connection(self):
        secret = self.probe.get("secret", "rmq-probe-default-user")["data"]
        credentials = pika.PlainCredentials(base64.b64decode(secret["username"]).decode(),
                                            base64.b64decode(secret["password"]).decode())
        return pika.BlockingConnection(pika.ConnectionParameters("127.0.0.1", self.amqp_port,
            credentials=credentials, heartbeat=30, blocked_connection_timeout=30,
            socket_timeout=15, connection_attempts=3, retry_delay=2))

    def declare(self):
        with self.connection() as connection:
            connection.channel().queue_declare(queue="v23-accounted", durable=True,
                                               arguments={"x-queue-type": "quorum"})

    def publish(self):
        # Uncertain rolling publishes retry the SAME ID; only a received broker
        # confirmation enters the confirmed set. Consumption counts duplicates.
        deadline = time.monotonic() + self.args.load_seconds
        retry_deadline = deadline + 120
        sequence = 0
        connection = None
        pending = None
        try:
            while time.monotonic() < deadline or pending is not None:
                if pending is None:
                    pending = f"v23-{sequence}"
                try:
                    if connection is None or connection.is_closed:
                        if self.forward is None or self.forward.poll() is not None:
                            self.start_amqp()
                        connection = self.connection()
                        channel = connection.channel()
                        channel.confirm_delivery()
                    channel.basic_publish(exchange="", routing_key="v23-accounted",
                        body=(pending + ":" + "x" * 200).encode(),
                        properties=pika.BasicProperties(delivery_mode=2, message_id=pending), mandatory=True)
                    self.confirmed.add(pending)
                    pending = None
                    sequence += 1
                    connection.process_data_events(time_limit=0)
                    time.sleep(0.01)
                except (pika.exceptions.AMQPError, OSError) as error:
                    if self.args.scenario != "rolling" or time.monotonic() > retry_deadline:
                        raise RuntimeError("unconfirmed AMQP publish; cycle is not proven") from error
                    self.uncertain_attempts.append({"time": utc(), "id": pending,
                        "errorType": type(error).__name__})
                    if connection is not None and connection.is_open:
                        try:
                            connection.close()
                        except pika.exceptions.AMQPError:
                            pass
                    connection = None
                    time.sleep(2)
        finally:
            if connection is not None and connection.is_open:
                connection.close()
            self.save("confirmed-ids.json", sorted(self.confirmed))
            self.save("uncertain-publish-attempts.json", self.uncertain_attempts)

    def drain(self):
        empty_since = None
        with self.connection() as connection:
            channel = connection.channel()
            while True:
                method, properties, body = channel.basic_get("v23-accounted", auto_ack=False)
                if method is None:
                    if empty_since is None:
                        empty_since = time.monotonic()
                    if time.monotonic() - empty_since > 5:
                        break
                    connection.process_data_events(time_limit=1)
                    continue
                empty_since = None
                message_id = properties.message_id
                if message_id in self.consumed:
                    self.duplicates += 1
                self.consumed.add(message_id)
                if body.split(b":", 1)[0].decode() != message_id:
                    raise RuntimeError("message body/ID mismatch")
                channel.basic_ack(method.delivery_tag)
        self.save("consumed-ids.json", sorted(self.consumed))

    def policy(self):
        data = json.loads((ROOT / "examples/v2/queues.json").read_text())
        data["kubernetes"] = {"namespace": self.namespace, "rmqServiceName": "rmq-probe"}
        data["rmq"] = {"host": f"rmq-probe.{self.namespace}.svc.cluster.local", "port": "15672"}
        data["triggers"] = [{"type": "rabbitmq", "metadata": {"mode": "QueueLength",
            "aggregation": "sum", "thresholds": {"HIGH": 10}}}]
        data["profiles"]["LOW"] = probe_module.BASELINE
        high = {"requests": {"cpu": "800m", "memory": "256Mi"},
                "limits": {"cpu": "1", "memory": "1Gi"}}
        if self.args.scenario != "native":
            high = {"requests": {"cpu": "800m", "memory": "512Mi"},
                    "limits": {"cpu": "1500m", "memory": "1Gi"}}
        if self.args.scenario in ("rolling", "blocked"):
            high["limits"]["memory"] = "1536Mi"
        data["profiles"]["HIGH"] = high
        data["updatePolicy"] = {"mode": "Auto", "allowRollingFallback": self.args.scenario == "rolling"}
        data["behavior"]["scaleDown"]["stepIntervalSeconds"] = self.args.down_seconds
        self.high = high
        return data

    def capacity(self):
        nodes = self.probe.kubectl("get", "nodes", "-o", "json", as_json=True)["items"]
        pods = self.probe.kubectl("get", "pods", "-A", "-o", "json", as_json=True)["items"]
        broker_uids = {p["metadata"]["uid"] for p in self.probe.pods()}
        result = {"observedAt": utc(), "nodes": [], "enoughRequestedResources": True}
        for node in nodes:
            allocated = {key: probe_module.quantity(node["status"]["allocatable"][key]) for key in ("cpu", "memory")}
            projected = {"cpu": 0, "memory": 0}
            entries = []
            for pod in pods:
                if pod["spec"].get("nodeName") != node["metadata"]["name"] or pod.get("status", {}).get("phase") in ("Succeeded", "Failed"):
                    continue
                requests = {}
                for key in ("cpu", "memory"):
                    regular = sum(probe_module.quantity(c.get("resources", {}).get("requests", {}).get(key, "0")) for c in pod["spec"]["containers"])
                    init = max([probe_module.quantity(c.get("resources", {}).get("requests", {}).get(key, "0")) for c in pod["spec"].get("initContainers", [])] or [0])
                    requested = max(regular, init)
                    if pod["metadata"]["uid"] in broker_uids:
                        requested = probe_module.quantity(self.high["requests"][key])
                    requests[key] = requested
                    projected[key] += requested
                entries.append({"namespace": pod["metadata"]["namespace"], "name": pod["metadata"]["name"],
                                "projectedCPU": str(requests["cpu"]), "projectedMemoryBytes": str(requests["memory"])})
            enough = all(projected[k] <= allocated[k] for k in allocated)
            ready = any(c["type"] == "Ready" and c["status"] == "True" for c in node["status"]["conditions"])
            pressure = any(c["type"] in ("MemoryPressure", "DiskPressure", "PIDPressure") and c["status"] == "True" for c in node["status"]["conditions"])
            result["enoughRequestedResources"] &= enough and ready and not pressure
            result["nodes"].append({"name": node["metadata"]["name"], "allocatableCPU": str(allocated["cpu"]),
                "allocatableMemoryBytes": str(allocated["memory"]), "projectedCPU": str(projected["cpu"]),
                "projectedMemoryBytes": str(projected["memory"]), "ready": ready, "pressure": pressure, "pods": entries})
        return result

    def run(self):
        self.probe.guard()
        fixture = (ROOT / "tests/feasibility/rabbitmq.yaml").read_text().replace(
            "rmq-v23-feasibility", self.namespace)
        fixture_path = self.output / "rabbitmq.yaml"
        fixture_path.write_text(fixture)
        self.probe.kubectl("apply", "-f", str(fixture_path))
        self.probe.wait("three baseline brokers", lambda: self.probe.all_at(probe_module.BASELINE), timeout=600)
        self.probe.start_api()
        self.probe.platform()
        baseline = self.probe.snapshot("baseline", probe_module.BASELINE)
        self.start_amqp()
        self.declare()
        policy = self.policy()
        policy_file = self.output / "policy.json"
        policy_file.write_text(json.dumps(policy, indent=2) + "\n")
        deployment = self.output / "scaler.yaml"
        subprocess.run(["go", "run", "./cmd/rmq-vertical-scaler", "generate", "--config", str(policy_file),
                        "--image", self.args.image, "--output", str(deployment)], cwd=ROOT, check=True)
        self.probe.kubectl("apply", "-f", str(deployment))
        self.probe.wait("actual scaler observes baseline", lambda: self.status().get("observation", {}).get(
            "currentProfileIndex") == 0, timeout=180)
        capacity = self.capacity()
        self.save("declared-capacity.json", capacity)
        if not capacity["enoughRequestedResources"]:
            raise RuntimeError("healthy-case requested-resource/pressure precondition not met")
        workload_start = utc()
        self.save("protocol.json", {"scenario": self.args.scenario, "workloadStart": workload_start,
            "loadSeconds": self.args.load_seconds, "cooldownBudgetSeconds": self.args.cooldown_seconds,
            "downStepSeconds": self.args.down_seconds, "pollingSeconds": 15, "upStabilizationSeconds": 30,
            "nativeComparisonPolicy": self.args.scenario == "native", "image": self.args.image,
            "cluster": "three Operator-managed brokers, one local node", "test": "actual-repository-controller"})
        up = None
        with concurrent.futures.ThreadPoolExecutor(max_workers=1) as executor:
            publishing = executor.submit(self.publish)
            while not publishing.done():
                state = self.observe()
                last = state.get("lastOperation") or {}
                if last.get("direction") == "up" and last.get("stage") == "Completed":
                    up = last
                time.sleep(5)
            publishing.result()
        cooldown_deadline = time.monotonic() + self.args.cooldown_seconds
        self.save("workload-ended.json", {"time": utc(), "confirmedCount": len(self.confirmed)})
        if self.args.scenario == "blocked":
            state = self.observe()
            if state.get("reason") != "MemoryLimitRequiresBrokerBudgetReload" or not self.probe.all_at(probe_module.BASELINE):
                raise RuntimeError("strict mode did not block the complete memory-limit transition")
            up = None
        else:
            self.probe.wait("effective scale-up completion", lambda: self.completed("up"), timeout=180)
            up = self.status()["lastOperation"]
            if self.args.scenario == "rolling":
                self.restart_forwards()
            self.probe.snapshot("increased", self.high)
        # Rolling may replace the pod selected by port-forward. Reopen API and
        # AMQP connections only after convergence; load itself must be accounted.
        if self.args.scenario == "rolling":
            self.restart_forwards()
        self.drain()
        remaining = self.probe.nodes()  # also verifies all broker nodes are running
        self.save("nodes-after-drain.json", remaining)
        if self.confirmed != self.consumed:
            raise RuntimeError(f"message IDs differ: confirmed={len(self.confirmed)} consumed={len(self.consumed)}")
        down = None
        if self.args.scenario != "blocked":
            self.probe.wait("one-step effective scale-down completion", lambda: self.completed("down"),
                            timeout=max(0, cooldown_deadline - time.monotonic()))
            down = self.status()["lastOperation"]
            if self.args.scenario == "rolling":
                self.restart_forwards()
            self.probe.snapshot("decreased", probe_module.BASELINE)
        final_pods = {p["metadata"]["name"]: p for p in self.probe.pods()}
        no_disruption = all(old["uid"] == final_pods[old["name"]]["metadata"]["uid"] and
            old["containerID"] == self.probe.status(final_pods[old["name"]]).get("containerID") and
            old["restarts"] == self.probe.status(final_pods[old["name"]]).get("restartCount")
            for old in baseline["brokers"])
        if self.args.scenario != "rolling" and not no_disruption:
            raise RuntimeError("unexpected broker disruption in strict in-place scenario")
        up_seconds = None
        if up:
            up_seconds = (dt.datetime.fromisoformat(up["completedAt"].replace("Z", "+00:00")) -
                          dt.datetime.fromisoformat(up["episodeStart"].replace("Z", "+00:00"))).total_seconds()
        result = {"time": utc(), "scenario": self.args.scenario, "confirmed": len(self.confirmed),
            "consumedUnique": len(self.consumed), "duplicates": self.duplicates,
            "uncertainPublishAttempts": len(self.uncertain_attempts),
            "allConfirmedIDsAccounted": self.confirmed == self.consumed,
            "withoutBrokerReplacementOrRestart": no_disruption, "upCompletionSeconds": up_seconds,
            "healthyUpObjectiveMet": up_seconds is not None and up_seconds <= 120,
            "up": up, "down": down, "fullAcceptanceMatrix": False,
            "scope": "declared happy-path cycle; fault/recovery cases remain separate",
            "runnerSHA256": hashlib.sha256((self.output / "runner.py").read_bytes()).hexdigest()}
        self.save("result.json", result)
        print(json.dumps(result, indent=2), flush=True)
        if self.args.scenario in ("native", "inplace") and not result["healthyUpObjectiveMet"]:
            raise RuntimeError("healthy in-place scale-up exceeded the two-minute objective")

    def restart_forwards(self):
        self.probe.close()
        if self.forward:
            self.forward.terminate()
            self.forward.wait(timeout=5)
        self.probe.start_api()
        self.start_amqp()

    def close(self):
        self.probe.close()
        if self.forward is not None:
            self.forward.terminate()
            try:
                self.forward.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.forward.kill()
                self.forward.wait(timeout=5)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kubeconfig", required=True)
    parser.add_argument("--context", required=True)
    parser.add_argument("--scenario", choices=("native", "inplace", "rolling", "blocked"), default="native")
    parser.add_argument("--image", default="rmq-vertical-scaler:v2.3-test")
    parser.add_argument("--output", required=True)
    parser.add_argument("--load-seconds", type=int, default=300)
    parser.add_argument("--down-seconds", type=int, default=300)
    parser.add_argument("--cooldown-seconds", type=int, default=600)
    args = parser.parse_args()
    args.reuse = False
    args.settle_seconds = 60
    if args.load_seconds < 60 or args.down_seconds < 60 or args.cooldown_seconds < args.down_seconds:
        parser.error("load/down must be at least60s; cooldown must cover down window")
    cycle = Cycle(args)
    try:
        cycle.run()
        return 0
    except Exception as error:
        cycle.save("error.json", {"time": utc(), "error": str(error)})
        print(f"FAILED: {error}", flush=True)
        return 1
    finally:
        cycle.close()


if __name__ == "__main__":
    raise SystemExit(main())
