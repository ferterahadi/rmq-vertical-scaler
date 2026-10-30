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

1. **Collect metrics** — fetch queue lengths and the cluster publish rate from the
   RabbitMQ management API.
2. **Determine profile** — compare against the configured thresholds, scanning
   highest to lowest; the first match wins.
3. **Check stability** — the target profile must be the same on consecutive
   polls before it counts as a real change.
4. **Debounce** — hold for `DEBOUNCE_SCALE_UP_SECONDS` / `DEBOUNCE_SCALE_DOWN_SECONDS`
   so a spike does not thrash the cluster.
5. **Apply** — in-place resize each pod through the `pods/resize` subresource, or
   patch the `RabbitmqCluster` and let the operator roll pods. See
   [Scale modes](usage.md#scale-modes).

The tracked recommendation and its stability timestamp are persisted in a
ConfigMap, so the current legacy controller can resume its debounce tracking
after a restart. This is not a record of confirmed effective resources. Its
current-profile lookup reads the desired CPU request from `RabbitmqCluster`.

## Startup preflight

Before the loop starts, the scaler asks the API server two questions:

- **Capability** — does discovery advertise the `pods/resize` subresource
  (Kubernetes ≥ 1.33)?
- **Permission** — via `SelfSubjectAccessReview`, may this service account
  `patch` `pods/resize` and `list` `pods`?

`SCALE_MODE=inplace` fails fast if permissions are denied or cannot be verified,
naming missing verbs when available. If discovery does not advertise the resize
subresource, explicit `inplace` logs a warning and proceeds; later resizes can
fail. `SCALE_MODE=auto` degrades to rolling when capability or permission is
unavailable. This exists
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

## Opt-in version-2 architecture

`internal/policy` validates and normalizes versioned policies; `internal/v2`
separates broker/resource observations, pure recommendations, and durable resize
reconciliation. The existing modules above remain the legacy path.

The new controller reads effective resources, owns a separate status ConfigMap,
and persists intent before resource mutations. Strict mode blocks unsupported
memory-limit transitions; explicitly configured rolling is a separate path.
The [policy guide](v2.3-policy.md) explains operation and migration; the
[feasibility report](v2.3-feasibility.md) distinguishes measured constraints from
release acceptance. No broker exec permission is granted.
