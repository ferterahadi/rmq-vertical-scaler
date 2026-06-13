# syntax=docker/dockerfile:1

# ---- Build stage ----
FROM golang:1.26 AS builder

WORKDIR /src

# Download dependencies first so they are cached across source changes.
COPY go.mod go.sum ./
RUN go mod download

# Build a fully static binary (no libc), stripped of debug info.
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/rmq-vertical-scaler ./cmd/rmq-vertical-scaler

# ---- Runtime stage ----
# distroless/static:nonroot ships CA certs, tzdata, and a non-root user, with no
# shell or package manager — a ~2 MB base. Go handles SIGTERM/SIGINT natively as
# PID 1, so no dumb-init is required.
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /out/rmq-vertical-scaler /rmq-vertical-scaler

USER nonroot:nonroot

ENTRYPOINT ["/rmq-vertical-scaler"]
# Default to the in-cluster control loop; override with "generate" for the CLI.
CMD ["run"]
