 # RabbitMQ Vertical Scaler

A Node.js application that automatically **vertically scales** RabbitMQ cluster resources (CPU/Memory) based on queue metrics and message rates in Google Kubernetes Engine (GKE).

## Quick Start

**For users wanting to deploy the scaler:**
```bash
cd deploy
./generate.sh
kubectl apply -f *-scaler.yaml
```

See [`deploy/README.md`](deploy/README.md) for complete documentation.

## Project Structure

```
├── deploy/                 # 🚀 User-facing deployment files
│   ├── generate.sh        # Script to generate deployment YAML
│   ├── README.md          # Complete user documentation
│   └── templates/         # Template files (if any)
├── src/                   # 💻 Source code and build files
│   ├── scale.js          # Main application code
│   ├── package.json      # Dependencies
│   ├── webpack.config.js # Build configuration
│   ├── yarn.lock         # Lock file
│   ├── build.sh          # Build and push script
│   └── Dockerfile        # Container build
└── dist/                  # 📦 Build artifacts (generated)
```

## License

MIT License - See LICENSE file for details.