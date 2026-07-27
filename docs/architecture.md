# Architecture

## Component overview

```
┌─────────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│  internal/metrics   │────│ internal/scaling │────│   internal/k8s  │
│                     │    │                  │    │                 │
│ • RabbitMQ API      │    │ • Profile logic  │    │ • client-go     │
│ • net/http GETs     │    │ • Thresholds     │    │ • CRD (dynamic) │
│ • Wait-for-ready    │    │ • Debounce (pure)│    │ • pods/resize   │
└─────────────────────┘    └──────────────────┘    └─────────────────┘
         │                           │                        │
         └───────────────────────────┼────────────────────────┘
                                     │
                  ┌─────────────────────────────────────┐
                  │       internal/controller           │
                  │                                     │
                  │ • Orchestration (ApplyScale/Run)    │
                  │ • internal/config (env vars)        │
                  │ • Signal-driven graceful shutdown   │
                  │ • Stability tracking                │
                  └─────────────────────────────────────┘
```

`internal/scaling` is deliberately pure — no I/O, no clock, no Kubernetes types —
so the decision engine is exhaustively table-tested, including a golden test that
proves byte-identical decisions to the Node.js v1.

## The control loop

1. **Collect metrics** — fetch total queue depth and message rate from the
   RabbitMQ management API.
2. **Determine profile** — compare against the configured thresholds, scanning
   highest to lowest; the first match wins.
3. **Check stability** — the target profile must be the same on consecutive
   polls before it counts as a real change.
4. **Debounce** — hold for `DEBOUNCE_SCALE_UP_SECONDS` / `DEBOUNCE_SCALE_DOWN_SECONDS`
   so a spike does not thrash the cluster.
5. **Apply** — in-place resize each pod through the `pods/resize` subresource, or
   patch the `RabbitmqCluster` and let the operator roll pods. See
   [Scale modes](../README.md#scale-modes).

State (the last applied profile and when it was applied) is persisted in a
ConfigMap, so a scaler restart does not reset the debounce window or re-apply a
profile the cluster is already running.

## Startup preflight

Before the loop starts, the scaler asks the API server two questions:

- **Capability** — does discovery advertise the `pods/resize` subresource
  (Kubernetes ≥ 1.33)?
- **Permission** — via `SelfSubjectAccessReview`, may this service account
  `patch` `pods/resize` and `list` `pods`?

`SCALE_MODE=inplace` fails fast when either answer is no, naming the exact verbs
to grant. `SCALE_MODE=auto` logs a warning and degrades to rolling. This exists
because a missing RBAC grant would otherwise surface as a 403 at the first scale
action, potentially hours later, after the cluster had already been flipped to
`updateStrategy: OnDelete`.

## Project structure

```
rmq-vertical-scaler/
├── cmd/rmq-vertical-scaler/   # CLI entry point: `init`, `generate`, `run`
├── internal/
│   ├── config/                # Env-var configuration (profiles, thresholds)
│   ├── metrics/               # RabbitMQ management API client (net/http)
│   ├── scaling/               # Pure scaling + debounce engine (table-tested)
│   ├── k8s/                   # client-go: CRD (dynamic), pods/resize, ConfigMap
│   ├── controller/            # Control loop orchestration
│   └── manifests/             # Embedded YAML template for `generate`
├── charts/                    # Helm chart
├── examples/                  # Configuration templates
├── schema/                    # JSON Schema for configuration validation
├── tests/e2e/                 # kind-based end-to-end suite
├── research/                  # Benchmark methodology + data
├── Dockerfile                 # Multi-stage → distroless/static:nonroot
└── Makefile                   # build / test / image / bench
```
