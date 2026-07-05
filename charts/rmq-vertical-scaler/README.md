# rmq-vertical-scaler Helm chart

Installs the RabbitMQ Vertical Scaler controller. See the
[project README](../../README.md) for what it does and the safety caveats
(quorum queues, 3+ nodes).

## Install

From a repo checkout:

```bash
helm install rvs charts/rmq-vertical-scaler \
  --namespace production \
  --set serviceName=my-rabbitmq
```

Or straight from a GitHub tag tarball:

```bash
helm install rvs \
  https://github.com/ferterahadi/rmq-vertical-scaler/archive/refs/tags/v2.1.0.tar.gz \
  --namespace production
```

(For the tarball form helm needs the chart at the archive root; if it
complains, download + extract and point at `charts/rmq-vertical-scaler`.)

## Key values

| Value | Default | Meaning |
|-|-|-|
| `serviceName` | `rabbitmq` | RabbitmqCluster/service name (DNS, default secret, PDB selector) |
| `image.tag` | chart appVersion | Controller image tag (pinned, never `latest`) |
| `rmq.host` | `<serviceName>.<ns>.svc.cluster.local` | Management API host override |
| `auth.existingSecret` | `<serviceName>-default-user` | Credentials secret name override |
| `auth.usernameKey` / `auth.passwordKey` | `username` / `password` | Secret key overrides |
| `profiles` | LOW→CRITICAL | **Ordered** list; first entry is the floor (no thresholds) |
| `debounce.scaleUpSeconds` / `scaleDownSeconds` | 30 / 120 | Anti-oscillation delays |
| `checkInterval` | 5 | Metrics poll interval (seconds) |
| `pdb.enabled` / `pdb.minAvailable` | `true` / 2 | PDB on the RabbitMQ pods |
| `serviceAccount.create` / `rbac.create` | `true` | Skip to bring your own |

`values.schema.json` validates your overrides at install time.
