#!/bin/bash
set -e

# Configuration
REGISTRY="${REGISTRY:-ferterahadi}"
IMAGE_NAME="rabbitmq-vscaler"
VERSION="${VERSION:-latest}"
FULL_IMAGE="${REGISTRY}/${IMAGE_NAME}:${VERSION}"

echo "🚀 Building RabbitMQ Vertical Scaler"
echo "📦 Image: ${FULL_IMAGE}"

# Change to the root directory (parent of build/)
cd "$(dirname "$0")/.."

# Enable BuildKit for better caching and smaller images
export DOCKER_BUILDKIT=1

# Build the Docker image with webpack optimization
echo "🔨 Building optimized Docker image with webpack bundling..."
docker build --no-cache \
  -f build/Dockerfile \
  -t "${FULL_IMAGE}" \
  .

# Show image size
echo "📏 Image size:"
docker images "${FULL_IMAGE}" --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}"

# Push to registry
echo "📤 Pushing to registry..."
docker push "${FULL_IMAGE}"

echo "✅ Build and push complete!"
echo "🔧 Image: ${FULL_IMAGE}" 