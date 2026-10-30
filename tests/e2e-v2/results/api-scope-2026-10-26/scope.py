#!/usr/bin/env python3
"""Verify live queue/rate visibility and fan-out accounting on the local fixture."""
import argparse
import base64
import json
import time
import uuid
from urllib.error import HTTPError
from urllib.parse import quote
from urllib.request import Request, build_opener, ProxyHandler

import pika
from matrix import Matrix
from run import probe_module, utc


class Scope(Matrix):
    def api(self, method, path, body=None):
        data = None if body is None else json.dumps(body).encode()
        request = Request(f"http://127.0.0.1:{self.probe.api_port}" + path, data=data, method=method,
            headers={"Authorization": self.probe.authorization, "Content-Type": "application/json"})
        with build_opener(ProxyHandler({})).open(request, timeout=15) as response:
            raw = response.read()
            return json.loads(raw) if raw else None

    def connection_vhost(self, vhost):
        secret = self.probe.get("secret", "rmq-probe-default-user")["data"]
        credentials = pika.PlainCredentials(base64.b64decode(secret["username"]).decode(),
                                           base64.b64decode(secret["password"]).decode())
        return pika.BlockingConnection(pika.ConnectionParameters("127.0.0.1", self.amqp_port,
            virtual_host=vhost, credentials=credentials, socket_timeout=15, heartbeat=30))

    def publish_to(self, vhost, queue, count):
        ids = set()
        with self.connection_vhost(vhost) as connection:
            channel = connection.channel()
            channel.queue_declare(queue=queue, durable=True, arguments={"x-queue-type": "quorum"})
            channel.confirm_delivery()
            for number in range(count):
                message_id = f"{self.run_id}-{vhost}-{queue}-{number}"
                channel.basic_publish("", queue, message_id.encode(), mandatory=True,
                    properties=pika.BasicProperties(message_id=message_id, delivery_mode=2))
                ids.add(message_id)
        return ids

    def consume_from(self, vhost, queue):
        ids = []
        with self.connection_vhost(vhost) as connection:
            channel = connection.channel()
            while True:
                method, props, body = channel.basic_get(queue, auto_ack=False)
                if method is None:
                    break
                if body.decode() != props.message_id:
                    raise RuntimeError("scope message body/ID mismatch")
                ids.append(props.message_id)
                channel.basic_ack(method.delivery_tag)
        return ids

    def credential_secret(self, name):
        patch = {"spec": {"template": {"spec": {"containers": [{"name": "scaler", "env": [
            {"name": "RMQ_USER", "value": None, "valueFrom": {"secretKeyRef": {"name": name, "key": "username"}}},
            {"name": "RMQ_PASS", "value": None, "valueFrom": {"secretKeyRef": {"name": name, "key": "password"}}}
        ]}]}}}}
        self.probe.kubectl("patch", "deployment", "rmq-vertical-scaler", "-n", self.namespace,
            "--type=strategic", "-p", json.dumps(patch))
        self.probe.wait("scaler credentials rollout ready", lambda: self.probe.kubectl("get", "deployment",
            "rmq-vertical-scaler", "-n", self.namespace, "-o", "json", as_json=True)["status"].get("readyReplicas", 0) == 1,
            timeout=120)

    def run(self):
        from pathlib import Path
        (self.output / "scope.py").write_bytes(Path(__file__).read_bytes())
        self.probe.guard()
        if self.status().get("operation") is not None:
            raise RuntimeError("scope test requires a converged baseline")
        self.probe.start_api()
        self.start_amqp()
        initial = self.current_pods()
        policy = self.base()
        policy["updatePolicy"]["mode"] = "Off"
        policy["triggers"] = [
            {"type": "rabbitmq", "metadata": {"mode": "QueueLength", "aggregation": "sum",
                "selector": {"queueNameRegex": "^same$"}, "thresholds": {"HIGH": 10}}},
            {"type": "rabbitmq", "metadata": {"mode": "MessageRate", "thresholds": {"HIGH": 10}}},
            {"type": "rabbitmq", "metadata": {"mode": "MessageRate",
                "selector": {"vhosts": ["/v23-visible"], "queueNameRegex": "^fanout-"},
                "thresholds": {"HIGH": 10}}}
        ]
        self.install(policy, "scope-Off")
        visible, private = "/v23-visible", "/v23-private"
        user, secret_name = "v23-scope-reader", "rmq-v23-restricted"
        admin = base64.b64decode(self.probe.get("secret", "rmq-probe-default-user")["data"]["username"]).decode()
        created_vhosts = []
        created_user = False
        created_secret = False
        try:
            for vhost in (visible, private):
                try:
                    self.api("GET", "/api/vhosts/" + quote(vhost, safe=""))
                    raise RuntimeError("scope vhost name already exists")
                except HTTPError as error:
                    if error.code != 404:
                        raise
                self.api("PUT", "/api/vhosts/" + quote(vhost, safe=""), {})
                created_vhosts.append(vhost)
                self.api("PUT", "/api/permissions/" + quote(vhost, safe="") + "/" + quote(admin, safe=""),
                         {"configure": ".*", "write": ".*", "read": ".*"})
            visible_ids = self.publish_to(visible, "same", 20)
            private_ids = self.publish_to(private, "same", 30)
            self.probe.wait("queue total deduplicates by vhost and excludes quorum replicas", lambda:
                self.observe()["observation"]["signals"][0]["value"] == 50, timeout=120)
            self.save("admin-scope.json", self.status())
            # Create a test reader only if no account with that name exists.
            try:
                self.api("GET", "/api/users/" + user)
                raise RuntimeError("scope user name already exists")
            except HTTPError as error:
                if error.code != 404:
                    raise
            password = uuid.uuid4().hex
            self.api("PUT", "/api/users/" + user, {"password": password, "tags": "management"})
            created_user = True
            self.api("PUT", "/api/permissions/" + quote(visible, safe="") + "/" + user,
                     {"configure": ".*", "write": ".*", "read": ".*"})
            # Credentials pass to kubectl over stdin and are never retained in evidence.
            import subprocess
            try:
                self.probe.get("secret", secret_name)
                raise RuntimeError("test credential Secret name already exists")
            except RuntimeError as error:
                if "NotFound" not in str(error):
                    raise
            secret = {"apiVersion": "v1", "kind": "Secret", "metadata": {"name": secret_name,
                "namespace": self.namespace, "labels": {"rmq-vertical-scaler-test": "v23-scope"}},
                "stringData": {"username": user, "password": password}}
            result = subprocess.run(self.probe.command + ["apply", "-f", "-"], input=json.dumps(secret),
                                    text=True, capture_output=True, timeout=30)
            if result.returncode:
                raise RuntimeError("could not create scoped test credential Secret")
            created_secret = True
            self.credential_secret(secret_name)
            self.probe.wait("restricted queues are visible but overview scope is unavailable", lambda:
                self.observe()["observation"]["signals"][0]["value"] == 20 and
                not self.status()["observation"]["signals"][1]["available"], timeout=120)
            self.save("restricted-scope.json", self.status())
            self.credential_secret("rmq-probe-default-user")
            self.probe.wait("administrator scope restored", lambda: self.observe()["observation"]["signals"][0]["value"] == 50,
                            timeout=120)
            # One ingress publish creates two selected queue copies; unroutable
            # confirmed ingress creates none. Compare cumulative counters after settling.
            with self.connection_vhost(visible) as connection:
                channel = connection.channel()
                channel.exchange_declare("v23-fanout", "fanout", durable=True)
                channel.exchange_declare("v23-unroutable", "fanout", durable=True)
                for queue in ("fanout-a", "fanout-b"):
                    channel.queue_declare(queue=queue, durable=True, arguments={"x-queue-type": "quorum"})
                    channel.queue_bind(queue, "v23-fanout")
                channel.confirm_delivery()
                before = self.api("GET", "/api/overview").get("message_stats", {}).get("publish", 0)
                fanout_ids = set()
                selected_rate_observation = None
                for number in range(100):
                    message_id = f"{self.run_id}-fanout-{number}"
                    channel.basic_publish("v23-fanout", "", message_id.encode(), mandatory=True,
                        properties=pika.BasicProperties(message_id=message_id, delivery_mode=2))
                    fanout_ids.add(message_id)
                    # Span multiple controller polls, rather than hoping a short
                    # burst happens to coincide with a rate sample.
                    time.sleep(0.3)
                    if number % 10 == 9:
                        observed = self.observe()
                        selected = observed["observation"]["signals"][2]
                        if selected["available"] and selected["complete"] and selected["value"] > 0:
                            selected_rate_observation = observed
                for number in range(10):
                    channel.basic_publish("v23-unroutable", "", b"intentionally-unroutable", mandatory=False,
                        properties=pika.BasicProperties(message_id=f"{self.run_id}-unroutable-{number}"))
            def counters_ready():
                overview = self.api("GET", "/api/overview").get("message_stats", {}).get("publish", 0)
                queues = [self.api("GET", "/api/queues/" + quote(visible, safe="") + "/" + name)
                          for name in ("fanout-a", "fanout-b")]
                return overview - before == 110 and all(q.get("message_stats", {}).get("publish") == 100 for q in queues)
            self.probe.wait("fanout and unroutable publish scope counters settle", counters_ready, timeout=90)
            if selected_rate_observation is None:
                raise RuntimeError("actual controller never observed a positive complete selected-queue rate")
            self.save("selected-rate-status.json", selected_rate_observation)
            queue_counters = [self.api("GET", "/api/queues/" + quote(visible, safe="") + "/" + name)
                              for name in ("fanout-a", "fanout-b")]
            result = {"time": utc(), "adminQueueTotal": 50, "restrictedQueueTotal": 20,
                "overviewUnavailableForRestrictedUser": True, "fanoutConfirmedIngress": 100,
                "intentionallyUnroutableConfirmedIngress": 10, "overviewPublishDelta": 110,
                "actualSelectedRateObserved": True,
                "selectedQueuePublishCopies": sum(q["message_stats"]["publish"] for q in queue_counters)}
            consumed = {"visibleSame": self.consume_from(visible, "same"),
                "privateSame": self.consume_from(private, "same"),
                "fanoutA": self.consume_from(visible, "fanout-a"),
                "fanoutB": self.consume_from(visible, "fanout-b")}
            confirmed = {"visibleSame": sorted(visible_ids), "privateSame": sorted(private_ids),
                "fanoutA": sorted(fanout_ids), "fanoutB": sorted(fanout_ids)}
            accounting = {name: set(ids) == set(confirmed[name]) for name, ids in consumed.items()}
            self.save("scope-message-ids.json", {"confirmed": confirmed, "consumed": consumed})
            if not all(accounting.values()) or not self.resources_unchanged(initial):
                raise RuntimeError("scope message accounting or Off resource isolation failed")
            result["durableMessageAccounting"] = accounting
            result["consumedDuplicates"] = {name: len(ids) - len(set(ids)) for name, ids in consumed.items()}
            result["unroutableMessagesExcludedFromDurabilityClaim"] = True
            result["withoutBrokerResourceChanges"] = True
            self.save("scope-result.json", result)
            print(json.dumps(result, indent=2), flush=True)
        finally:
            self.credential_secret("rmq-probe-default-user")
            if created_user:
                self.api("DELETE", "/api/users/" + user)
            for vhost in created_vhosts:
                self.api("DELETE", "/api/vhosts/" + quote(vhost, safe=""))
            if created_secret:
                self.probe.kubectl("delete", "secret", secret_name, "-n", self.namespace, "--ignore-not-found")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kubeconfig", required=True)
    parser.add_argument("--context", required=True)
    parser.add_argument("--image", default="rmq-vertical-scaler:v2.3-test")
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    args.scenario = "native"
    args.load_seconds = args.down_seconds = args.settle_seconds = 60
    args.cooldown_seconds = 600
    args.reuse = True
    args.extras_only = False
    run = Scope(args)
    try:
        run.run()
        return 0
    except Exception as error:
        run.save("scope-error.json", {"time": utc(), "error": str(error)})
        print(f"FAILED SCOPE: {error}", flush=True)
        return 1
    finally:
        run.close()


if __name__ == "__main__":
    raise SystemExit(main())
