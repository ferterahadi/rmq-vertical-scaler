#!/bin/bash
set -e

# Configuration
REGISTRY="${REGISTRY:-ferterahadi}"
IMAGE_NAME="rabbitmq-vscaler"
VERSION="${VERSION:-v1.0.0}"
FULL_IMAGE="${REGISTRY}/${IMAGE_NAME}:${VERSION}"

echo "🚀 Building RabbitMQ Vertical Scaler"
echo "📦 Image: ${FULL_IMAGE}"

# Enable BuildKit for better caching and smaller images
export DOCKER_BUILDKIT=1

# Build the Docker image
echo "🔨 Building optimized Docker image..."
docker buildx build \
  --platform linux/amd64 \
  --build-arg BUILDKIT_INLINE_CACHE=1 \
  -t "${FULL_IMAGE}" \
  --load \
  .

# Show image size
echo "📏 Image size:"
docker images "${FULL_IMAGE}" --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}"

# Push to registry
echo "📤 Pushing to registry..."
docker push "${FULL_IMAGE}"

echo "✅ Build complete!"
echo "🔧 Update your Kubernetes deployment with:"
echo "   image: ${FULL_IMAGE}"

# Optional: Update the deployment file automatically
if [ "$UPDATE_DEPLOYMENT" = "true" ]; then
    echo "🔄 Updating deployment file..."
    sed -i.bak "s|image: your-registry/rmq-vertical-scaler:.*|image: ${FULL_IMAGE}|g" ../k8s/prod/prep/vv.rmq-vertical-scaler2.yaml
    echo "✅ Deployment file updated!"
fi 