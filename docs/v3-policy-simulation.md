# Evaluate a four-profile policy

Use the four-profile example in the [README](../README.md#configuration-example)
or download its [complete JSON policy](examples/v3-four-profile.json).
The sizes and thresholds are illustrative; adapt them to the broker workload and
platform minimum requests. This is a decision walkthrough, not measured performance.

## Profile order and inputs

`LOW → MEDIUM → HIGH → CRITICAL` is the resource order. Trigger array order does
not establish priority. Each trigger recommends a profile; the highest wins.
CPU and memory requests change together on every broker.

| Profile | CPU request | Memory request | Deepest queue threshold | Cluster ingress threshold |
|---|---:|---:|---:|---:|
| LOW | 500m | 1Gi | Baseline | Baseline |
| MEDIUM | 1000m | 2Gi | > 2,000 | > 200/s |
| HIGH | 2000m | 4Gi | > 10,000 | > 1,000/s |
| CRITICAL | 4000m | 8Gi | > 50,000 | > 5,000/s |

All profiles retain fixed limits of 5 CPU and 8Gi memory. CPU utilization targets
60%; memory targets 75%. Both use the busiest broker, which may be a different
broker for CPU and memory. The example's queue aggregation is `max`, while ingress
is the cluster-wide publish rate.

## Calculate each recommendation

1. **Queue and ingress:** choose the highest profile whose threshold is strictly
   exceeded. A depth of exactly 10,000 still recommends MEDIUM; 10,001 recommends HIGH.
2. **CPU:** project measured absolute core usage onto each profile's CPU request.
   Choose the smallest profile that would keep utilization at or below 60%.
3. **Memory:** do the same with working-set bytes and each memory request, at 75%.
4. **Combine:** choose the highest recommendation in `profileOrder`. Ties do not
   need a tiebreaker: both signals recommend the same resource tuple.
5. **Check permission to act:** `Off` only recommends. `Auto` applies eligible
   changes after stabilization and safety checks, and then verifies convergence.

For example, 400m CPU usage is 80% of LOW's 500m request but 40% of MEDIUM's 1000m.
The CPU recommendation is MEDIUM. It does not grow again merely because the
previous observation was 80%; it always projects absolute usage onto candidate requests.

## Worked simulations

These cases assume complete, fresh observations from healthy brokers with uniform
current resources and no pending operation. Ingress is 10/s in every row.

| Case | Current | Queue depth | CPU usage | Memory usage | Queue / CPU / memory recommendation | Outcome in Auto |
|---|---|---:|---:|---:|---|---|
| CPU drives | LOW | 100 | 400m | 256Mi | LOW / MEDIUM / LOW | MEDIUM after 30s sustained demand |
| Queue drives | LOW | 12,000 | 400m | 256Mi | HIGH / MEDIUM / LOW | HIGH after 30s |
| Memory drives | LOW | 100 | 100m | 3,500Mi | LOW / LOW / CRITICAL | CRITICAL after 30s |
| Tie | LOW | 100 | 800m | 1,800Mi | LOW / HIGH / HIGH | HIGH after 30s |
| Brief spike | LOW | 100 | 800m | 256Mi | LOW / HIGH / LOW | At 15s, hold LOW; stabilization is incomplete |
| Demand drops | CRITICAL | 100 | 100m | 256Mi | LOW / LOW / LOW | After a full safe 300s, decrease to HIGH only |
| Above the ceiling | LOW | 100 | 2,800m | 7,000Mi | LOW / CRITICAL / CRITICAL | CRITICAL after 30s, but utilization targets remain unmet |

The demand-drop case continues **CRITICAL → HIGH → MEDIUM → LOW**, with a new full
300-second safety window for each step. Those are minimum observation windows,
not a promise of exact completion time: reconciliation, capacity, stale samples,
restarts and changing demand can delay or reset them.

Missing CPU or memory signal data blocks decreases when those triggers are enabled.
Fresh broker memory is also required for decreases even when the memory trigger
is disabled. A valid queue signal can still justify an increase without CPU/memory
metrics. The simulator's elapsed-time field assumes steady inputs and continuously
fresh, distinct samples; it does not model pending operations or broker/node faults.

The memory decrease guard requires at least 20% headroom beneath the target memory
**limit**, separate from the utilization trigger's percentage of its **request**.
Kubernetes requests do not change RabbitMQ's separately configured memory watermark.
