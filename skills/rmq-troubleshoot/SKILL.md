---
name: rmq-troubleshoot
description: Use when a deployed rmq-vertical-scaler isn't behaving — it's not scaling RabbitMQ up or down, the pod is crashing or CrashLooping, it can't reach the RabbitMQ management API, or it logs RBAC/permission/secret errors.
---

# Troubleshoot rmq-vertical-scaler

## Overview
The scaler is a control loop: every `CHECK_INTERVAL_SECONDS` it GETs the RabbitMQ management API, picks a profile from the metrics, and (after debounce) JSON-PATCHes the `RabbitmqCluster` CR. Failures cluster into: can't start, can't read metrics, can't patch, or won't scale by design. Diagnose in that order.

## When to use
- "Scaler isn't scaling" / "stuck on one profile"
- Pod `CrashLoopBackOff`, `Error`, or restarts
- Logs mention `secret`, `forbidden`, `connection refused`, `401`, `RBAC`

## Diagnose in order

### 1. Is the pod up?
```bash
kubectl get pods -n <ns> -l app.kubernetes.io/name=<service>
kubectl logs deployment/rmq-vertical-scaler -n <ns> --tail=100
kubectl describe pod <pod> -n <ns>   # events: image pull, secret mount, OOM
```
- `CreateContainerConfigError` / secret not found → the **`<service>-default-user`** secret is missing or in the wrong namespace. See `rmq-deploy` step 2.
- `OOMKilled` → floor profile memory too low.

### 2. Can it reach RabbitMQ?
Logs show `connection refused` / timeout / DNS error:
- `RMQ_HOST` must be the full service DNS `<service>.<ns>.svc.cluster.local`, `RMQ_PORT` the management port (`15672`).
- Test from inside the cluster:
```bash
kubectl exec -it deploy/rmq-vertical-scaler -n <ns> -- sh   # distroless: no shell — use a debug pod instead
kubectl run curl --rm -it --image=curlimages/curl -n <ns> -- \
  curl -u <user>:<pass> http://<service>.<ns>.svc.cluster.local:15672/api/overview
```
- `401` → wrong creds in the `<service>-default-user` secret.

### 3. Can it patch the cluster? (RBAC)
Logs show `forbidden` / `cannot patch resource "rabbitmqclusters"`:
```bash
kubectl auth can-i patch rabbitmqclusters \
  --as=system:serviceaccount:<ns>:rmq-vertical-scaler-sa -n <ns>
```
Required verbs: `rabbitmqclusters` get/patch/update, `secrets` get, `configmaps` get/create/update/patch. If `no`, the Role/RoleBinding didn't apply or the SA namespace is wrong — re-apply the generated manifest.

### 4. It runs fine but won't scale
Often correct-by-design, not a bug:
- **Debounce**: scale-up waits `DEBOUNCE_SCALE_UP_SECONDS`, scale-down waits `DEBOUNCE_SCALE_DOWN_SECONDS` (default 120). The target profile must stay stable that long. Brief spikes won't trigger.
- **Thresholds**: metrics never crossed the next profile's `queue`/`rate`. Compare live `/api/overview` numbers against the config.
- **Floor**: first profile has no thresholds — it's the minimum; it never scales below that.
- **State**: current profile is tracked in the `rmq-vertical-scaler-config` ConfigMap:
```bash
kubectl get configmap rmq-vertical-scaler-config -n <ns> -o yaml
```

## Env contract (what the Deployment sets)
`RMQ_HOST` `RMQ_PORT` `RMQ_USER` `RMQ_PASS` `RMQ_SERVICE_NAME` `NAMESPACE` `CONFIG_MAP_NAME` `PROFILE_NAMES` `PROFILE_<NAME>_CPU` `PROFILE_<NAME>_MEMORY` `QUEUE_THRESHOLD_<NAME>` `RATE_THRESHOLD_<NAME>` `DEBOUNCE_SCALE_UP_SECONDS` `DEBOUNCE_SCALE_DOWN_SECONDS` `CHECK_INTERVAL_SECONDS`. Inspect live: `kubectl set env deploy/rmq-vertical-scaler -n <ns> --list`.

## Common mistakes
- Trying `kubectl exec` for a shell — the image is distroless (no shell). Use a separate debug pod.
- Assuming "not scaling" = broken when it's debounce or thresholds working as configured.
- Editing env on the live Deployment instead of the config + regenerate — drift from the source of truth.
