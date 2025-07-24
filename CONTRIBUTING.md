# Contributing to RMQ Vertical Scaler

Thank you for your interest in contributing to RMQ Vertical Scaler! This document provides guidelines and information for contributors.

## 🤝 Code of Conduct

This project and everyone participating in it is governed by our [Code of Conduct](CODE_OF_CONDUCT.md). By participating, you are expected to uphold this code.

## 🚀 Getting Started

### Prerequisites

- Node.js ≥18.0.0
- npm ≥8.0.0
- Docker (for containerized testing)
- Kubernetes cluster access (for integration testing)

### Development Setup

1. **Fork and Clone**
   ```bash
   git clone https://github.com/yourusername/rmq-vertical-scaler.git
   cd rmq-vertical-scaler
   ```

2. **Install Dependencies**
   ```bash
   npm install
   ```

3. **Run Tests**
   ```bash
   npm test
   ```

4. **Run Linting**
   ```bash
   npm run lint
   ```

5. **Start Development Mode**
   ```bash
   npm run dev
   ```

## 🏗️ Project Structure

```
rmq-vertical-scaler/
├── lib/                    # Core library modules
│   ├── RabbitMQVerticalScaler.js  # Main orchestrator class
│   ├── MetricsCollector.js        # RabbitMQ metrics collection
│   ├── ScalingEngine.js           # Scaling logic and decisions
│   ├── KubernetesClient.js        # Kubernetes API interactions
│   ├── ConfigManager.js           # Configuration management
│   └── index.js                   # Library exports
├── bin/                    # CLI executable
│   └── rmq-vertical-scaler        # Main CLI entry point
├── tests/                  # Test suites
│   ├── unit/              # Unit tests
│   ├── integration/       # Integration tests
│   └── fixtures/          # Test data and fixtures
├── examples/              # Usage examples
├── docs/                  # Documentation
├── helm/                  # Helm chart
├── .github/               # CI/CD workflows
└── deploy/                # Deployment scripts
```

## 📝 How to Contribute

### 1. Reporting Issues

Before creating an issue, please:

- **Search existing issues** to avoid duplicates
- **Use the issue templates** provided
- **Include version information** and environment details
- **Provide reproducible steps** when reporting bugs

### 2. Feature Requests

For new features:

- **Open a discussion** first to validate the idea
- **Describe the use case** and benefits
- **Consider backwards compatibility**
- **Provide implementation suggestions** if possible

### 3. Pull Requests

#### Before You Start

- **Create an issue** first to discuss the change
- **Fork the repository** and create a feature branch
- **Follow the existing code style** and conventions
- **Write tests** for new functionality
- **Update documentation** as needed

#### Pull Request Process

1. **Create a feature branch**
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. **Make your changes**
   - Follow the [coding standards](#coding-standards)
   - Add tests for new functionality
   - Update documentation

3. **Run tests and linting**
   ```bash
   npm test
   npm run lint
   ```

4. **Commit your changes**
   ```bash
   git commit -m "feat: add new scaling algorithm"
   ```

5. **Push to your fork**
   ```bash
   git push origin feature/your-feature-name
   ```

6. **Open a Pull Request**
   - Use the PR template
   - Link to related issues
   - Describe the changes made
   - Include testing instructions

## 🎯 Coding Standards

### JavaScript Style

We use ESLint with the Standard config:

```bash
npm run lint          # Check for issues
npm run lint:fix      # Auto-fix issues
```

### Code Organization

- **Modular Design**: Keep classes focused and single-purpose
- **Error Handling**: Always handle errors gracefully
- **Logging**: Use structured logging with appropriate levels
- **Configuration**: Make behavior configurable via environment variables
- **Documentation**: Document public APIs and complex logic

### Naming Conventions

- **Classes**: PascalCase (`MetricsCollector`)
- **Methods**: camelCase (`getQueueMetrics`)
- **Constants**: UPPER_SNAKE_CASE (`DEFAULT_TIMEOUT`)
- **Files**: camelCase (`MetricsCollector.js`)

### Testing

- **Unit Tests**: Test individual methods and classes
- **Integration Tests**: Test component interactions
- **Mocking**: Mock external dependencies (Kubernetes API, RabbitMQ API)
- **Coverage**: Aim for >90% test coverage

Example test structure:
```javascript
import { describe, it, before, after } from 'node:test';
import assert from 'node:assert';
import { MetricsCollector } from '../lib/MetricsCollector.js';

describe('MetricsCollector', () => {
  let collector;

  before(() => {
    collector = new MetricsCollector({
      rmqHost: 'localhost',
      rmqPort: 15672,
      rmqUser: 'guest',
      rmqPass: 'guest'
    });
  });

  it('should extract metrics correctly', async () => {
    const mockData = { /* mock API response */ };
    const metrics = collector.extractMetrics(mockData);
    
    assert.strictEqual(metrics.totalMessages, 100);
    assert.strictEqual(metrics.publishRate, 10.5);
  });
});
```

## 🔄 Development Workflow

### Git Workflow

We use a simplified Git Flow:

1. **main**: Production-ready code
2. **develop**: Integration branch for features
3. **feature/***: Feature development branches
4. **hotfix/***: Critical bug fixes

### Commit Messages

We follow [Conventional Commits](https://www.conventionalcommits.org/):

```bash
feat: add new scaling profile
fix: handle connection timeouts gracefully
docs: update configuration examples
test: add integration tests for scaling engine
chore: update dependencies
```

Types:
- `feat`: New features
- `fix`: Bug fixes
- `docs`: Documentation changes
- `test`: Test additions/changes
- `chore`: Maintenance tasks
- `refactor`: Code refactoring
- `perf`: Performance improvements

### Release Process

1. **Update version** in package.json
2. **Update CHANGELOG.md** with release notes
3. **Create release PR** to main branch
4. **Tag release** after merge
5. **Publish to npm** (automated)
6. **Build and push Docker image** (automated)

## 🧪 Testing

### Running Tests

```bash
# Run all tests
npm test

# Run with coverage
npm run test:coverage

# Run specific test file
npm test tests/unit/MetricsCollector.test.js

# Run in watch mode
npm run test:watch
```

### Test Environment Setup

For integration tests, you'll need:

```bash
# Start test RabbitMQ
docker run -d --name rabbitmq-test \
  -p 15672:15672 -p 5672:5672 \
  rabbitmq:3-management

# Set test environment variables
export RMQ_HOST=localhost
export RMQ_PORT=15672
export RMQ_USER=guest
export RMQ_PASS=guest
```

### Writing Tests

- **Test file naming**: `*.test.js`
- **Test structure**: Describe blocks for each class/method
- **Assertions**: Use Node.js built-in `assert` module
- **Mocking**: Mock external APIs and Kubernetes calls
- **Cleanup**: Always clean up resources in `after` hooks

## 📚 Documentation

### Code Documentation

- **JSDoc comments** for all public methods
- **README updates** for new features
- **Configuration examples** for new options
- **Migration guides** for breaking changes

### Documentation Style

```javascript
/**
 * Collects metrics from RabbitMQ Management API
 * @param {Object} options - Configuration options
 * @param {string} options.rmqHost - RabbitMQ host
 * @param {number} options.rmqPort - RabbitMQ port
 * @returns {Promise<Object>} Queue metrics
 * @throws {Error} When connection fails
 */
async getQueueMetrics(options) {
  // Implementation
}
```

## 🚀 Performance Considerations

- **Memory Usage**: Be mindful of memory leaks in long-running processes
- **API Rate Limits**: Implement backoff strategies for external APIs
- **Error Recovery**: Design for resilience and automatic recovery
- **Resource Cleanup**: Always clean up resources (timers, connections)

## 🐛 Debugging

### Local Development

```bash
# Enable debug logging
export DEBUG=rmq-vertical-scaler:*
npm start

# Run with Node.js inspector
node --inspect bin/rmq-vertical-scaler

# Use dry-run mode for testing
npm start -- --dry-run
```

### Common Issues

- **Connection Errors**: Check RabbitMQ accessibility and credentials
- **Permission Errors**: Verify Kubernetes RBAC configuration
- **Scaling Not Triggered**: Check threshold configurations and stability delays

## 📋 Review Checklist

Before submitting a PR, ensure:

- [ ] Code follows project standards
- [ ] Tests pass and coverage is maintained
- [ ] Documentation is updated
- [ ] Commit messages follow conventions
- [ ] No breaking changes (or properly documented)
- [ ] Error handling is comprehensive
- [ ] Configuration is validated
- [ ] Logging is appropriate

## 🎯 Areas for Contribution

We welcome contributions in these areas:

### High Priority
- **Additional Metrics**: Support for more RabbitMQ metrics
- **Scaling Algorithms**: Alternative scaling strategies
- **Monitoring Integration**: Prometheus metrics, health checks
- **Documentation**: Examples, tutorials, best practices

### Medium Priority
- **Multi-Cluster Support**: Scaling across multiple RabbitMQ clusters
- **Custom Resources**: Support for custom Kubernetes resources
- **Webhook Support**: Integration with external systems
- **Dashboard**: Web UI for monitoring and configuration

### Lower Priority
- **Plugin System**: Extensible architecture for custom plugins
- **Alternative APIs**: Support for other message brokers
- **Advanced Scheduling**: Time-based scaling policies

## 💬 Community

- **GitHub Discussions**: For questions and general discussion
- **GitHub Issues**: For bug reports and feature requests
- **Pull Requests**: For code contributions

## 🔒 Security

If you discover a security vulnerability, please email security@rmq-vertical-scaler.dev instead of opening a public issue.

## 📄 License

By contributing, you agree that your contributions will be licensed under the MIT License.