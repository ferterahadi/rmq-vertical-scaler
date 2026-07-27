# Environment variables

The controller (`rmq-vertical-scaler run`) is configured entirely through the
process environment. The `generate` command and the Helm chart write these
variables into the Deployment for you — you only need this page when hand-rolling
a manifest, debugging a running pod, or upgrading from v1.

Defaults are the v1 (Node.js) defaults, unchanged. An unset **or empty** variable
falls back to its default.

## RabbitMQ connection

| Variable | Default | Meaning |
|-|-|-|
| `RMQ_HOST` | *(none)* | Management API host. Always written by `generate`; empty means the scaler cannot reach RabbitMQ |
| `RMQ_PORT` | *(none)* | Management API port, normally `15672` |
| `RMQ_USER` | `guest` | Management API user |
| `RMQ_PASS` | `guest` | Management API password |

`generate` wires `RMQ_USER` / `RMQ_PASS` to the `{serviceName}-default-user`
secret rather than literal values.

## Kubernetes target

| Variable | Default | Meaning |
|-|-|-|
| `RMQ_SERVICE_NAME` | `rmq` | Name of the `RabbitmqCluster` to patch |
| `NAMESPACE` | `prod` | Namespace holding the cluster and the state ConfigMap |
| `CONFIG_MAP_NAME` | `rmq-config` | ConfigMap the scaler uses to persist state across restarts |

## Timing

| Variable | Default | Meaning |
|-|-|-|
| `CHECK_INTERVAL_SECONDS` | `5` | Seconds between metric polls |
| `DEBOUNCE_SCALE_UP_SECONDS` | `30` | The target profile must hold this long before scaling **up** |
| `DEBOUNCE_SCALE_DOWN_SECONDS` | `120` | The target profile must hold this long before scaling **down** |

## Scale mode

| Variable | Default | Meaning |
|-|-|-|
| `SCALE_MODE` | `auto` | `auto`, `inplace`, or `rolling`. An unrecognised value falls back to `auto` |

See [Scale modes](../README.md#scale-modes) for what each one does.

## Profiles

Profiles are defined by a space-separated list of names, plus two variables per
name.

| Variable | Default | Meaning |
|-|-|-|
| `PROFILE_NAMES` | `LOW MEDIUM HIGH CRITICAL` | Ordered, smallest first. The **first** name is the floor |
| `PROFILE_<NAME>_CPU` | `1000m` | CPU request for that profile |
| `PROFILE_<NAME>_MEMORY` | `2Gi` | Memory request for that profile |
| `QUEUE_THRESHOLD_<NAME>` | `1000` | Total queue depth that selects this profile |
| `RATE_THRESHOLD_<NAME>` | `100` | Messages/second that selects this profile |

The first profile is the floor and takes **no** thresholds — any threshold
variables for it are ignored. Every later profile is selected when its queue
**or** rate threshold is exceeded; the engine scans from the highest profile down
and takes the first match.

If two profiles share a CPU value, the later one wins when the scaler maps an
observed CPU request back to a profile name.

### Worked example

```yaml
env:
  - name: PROFILE_NAMES
    value: "LOW MEDIUM HIGH"
  - name: PROFILE_LOW_CPU
    value: "330m"
  - name: PROFILE_LOW_MEMORY
    value: "2Gi"
  - name: PROFILE_MEDIUM_CPU
    value: "800m"
  - name: PROFILE_MEDIUM_MEMORY
    value: "3Gi"
  - name: QUEUE_THRESHOLD_MEDIUM
    value: "2000"
  - name: RATE_THRESHOLD_MEDIUM
    value: "200"
  - name: PROFILE_HIGH_CPU
    value: "1600m"
  - name: PROFILE_HIGH_MEMORY
    value: "4Gi"
  - name: QUEUE_THRESHOLD_HIGH
    value: "10000"
  - name: RATE_THRESHOLD_HIGH
    value: "1000"
```

Equivalent to this config file, which is the recommended way to author it:

```json
{
  "profiles": {
    "LOW":    { "cpu": "330m",  "memory": "2Gi" },
    "MEDIUM": { "cpu": "800m",  "memory": "3Gi", "queue": 2000,  "rate": 200 },
    "HIGH":   { "cpu": "1600m", "memory": "4Gi", "queue": 10000, "rate": 1000 }
  }
}
```
