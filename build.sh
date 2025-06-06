#!/bin/bash
set -e

# Configuration
REGISTRY="${REGISTRY:-your-registry}"
IMAGE_NAME="rmq-vertical-scaler"
VERSION="${VERSION:-v1.0.0}"
FULL_IMAGE="${REGISTRY}/${IMAGE_NAME}:${VERSION}"

echo "🚀 Building RabbitMQ Vertical Scaler"
echo "📦 Image: ${FULL_IMAGE}"

# Build the Docker image
echo "🔨 Building Docker image..."
docker build -t "${FULL_IMAGE}" .

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