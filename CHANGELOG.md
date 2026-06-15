# Changelog

All notable changes to this project will be documented in this file.

## [2.0.0] - 2026-06-13

### Changed
- **Rewritten in Go.** The runtime controller and the manifest generator are now a single static Go binary shipped in a `distroless/static:nonroot` image — smaller idle memory footprint (~10–15 MB RSS vs ~50–80 MB), much smaller attack surface, and no interpreted runtime. **No change to scaling behaviour.**
- Kubernetes integration now uses the official `client-go` (dynamic client for the `RabbitmqCluster` CRD, typed CoreV1 for the state ConfigMap).
- Container signal handling is now native to Go; `dumb-init` is no longer needed.
- CI now builds and publishes a multi-arch image (`linux/amd64`, `linux/arm64`) and attaches prebuilt release binaries.

### Compatibility
- **Behaviour parity** with v1: identical scaling decisions for the same metrics + config (enforced by tests).
- **Env-var contract unchanged**: a v1-generated Deployment drives v2 — swap the image tag to `:2`.
- **Generated manifests** are byte-for-byte compatible with v1 (golden-tested).
- **Config format unchanged**: existing `examples/*.json` work as-is.
- The Node.js v1 remains available via `v1.x` git tags and `:1.x` Docker images / npm `1.x`.

## [1.0.2] - 2025-08-17

### Fixed
- Added namespace environment variable to scaler deployment

### Changed
- Reorganized test files from k8s-test to tests/k8s directory
- Monitor script now displays individual pod CPU allocations for better visibility
- Test dependencies moved to devDependencies

## [1.0.1] - 2025-08-17

### Fixed
- RMQ_HOST environment variable generation for cluster deployments
- Added comprehensive unit tests for configuration

## [1.0.0] - 2025-08-17

### Added
- Initial release of RabbitMQ Vertical Scaler
- Automatic vertical scaling based on queue depth and message rates
- Support for multiple scaling profiles (LOW, MEDIUM, HIGH, CRITICAL)
- Kubernetes deployment with RBAC configuration
- Docker support
- CLI tool for generating deployment manifests