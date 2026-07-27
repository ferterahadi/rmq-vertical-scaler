# Why Go (footprint, not speed)

**The v2 rewrite is not about speed.** The workload is a low-frequency, I/O-bound
control loop: every few seconds it does one HTTP GET against the RabbitMQ
management API, compares a handful of numbers, and *maybe* issues a Kubernetes
PATCH. It is idle ~99.9% of the time, and the bottleneck is the network call, not
the CPU. Go does not make any scaling decision perceptibly faster.

Go was chosen because, for a Kubernetes operator, it wins on what actually
matters for a tool whose entire pitch is *saving cluster resources*:

1. **Memory footprint** — Node idles ~50–80 MB RSS; the Go binary idles
   ~10–15 MB. A fat runtime sidecar undercuts a resource-saver.
2. **Image and attack surface** — `distroless/static:nonroot` plus one static
   binary: no shell, no package manager, no interpreter. Fewer CVEs, faster
   pulls.
3. **Ecosystem fit** — `client-go` is the official, first-class Kubernetes
   client, and the whole operator ecosystem is Go.
4. **Dependency hygiene** — stdlib `net/http` instead of axios; `client-go` is
   the one real dependency.
5. **Distribution** — a single cross-compiled binary via `go install` or a
   release download. No "do you have Node 18+?".

See [research/benchmark.md](../research/benchmark.md) for the full methodology
and the v1-vs-v2 numbers.
