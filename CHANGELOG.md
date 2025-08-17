# Changelog

All notable changes to this project will be documented in this file.

## [1.0.2] - 2025-08-17

### Fixed
- Resource patching now updates both requests and limits to prevent pod scheduling failures
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