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
├── src/                   # 💻 Source code for development
│   ├── scale.js          # Main application code
│   ├── package.json      # Dependencies
│   ├── webpack.config.js # Build configuration
│   └── yarn.lock         # Lock file
├── build/                 # 🔨 Build scripts and Docker
│   ├── build.sh          # Build and push script
│   └── Dockerfile        # Container build
└── dist/                  # 📦 Build artifacts (generated)
```

## For Users

- **Deploy the scaler**: Go to [`deploy/`](deploy/) directory
- **Read documentation**: See [`deploy/README.md`](deploy/README.md)

## For Developers

- **Source code**: Located in [`src/`](src/) directory
- **Build the image**: Run [`build/build.sh`](build/build.sh)
- **Development setup**: Install dependencies in `src/` directory

## License

MIT License - See LICENSE file for details.