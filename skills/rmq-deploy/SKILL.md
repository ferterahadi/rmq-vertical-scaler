---
name: rmq-deploy
description: Use when an operator wants to install or deploy the rmq-vertical-scaler into a Kubernetes cluster — generating manifests from a config, ensuring the RabbitMQ credentials secret exists, applying with kubectl, and verifying the rollout.
---

# Deploy rmq-vertical-scaler to Kubernetes

## Overview
One binary does two jobs: `generate` emits manifests locally; the same image runs `run` in-cluster. This skill takes an operator from a config file to a verified running deployment.

## When to use
- "Deploy / install the RabbitMQ scaler"
- "Apply this to my cluster"

Prereq: a valid config. If they don't have one, use `rmq-generate-config` first.

## Steps

### 1. Generate manifests
Local binary:
```bash
rmq-vertical-scaler generate --config my-config.json --output my-scaler.yaml
```
No local binary — use the image:
```bash
docker run --rm -v "$PWD:/work" -w /work \
  ferterahadi/rmq-vertical-scaler:2 generate \
  --config my-config.json --output my-scaler.yaml
```
Flags: `-c/--config`, `-n/--namespace`, `-s/--service-name`, `-o/--output`, `--image`, `--scaler-name`. CLI flags override config values.

### 2. Ensure the credentials secret exists (FIRST — the pod won't start without it)
The scaler reads RabbitMQ management creds from secret **`<rmqServiceName>-default-user`** in the target namespace, keys `username` + `password`.
```bash
kubectl get secret <service>-default-user -n <namespace>
```
- Created automatically by the RabbitMQ Cluster Operator — usually already present.
- If missing, create it manually:
```bash
kubectl create secret generic <service>-default-user -n <namespace> \
  --from-literal=username='<user>' --from-literal=password='<pass>'
```

### 3. Review, then apply
Show the operator what gets created before applying: `ServiceAccount`, `Role` (get/patch/update on `rabbitmqclusters`, get on `secrets`, get/create/update/patch on `configmaps`), `RoleBinding`, state `ConfigMap`, `PodDisruptionBudget`, and the `Deployment` (runs `run`).
```bash
kubectl apply -f my-scaler.yaml
```

### 4. Verify the rollout
```bash
kubectl rollout status deployment/rmq-vertical-scaler -n <namespace>
kubectl logs -f deployment/rmq-vertical-scaler -n <namespace>
```
Healthy = logs show it connecting to the RMQ management API and reporting the current profile. Default deployment name is `rmq-vertical-scaler` (or `<scaler-name>` if you set `--scaler-name`).

## Resource naming (defaults)
| Resource | Name |
|---|---|
| Deployment / ServiceAccount / Role / Binding / ConfigMap | `rmq-vertical-scaler`, `-sa`, `-role`, `-binding`, `-config` |
| PodDisruptionBudget | `<service>-pdb` |
| Credentials secret (you supply) | `<service>-default-user` |
| Image | `ferterahadi/rmq-vertical-scaler:latest` (override with `--image`; pin to `:2`) |

## Common mistakes
- Applying before the `<service>-default-user` secret exists → CrashLoop. Check the secret first.
- Namespace in the manifest ≠ where RabbitMQ actually runs → RBAC and DNS won't resolve.
- Forgetting `--image ...:2` and getting `:latest` → pin the tag in production.
- If scaling never happens after a clean deploy, hand off to `rmq-troubleshoot`.
