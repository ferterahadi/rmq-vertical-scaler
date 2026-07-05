#!/usr/bin/env bash
# End-to-end test for v2.2.0 in-place resize on a local kind cluster.
#
# Scenarios (in order):
#   1. scale-up   LOW -> MEDIUM in place: pod requests change, restartCount
#                 stays 0, pod UIDs unchanged, consumer stays connected,
#                 CR synced, OnDelete override set, no broker memory alarm
#   2. scale-down MEDIUM -> LOW in place (memory decrease, requests only)
#   3. self-heal  deleted pod comes back at the current profile's size
#   4. infeasible oversized profile falls back to a rolling scale (auto mode)
#
# Usage: tests/e2e/run.sh [--keep]   (--keep skips cluster teardown)
set -euo pipefail

CLUSTER=rmq-e2e
NS=default
DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$DIR/../.." && pwd)"
KEEP="${1:-}"

PASS=0; FAIL=0
ok()   { echo "✅ $1"; PASS=$((PASS+1)); }
bad()  { echo "❌ $1"; FAIL=$((FAIL+1)); }
note() { echo "— $1"; }

server_pods() { kubectl get pods -n "$NS" -l app.kubernetes.io/name=rabbitmq -o name; }

pod_field() { kubectl get "$1" -n "$NS" -o jsonpath="$2"; }

assert_requests() { # $1 want-cpu $2 want-mem $3 label
  local all_ok=1
  for p in $(server_pods); do
    local cpu mem
    cpu=$(pod_field "$p" '{.spec.containers[?(@.name=="rabbitmq")].resources.requests.cpu}')
    mem=$(pod_field "$p" '{.spec.containers[?(@.name=="rabbitmq")].resources.requests.memory}')
    [ "$cpu" = "$1" ] && [ "$mem" = "$2" ] || { all_ok=0; note "$p requests=$cpu/$mem want=$1/$2"; }
  done
  [ "$all_ok" = 1 ] && ok "$3: all pods at $1/$2" || bad "$3: pod requests mismatch"
}

snapshot_uids()     { server_pods | while read -r p; do pod_field "$p" '{.metadata.uid}'; echo; done | sort; }
snapshot_restarts() { server_pods | while read -r p; do pod_field "$p" '{.status.containerStatuses[?(@.name=="rabbitmq")].restartCount}'; echo; done | sort; }

wait_for() { # $1 timeout-seconds $2 description $3... command
  local timeout=$1 desc=$2; shift 2
  local start; start=$(date +%s)
  until "$@" > /dev/null 2>&1; do
    if [ $(( $(date +%s) - start )) -ge "$timeout" ]; then
      bad "timeout waiting for: $desc"
      return 1
    fi
    sleep 5
  done
  note "$desc"
}

profile_cpu_is() { # $1 cpu
  [ "$(kubectl get rabbitmqclusters.rabbitmq.com rabbitmq -n "$NS" -o jsonpath='{.spec.resources.requests.cpu}')" = "$1" ]
}
pods_cpu_is() { # $1 cpu — every server pod at this request
  for p in $(server_pods); do
    [ "$(pod_field "$p" '{.spec.containers[?(@.name=="rabbitmq")].resources.requests.cpu}')" = "$1" ] || return 1
  done
}

echo "=== [0/6] kind cluster + operator + images ==="
if ! kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  kind create cluster --name "$CLUSTER" --wait 120s
fi
kubectl config use-context "kind-$CLUSTER" > /dev/null

SERVER_MINOR=$(kubectl version -o json 2>/dev/null | grep -o '"minor": *"[0-9]*"' | grep -o '[0-9]*' | head -1)
[ "${SERVER_MINOR:-0}" -ge 33 ] && ok "K8s server 1.$SERVER_MINOR >= 1.33" || bad "K8s server 1.$SERVER_MINOR < 1.33"

# Recent cluster-operator releases ship Certificate/Issuer objects and need
# cert-manager's CRDs + webhook before they apply cleanly.
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml > /dev/null
wait_for 180 "cert-manager webhook ready" \
  kubectl wait -n cert-manager deploy/cert-manager-webhook --for=condition=Available --timeout=5s
kubectl apply -f https://github.com/rabbitmq/cluster-operator/releases/latest/download/cluster-operator.yml > /dev/null
wait_for 120 "cluster operator ready" \
  kubectl wait -n rabbitmq-system deploy/rabbitmq-cluster-operator --for=condition=Available --timeout=5s

(cd "$ROOT" && docker build -q -t rmq-vertical-scaler:e2e .) > /dev/null
kind load docker-image rmq-vertical-scaler:e2e --name "$CLUSTER" > /dev/null
note "scaler image built + loaded"

echo "=== [1/6] RabbitMQ cluster (3 nodes) ==="
kubectl apply -f "$DIR/rabbitmq-small.yaml" > /dev/null
wait_for 600 "RabbitmqCluster AllReplicasReady" \
  kubectl wait rabbitmqclusters.rabbitmq.com/rabbitmq -n "$NS" --for=condition=AllReplicasReady --timeout=5s
assert_requests 100m 300Mi "baseline"

echo "=== [2/6] scaler (helm) ==="
helm upgrade --install rmq-scaler "$ROOT/charts/rmq-vertical-scaler" -n "$NS" -f "$DIR/values.yaml" > /dev/null
wait_for 120 "scaler deployment ready" \
  kubectl wait deploy/rmq-vertical-scaler -n "$NS" --for=condition=Available --timeout=5s

UIDS_BEFORE=$(snapshot_uids)
RESTARTS_BEFORE=$(snapshot_restarts)

echo "=== [3/6] scale-up LOW -> MEDIUM (in place) ==="
kubectl apply -f "$DIR/load.yaml" > /dev/null
wait_for 120 "consumer connected" \
  kubectl wait deploy/e2e-consumer -n "$NS" --for=condition=Available --timeout=5s
wait_for 300 "pods resized to MEDIUM (200m)" pods_cpu_is 200m
assert_requests 200m 600Mi "scale-up"

# Mode resolution is lazy (logged with the first applied scale action);
# poll briefly in case the log write races this check.
if wait_for 60 "mode line in scaler logs" \
  sh -c "kubectl logs deploy/rmq-vertical-scaler -n $NS --tail=-1 | grep -q 'Scale mode: inplace'"; then
  ok "scaler resolved SCALE_MODE=auto -> inplace"
else
  bad "scaler did not resolve to inplace mode"
fi

[ "$(snapshot_uids)" = "$UIDS_BEFORE" ] && ok "scale-up: pod UIDs unchanged (no restart)" || bad "scale-up: pods were recreated"
[ "$(snapshot_restarts)" = "$RESTARTS_BEFORE" ] && ok "scale-up: restartCount unchanged" || bad "scale-up: containers restarted"
profile_cpu_is 200m && ok "scale-up: CR synced to MEDIUM" || bad "scale-up: CR not synced"

STRATEGY=$(kubectl get rabbitmqclusters.rabbitmq.com rabbitmq -n "$NS" -o jsonpath='{.spec.override.statefulSet.spec.updateStrategy.type}')
[ "$STRATEGY" = "OnDelete" ] && ok "OnDelete override set" || bad "OnDelete override missing (got '$STRATEGY')"

# The watermark derives from the (unchanged) memory limit — a resize must
# never trip the broker's memory alarm.
if kubectl exec -n "$NS" rabbitmq-server-0 -c rabbitmq -- rabbitmqctl status 2>/dev/null | grep -qi "memory alarm"; then
  bad "broker memory alarm raised after in-place resize"
else
  ok "no broker memory alarm after resize"
fi

CONSUMER_RESTARTS=$(kubectl get pods -n "$NS" -l app=e2e-consumer -o jsonpath='{.items[0].status.containerStatuses[0].restartCount}')
[ "$CONSUMER_RESTARTS" = "0" ] && ok "consumer connection uninterrupted" || bad "consumer restarted $CONSUMER_RESTARTS times"

echo "=== [4/6] scale-down MEDIUM -> LOW (in place, memory decrease) ==="
kubectl delete deploy e2e-producer -n "$NS" > /dev/null
# Purging while the producer pod is still terminating repopulates the queue.
kubectl wait --for=delete pod -l app=e2e-producer -n "$NS" --timeout=120s > /dev/null 2>&1 || true
# Purge until actually empty (in-flight messages can survive a single purge).
for _ in $(seq 1 10); do
  kubectl exec -n "$NS" rabbitmq-server-0 -c rabbitmq -- rabbitmqctl purge_queue e2e-load > /dev/null 2>&1 || true
  READY=$(kubectl exec -n "$NS" rabbitmq-server-0 -c rabbitmq -- rabbitmqctl list_queues messages --quiet 2>/dev/null | tail -1 | tr -dc 0-9)
  [ "${READY:-0}" -lt 10 ] && break
  sleep 5
done
note "queue drained (${READY:-?} messages left)"
wait_for 300 "pods resized back to LOW (100m)" pods_cpu_is 100m
assert_requests 100m 300Mi "scale-down"
[ "$(snapshot_uids)" = "$UIDS_BEFORE" ] && ok "scale-down: pod UIDs unchanged (memory decrease in place)" || bad "scale-down: pods were recreated"
[ "$(snapshot_restarts)" = "$RESTARTS_BEFORE" ] && ok "scale-down: restartCount unchanged" || bad "scale-down: containers restarted"

echo "=== [5/6] self-heal: deleted pod returns at current profile ==="
kubectl delete pod rabbitmq-server-1 -n "$NS" > /dev/null
wait_for 300 "rabbitmq-server-1 ready again" \
  kubectl wait pod/rabbitmq-server-1 -n "$NS" --for=condition=Ready --timeout=5s
HEAL_CPU=$(pod_field pod/rabbitmq-server-1 '{.spec.containers[?(@.name=="rabbitmq")].resources.requests.cpu}')
[ "$HEAL_CPU" = "100m" ] && ok "self-heal: recreated pod at current profile (100m)" || bad "self-heal: recreated pod at $HEAL_CPU"

echo "=== [6/6] infeasible resize falls back to rolling (auto mode) ==="
# A MEDIUM profile larger than the node can ever fit: the kubelet marks the
# resize Infeasible and auto mode must revert to a rolling scale. The rolling
# attempt itself can never complete (nothing can fit 12Gi here) — the point
# is observing the detection + fallback, then cleaning up immediately.
kubectl set env deploy/rmq-vertical-scaler -n "$NS" PROFILE_MEDIUM_MEMORY=12Gi > /dev/null
kubectl rollout status deploy/rmq-vertical-scaler -n "$NS" --timeout=60s > /dev/null
kubectl apply -f "$DIR/load.yaml" > /dev/null
if wait_for 300 "scaler logged infeasible fallback" \
  sh -c "kubectl logs deploy/rmq-vertical-scaler -n $NS --tail=-1 | grep -q 'falling back to a rolling scale'"; then
  ok "infeasible resize triggered rolling fallback"
  STRATEGY=$(kubectl get rabbitmqclusters.rabbitmq.com rabbitmq -n "$NS" -o jsonpath='{.spec.override.statefulSet.spec.updateStrategy.type}')
  [ "$STRATEGY" = "RollingUpdate" ] && ok "override reverted to RollingUpdate for fallback" || bad "override is '$STRATEGY', want RollingUpdate"
else
  bad "no rolling fallback observed"
fi
# Stop the doomed rolling attempt before it degrades the cluster: restore the
# sane profile and remove the load so the scaler settles back to LOW.
kubectl set env deploy/rmq-vertical-scaler -n "$NS" PROFILE_MEDIUM_MEMORY- > /dev/null
kubectl delete -f "$DIR/load.yaml" --ignore-not-found > /dev/null 2>&1 || true

echo
echo "=== RESULT: $PASS passed, $FAIL failed ==="
if [ "$KEEP" != "--keep" ]; then
  kind delete cluster --name "$CLUSTER" > /dev/null || true
  note "cluster deleted (use --keep to retain)"
fi
[ "$FAIL" = 0 ]
