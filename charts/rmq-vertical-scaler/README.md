# rmq-vertical-scaler Helm chart

Installs the RabbitMQ Vertical Scaler controller. See the
[project README](../../README.md) for what it does and the safety caveats
(quorum queues, 3+ nodes).

## Install

From a repo checkout:

```bash
helm install rvs charts/rmq-vertical-scaler \
  --namespace production --create-namespace \
  --set serviceName=my-rabbitmq
```

Or from a GitHub tag tarball (extract first — Helm needs the chart directory):

```bash
curl -sL https://github.com/ferterahadi/rmq-vertical-scaler/archive/refs/tags/v2.1.0.tar.gz | tar xz
helm install rvs rmq-vertical-scaler-2.1.0/charts/rmq-vertical-scaler \
  --namespace production --create-namespace
```

## Key values

| Value | Default | Meaning |
|-|-|-|
| `serviceName` | `rabbitmq` | RabbitmqCluster/service name (DNS, default secret, PDB selector) |
| `image.tag` | chart appVersion | Controller image tag (pinned, never `latest`). Default `pullPolicy: IfNotPresent` suits pinned tags; if overriding to a mutable tag (e.g. `"2"`), set `image.pullPolicy: Always` |
| `rmq.host` | `<serviceName>.<ns>.svc.cluster.local` | Management API host override |
| `auth.existingSecret` | `<serviceName>-default-user` | Credentials secret name override |
| `auth.usernameKey` / `auth.passwordKey` | `username` / `password` | Secret key overrides |
| `profiles` | LOW→CRITICAL | **Ordered** list; first entry is the floor (no thresholds) |
| `debounce.scaleUpSeconds` / `scaleDownSeconds` | 30 / 120 | Anti-oscillation delays |
| `checkInterval` | 5 | Metrics poll interval (seconds) |
| `scaling.mode` | `auto` | `auto`: in-place pod resize when the cluster supports it (K8s ≥ 1.33), rolling restart otherwise; `inplace`: always resize in place; `rolling`: always patch the CR only (v2.0.0 behaviour) |
| `pdb.enabled` / `pdb.minAvailable` | `true` / 2 | PDB on the RabbitMQ pods |
| `serviceAccount.create` / `rbac.create` | `true` | Skip to bring your own |

`values.schema.json` validates your overrides at install time.
