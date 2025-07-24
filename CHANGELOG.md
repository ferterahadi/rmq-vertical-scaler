# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial release of RMQ Vertical Scaler
- Modular architecture with separate components
- Professional CLI interface with commander.js
- Comprehensive configuration management
- Docker support with multi-stage builds
- Built-in Kubernetes deployment commands
- CI/CD workflows for automated testing and releases

## [1.0.0] - 2024-01-01

### Added
- **Core Features**
  - Automatic vertical scaling of RabbitMQ clusters in Kubernetes
  - Support for multiple scaling profiles (LOW, MEDIUM, HIGH, CRITICAL)
  - Configurable thresholds based on queue depth and message rates
  - Debounced scaling to prevent oscillation
  - Stability tracking with ConfigMap persistence

- **Architecture**
  - `MetricsCollector`: RabbitMQ API integration for metrics gathering
  - `ScalingEngine`: Intelligent scaling decision logic
  - `KubernetesClient`: Kubernetes API operations
  - `ConfigManager`: Centralized configuration management
  - `RabbitMQVerticalScaler`: Main orchestrator class

- **CLI & API**
  - Command-line interface with comprehensive options
  - Programmatic API for integration with other tools
  - Configuration via environment variables or JSON files
  - Dry-run mode for testing and validation

- **Deployment Options**
  - npm package for global installation
  - Docker image with optimized multi-stage build
  - Kubernetes manifests with RBAC configuration
  - Built-in CLI deployment commands

- **Developer Experience**
  - Comprehensive documentation and examples
  - Professional project structure
  - ESLint configuration with Standard style
  - Automated testing with GitHub Actions
  - Docker Hub integration for releases

- **Monitoring & Observability**
  - Structured logging with scaling events
  - Health check endpoints
  - Error handling and recovery mechanisms
  - Debug mode for troubleshooting

### Security
- Non-root Docker container execution
- Minimal Kubernetes RBAC permissions
- Secure handling of RabbitMQ credentials
- No secrets logged or exposed

### Documentation
- Comprehensive README with usage examples
- Contributing guidelines and development setup
- Architecture documentation with component diagrams
- Configuration reference and best practices
- Deployment guides for various environments

---

## Release Notes

### Version 1.0.0 Highlights

This is the initial professional release of RMQ Vertical Scaler, transforming a simple scaling script into a production-ready, enterprise-grade tool.

**Key Improvements:**
- **Modular Design**: Separated concerns into focused, testable components
- **Professional CLI**: Full-featured command-line interface with deployment commands
- **Multiple Deployment Options**: npm, Docker, and Kubernetes support
- **Comprehensive Documentation**: Production-ready docs and examples
- **CI/CD Pipeline**: Automated testing, building, and publishing
- **Security Hardening**: Non-root containers, minimal permissions, secure credential handling

**Migration from Previous Versions:**
- The API has been completely redesigned for better usability
- Configuration format has changed to support more flexible setups
- Docker image now runs as non-root user
- Built-in deployment commands replace external scripts

**Breaking Changes:**
- Previous single-file script has been replaced with modular architecture
- Environment variable names have been standardized
- Docker image entry point has changed

**Upgrade Path:**
1. Review new configuration format in README
2. Update environment variables to match new naming
3. Test with `--dry-run` mode before production deployment
4. Update Kubernetes RBAC permissions if using custom setup

For detailed upgrade instructions, see the [Migration Guide](docs/MIGRATION.md).