---
name: rmq-generate-config
description: Use when an operator needs to create or revise an rmq-vertical-scaler config.json — when they describe their RabbitMQ workload (queue depths, message rates, burst patterns, node count) and want scaling profiles, thresholds, and debounce settings chosen for them.
---

# Generate an rmq-vertical-scaler config

## Overview
The scaler is driven by a JSON config (validated against `schema/config-schema.json`) that the `generate` command turns into Kubernetes manifests. This skill interviews the operator about their workload and produces a valid config.

## When to use
- "Help me set up / configure the RabbitMQ scaler"
- "What scaling profiles should I use for my workload?"
- They have an existing config and want it tuned for new traffic.

Do NOT use for deploying — that's `rmq-deploy`. This skill only produces the config file.

## Interview first
Start from `rmq-vertical-scaler init` (writes `my-config.json`) instead of hand-copying a template when the CLI is available. Ask only what you can't infer. Map answers to config:

| Ask | Drives |
|---|---|
| Typical vs peak queue depth (messages)? | `queue` thresholds |
| Typical vs peak publish/consume rate (msg/s)? | `rate` thresholds |
| How many RabbitMQ nodes? (need 3+ quorum) | safety warning if <3 |
| How bursty? (steady / spiky) | `debounce` values |
| CPU/memory budget at idle vs peak? | profile `cpu`/`memory` |
| Namespace + RabbitMQ service name? | `kubernetes`, `rmq.host` |

## Config rules (must hold)
- `profiles`: keys must match `^[A-Z_]+$`. Each needs `cpu` + `memory`. `queue`/`rate` optional.
- **First profile is the floor** — it gets NO `queue`/`rate` (idle baseline). Every other profile needs at least one threshold.
- Selection: engine scans **highest profile to lowest**, picks the first whose `queue` OR `rate` is exceeded. Order thresholds ascending across profiles.
- `cpu`: `^[0-9]+(\.[0-9]+)?(m)?$` (e.g. `330m`, `2`). `memory`: must end `Mi`/`Gi`/`Ti` (e.g. `2Gi`).
- `rmq.host` = Kubernetes service DNS: `<service>.<namespace>.svc.cluster.local`. `port` default `15672`.
- Bursty workloads → longer `scaleDownSeconds` (e.g. 300) so it doesn't thrash on dips.

## Example output
```json
{
  "$schema": "https://raw.githubusercontent.com/ferterahadi/rmq-vertical-scaler/master/schema/config-schema.json",
  "profiles": {
    "LOW":    { "cpu": "330m",  "memory": "2Gi" },
    "MEDIUM": { "cpu": "800m",  "memory": "3Gi", "queue": 2000,  "rate": 200 },
    "HIGH":   { "cpu": "1600m", "memory": "4Gi", "queue": 10000, "rate": 1000 },
    "CRITICAL": { "cpu": "2400m", "memory": "8Gi", "queue": 50000, "rate": 2000 }
  },
  "debounce": { "scaleUpSeconds": 30, "scaleDownSeconds": 120 },
  "checkInterval": 5,
  "rmq": { "host": "rabbitmq.production.svc.cluster.local", "port": "15672" },
  "kubernetes": { "namespace": "production", "rmqServiceName": "rabbitmq" }
}
```

Start from `examples/template-config.json`, `basic-config.json`, or `production-config.json` if available — adapt rather than build from scratch.

## Validate before finishing
- Confirm it parses and matches the schema. Run `rmq-vertical-scaler generate --config <file> -o /tmp/check.yaml` (or the docker equivalent) — `generate` now **enforces** the schema (non-zero exit with a violation list), so a clean generate is authoritative.
- Warn loudly if node count <3 or single-node: scaling restarts cause message loss (README safety note).

## Common mistakes
- Putting a threshold on the first (floor) profile → it should be unconditional.
- Thresholds not monotonically increasing → a lower profile shadows a higher one.
- `memory` without a unit suffix → fails schema validation.
- `rmq.host` set to a bare service name instead of the full cluster DNS.
