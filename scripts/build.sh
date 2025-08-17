#!/bin/bash
set -e

# Configuration
REGISTRY="${REGISTRY:-ferterahadi}"
IMAGE_NAME="rmq-vertical-scaler"
VERSION="${VERSION:-latest}"
FULL_IMAGE="${REGISTRY}/${IMAGE_NAME}:${VERSION}"

echo "🚀 Building RMQ Vertical Scaler"
echo "📦 Image: ${FULL_IMAGE}"

# Ensure we're in the root directory
cd "$(dirname "$0")/.."

# Enable BuildKit for better caching and smaller images
export DOCKER_BUILDKIT=1

# Run tests first
echo "🧪 Running tests..."
npm test

# Build the optimized bundle
echo "📦 Building optimized bundle with webpack..."
npm run build

# Build the Docker image
echo "🔨 Building Docker image..."
docker build --no-cache \
  -t "${FULL_IMAGE}" \
  .

# Show image size
echo "📏 Image size:"
docker images "${FULL_IMAGE}" --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}"

# Optional: Push to registry if PUSH=true
if [ "${PUSH:-false}" = "true" ]; then
  echo "📤 Pushing to registry..."
  docker push "${FULL_IMAGE}"
  echo "✅ Build and push complete!"
else
  echo "✅ Build complete! (Use PUSH=true to push to registry)"
fi

echo "🔧 Image: ${FULL_IMAGE}"