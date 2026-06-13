# Local end-to-end testing (minikube)

How to prove v2 actually scales a real `RabbitmqCluster` on your machine. The
scaler only does in-cluster auth, so it must run **inside** a cluster — hence
minikube. The repo ships the fixtures you need under `tests/k8s/`.

## 0. Prerequisites (macOS, one-time)

No container runtime is assumed. The simplest no-Docker-Desktop path:

```bash
brew install minikube kubernetes-cli
brew install colima docker            # colima = lightweight Docker runtime
colima start --cpu 4 --memory 8
minikube start --driver=docker
```

Images are built **inside** the cluster with `minikube image build` — you never
need a host registry.

## 1. Install the RabbitMQ Cluster Operator

The scaler patches the operator's `RabbitmqCluster` CRD, so the operator must be present:

```bash
kubectl apply -f https://github.com/rabbitmq/cluster-operator/releases/latest/download/cluster-operator.yml
kubectl -n rabbitmq-system rollout status deploy/rabbitmq-cluster-operator
```

## 2. Deploy a RabbitMQ cluster

Use the shipped fixture (`namespace: default`, `cpu: 330m` = the LOW floor,
management plugin on, NodePort 30672):

```bash
kubectl apply -f tests/k8s/rabbitmq-cluster.yaml
# The operator names the StatefulSet "<cluster>-server":
kubectl rollout status sts/rabbitmq-server   # wait for the nodes
```

> On a small VM (e.g. a 5 GB minikube) the 3-replica/2Gi fixture won't schedule.
> Use 1 replica with small requests for a smoke test — see the values in
> `RMQ_VERTICAL_SCALING` notes or just edit a copy of the fixture.

The operator creates the `rabbitmq-default-user` secret the scaler reads.

## 3. Build the scaler image into the cluster

```bash
minikube image build -t rmq-vertical-scaler:dev .
```

## 4. Generate and deploy the scaler

Point it at the `rabbitmq` cluster in `default`, using the locally built image
and a fast loop so you don't wait around:

```bash
go run ./cmd/rmq-vertical-scaler generate \
  --config examples/basic-config.json \
  --namespace default \
  --service-name rabbitmq \
  --image rmq-vertical-scaler:dev \
  --output /tmp/scaler.yaml

# For a quick demo, shrink the debounce windows before applying (optional):
#   in /tmp/scaler.yaml set DEBOUNCE_SCALE_UP_SECONDS=10, DEBOUNCE_SCALE_DOWN_SECONDS=20
kubectl apply -f /tmp/scaler.yaml

# IMPORTANT: minikube can't pull "rmq-vertical-scaler:dev" from a registry.
kubectl set image deploy/rmq-vertical-scaler scaler=rmq-vertical-scaler:dev
kubectl patch deploy/rmq-vertical-scaler --type=json \
  -p='[{"op":"replace","path":"/spec/template/spec/containers/0/imagePullPolicy","value":"IfNotPresent"}]'

kubectl logs -f deploy/rmq-vertical-scaler   # watch it connect + evaluate
```

## 5. Generate load

You need the queue depth (or publish rate) to cross a profile threshold
(basic-config: MEDIUM at 2000 msgs / 200 msg/s).

**Option A — official perf tool (quick, no extra deps):**

```bash
kubectl run perf --rm -it --restart=Never \
  --image=pivotalrabbitmq/perf-test -- \
  --uri "amqp://$(kubectl get secret rabbitmq-default-user -o jsonpath='{.data.username}' | base64 -d):$(kubectl get secret rabbitmq-default-user -o jsonpath='{.data.password}' | base64 -d)@rabbitmq.default.svc.cluster.local:5672" \
  --queue load-test --producers 4 --consumers 0 --rate 500 --pmessages 50000
```

Producers with **no consumers** build a backlog fast → drives MEDIUM/HIGH.

**Option B — mighty-cns as a realistic producer:**

`~/Documents/mighty-cns` runs `rabbitmq:3-management` with `gateway` /
`email-router` services. Port-forward AMQP from minikube and point mighty-cns at it:

```bash
kubectl port-forward svc/rabbitmq 5672:5672 &
# in mighty-cns: set the broker URL to amqp://<user>:<pass>@localhost:5672 and
# run its message-producing workload (see mighty-cns/k8s/RMQ_VERTICAL_SCALING.md)
```

This exercises the exact queues/topology mighty-cns uses in production.

## 6. Observe scaling

```bash
# Watch the cluster's resource requests change as load rises/falls
kubectl get rabbitmqcluster rabbitmq -o jsonpath='{.spec.resources.requests}{"\n"}' -w

# Or the shipped monitor (pod allocations + cluster setting + scaler decision)
./tests/k8s/monitor-scaling.sh

# The stability/debounce state
kubectl get configmap rmq-vertical-scaler-config -o jsonpath='{.data}{"\n"}'
```

**Expected:** backlog crosses MEDIUM → after the scale-up debounce the scaler
JSON-patches `cpu`/`memory` up (operator rolls the StatefulSet) → stop the
producers, let it drain → after the longer scale-down debounce it patches back
to LOW.

## 7. (Optional) v1-vs-v2 parity on a live cluster

Run the same load+config against each version sequentially (or two namespaces)
and diff the decision log lines (`📊 Queue Depth …`, `⚙️ Applying …`). v1 image:
`ferterahadi/rmq-vertical-scaler:1.0.2`; v2: your `:dev` build. Decisions should
be identical for identical metrics. Capture the footprint numbers here too
(`scripts/benchmark.sh`).

## 8. Teardown

```bash
kubectl delete -f /tmp/scaler.yaml
kubectl delete -f tests/k8s/rabbitmq-cluster.yaml
minikube delete         # and: colima stop
```
