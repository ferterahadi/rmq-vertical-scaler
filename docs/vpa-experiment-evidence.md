# Why a separate RabbitMQ scaling policy

The motivating requirement was a load-driven **100m → 800m → 100m CPU-request
cycle within 15 minutes**. Two GKE Standard experiments on 26 October 2026
informed the design. These historical observations are separate from the
[final 2.3 controller acceptance](v2.3-acceptance-results.md).

## Native VPA observation

A fresh broker and native Vertical Pod Autoscaler (VPA) ran with a fixed CPU-only
policy: 100m minimum, 800m maximum, a fixed 1-CPU limit, 512Mi memory request and
1Gi memory limit. Five minutes of actual AMQP load preceded approximately ten
minutes of cooldown. No custom recommender or manual resource changes operated
inside the measurement window.

| Observation | Result |
|---|---|
| Measurement window | 26 October 2026, 13:17:07–13:32:07 Singapore time |
| Applied CPU request | 100m in all 171 samples |
| Native recommendation | Reached the 800m policy ceiling |
| Actual CPU peak | 989.4m, near the fixed 1-CPU limit |
| Resize audit | Zero Pod resize calls in the window |
| Broker identity | Same Pod/container, zero restarts; ready in sampled observations |

Native VPA did not complete the requested cycle within that window. VPA supports
StatefulSets; this result establishes a bounded responsiveness miss, not an
inability to resize them or a general assessment of longer-term rightsizing.

## Separate custom recommender observation

A short-window custom recommender using GKE's VPA updater completed the following
applied CPU-request cycle, with the same Pod/container and zero restarts:

| Singapore time | Applied CPU request |
|---|---:|
| 12:39:53 | 100m |
| 12:42:26 | 800m |
| 12:47:32 | 100m |

This demonstrates the updater path with a custom policy. The recommender used
recent CPU samples, 25% headroom and bounds chosen for the toy cycle; it was not
the native recommender or this repository's controller. CPU limits and memory
stayed fixed. The workloads and observation windows differed, so these results
are not a controlled performance comparison.

## Evidence boundary

This summary preserves the historical native-cycle and custom-cycle reports from
the local GKE experiment. Raw cloud captures and experimental scripts are outside
this PR's validated 2.3 scope. The experiment infrastructure was subsequently
removed. Neither experiment ran this controller or the RabbitMQ Cluster Operator,
or established throughput gains, lossless delivery or cost savings.

The separate 2.3 acceptance uses three Operator-managed brokers and names its own
source, image, workload and timing evidence. Its baseline group named `native`
runs this project's controller; it is not a new native GKE VPA comparison.
