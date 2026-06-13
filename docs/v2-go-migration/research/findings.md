# v1 architecture findings (Node.js)

Total source ≈ 830 LOC. Module map (all in `lib/` unless noted):

| v1 file | LOC | Responsibility | Go target |
|---|---|---|---|
| `index.js` | 28 | entrypoint, signal handlers, starts loop | `cmd/.../main.go` + `run` subcommand |
| `RabbitMQVerticalScaler.js` | 87 | orchestration, `applyScale()`, `main()` loop | `internal/controller` |
| `ScalingEngine.js` | 129 | profile calc, debounce/stability, messages | `internal/scaling` (highest parity risk) |
| `MetricsCollector.js` | 69 | RabbitMQ mgmt API GETs, wait-for-ready | `internal/metrics` (net/http) |
| `KubernetesClient.js` | 113 | CR get/patch, ConfigMap get/patch | `internal/k8s` (client-go dynamic + CoreV1) |
| `ConfigManager.js` | 82 | env-var config, profiles, thresholds, cpu→profile map | `internal/config` |
| `bin/rmq-vertical-scaler` | 324 | CLI `generate` → emits K8s YAML | `generate` subcommand + `internal/manifests` |

## Key behavioural facts (parity-critical)
- Loop interval default 5s; metrics fetch timeout 10s; wait-for-ready backoff 5s.
- Profile selection: scan `profileNames` high→low; first whose `maxQueueDepth > QUEUE_THRESHOLD`
  OR `messageRate > RATE_THRESHOLD` wins; else floor (`profileNames[0]`). First profile has no thresholds.
- Metrics extracted: `queue_totals.messages`, `message_stats.publish_details.rate`,
  `message_stats.deliver_get_details.rate`, `max(queues[].messages)`, backlog = publish − consume.
- Stability/debounce stored in a ConfigMap (`stable_profile`, `stable_since` unix secs).
  Recommendation change → reset timer, return false. Already-at-target → reset + true.
  Scale-up needs ≥ `DEBOUNCE_SCALE_UP_SECONDS` (default 30); scale-down ≥ `DEBOUNCE_SCALE_DOWN_SECONDS` (120).
- Current profile read from RabbitmqCluster CR `spec.resources.requests.cpu`, reverse-mapped.
- Both CR and ConfigMap mutated via **JSON Patch** (`op: replace`).
- In-cluster K8s auth only (`loadFromCluster`).

## Distribution / ops facts
- Docker: `node:20-alpine` multi-stage, dumb-init, non-root uid 1001, EXPOSE 8080, webpack build.
- CI: tag `v*` → Docker Hub push (`ferterahadi/rmq-vertical-scaler`) + GitHub release. No npm publish in CI.
- README markets it as a resource-saver — which is exactly why footprint is the right v2 benchmark.

## Footprint baseline expectations (to be measured, not assumed)
- v1 idle RSS: ~50–80 MB (V8). v2 Go target: ~8–15 MB.
- v1 image: ~120–180 MB. v2 `FROM scratch` target: ~10–20 MB.
