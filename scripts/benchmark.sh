#!/usr/bin/env bash
# Footprint benchmark harness for rmq-vertical-scaler v1 (Node) vs v2 (Go).
# Measures image size, cold start, and samples idle RSS/CPU. Requires both
# versions deployed to the same cluster (see research/benchmark.md).
#
# Usage:
#   NAMESPACE=prod V1_DEPLOY=rmq-vertical-scaler-v1 V2_DEPLOY=rmq-vertical-scaler \
#     SAMPLES=288 INTERVAL=300 ./scripts/benchmark.sh
#
# (288 samples × 300s ≈ 24h.)
set -euo pipefail

NAMESPACE="${NAMESPACE:-prod}"
V1_DEPLOY="${V1_DEPLOY:-rmq-vertical-scaler-v1}"
V2_DEPLOY="${V2_DEPLOY:-rmq-vertical-scaler}"
V1_IMAGE="${V1_IMAGE:-ferterahadi/rmq-vertical-scaler:1.0.2}"
V2_IMAGE="${V2_IMAGE:-ferterahadi/rmq-vertical-scaler:2.0.0}"
SAMPLES="${SAMPLES:-12}"
INTERVAL="${INTERVAL:-300}"
OUT="${OUT:-research}"

mkdir -p "$OUT"

echo "== Image sizes =="
if command -v docker >/dev/null 2>&1; then
  docker image inspect "$V1_IMAGE" --format '{{.RepoTags}} {{.Size}} bytes' 2>/dev/null || echo "v1 image not pulled: $V1_IMAGE"
  docker image inspect "$V2_IMAGE" --format '{{.RepoTags}} {{.Size}} bytes' 2>/dev/null || echo "v2 image not pulled: $V2_IMAGE"
else
  echo "docker not found — skipping image-size measurement"
fi

cold_start() {
  local deploy="$1"
  local start_ts first_decision_ts
  start_ts="$(kubectl -n "$NAMESPACE" get pods -l app="$deploy" \
    -o jsonpath='{.items[0].status.startTime}' 2>/dev/null || true)"
  first_decision_ts="$(kubectl -n "$NAMESPACE" logs deployment/"$deploy" --timestamps 2>/dev/null \
    | grep -m1 -E 'Queue Depth|Applying|Scaling skipped|not stable' | awk '{print $1}' || true)"
  echo "  $deploy: start=$start_ts firstDecision=$first_decision_ts"
}

echo "== Cold start (start time -> first decision log) =="
cold_start "$V1_DEPLOY"
cold_start "$V2_DEPLOY"

echo "== Sampling idle RSS/CPU ($SAMPLES samples every ${INTERVAL}s) =="
csv="$OUT/footprint-samples.csv"
echo "timestamp,deployment,cpu,memory" > "$csv"
for ((i = 1; i <= SAMPLES; i++)); do
  ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  for deploy in "$V1_DEPLOY" "$V2_DEPLOY"; do
    line="$(kubectl -n "$NAMESPACE" top pod -l app="$deploy" --no-headers 2>/dev/null | head -1 || true)"
    cpu="$(echo "$line" | awk '{print $2}')"
    mem="$(echo "$line" | awk '{print $3}')"
    echo "$ts,$deploy,${cpu:-NA},${mem:-NA}" | tee -a "$csv"
  done
  [[ $i -lt $SAMPLES ]] && sleep "$INTERVAL"
done

echo "== Done. Raw samples: $csv =="
echo "Summarise the median/steady-state RSS and update research/benchmark.md."
