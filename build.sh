#!/bin/bash
set -e

# Usage information
if [ "$1" = "--help" ] || [ "$1" = "-h" ]; then
    echo "RabbitMQ Vertical Scaler Build Script"
    echo "====================================="
    echo ""
    echo "Usage: $0 [options]"
    echo ""
    echo "Environment Variables:"
    echo "  REGISTRY                - Docker registry (default: ferterahadi)"
    echo "  VERSION                 - Image version (default: auto-increment from git tags)"
    echo "  UPDATE_DEPLOYMENT       - Update deployment YAML files (default: false)"
    echo "  CREATE_GIT_TAG          - Create git tag for version (default: false)"
    echo "  SKIP_VERSION_INCREMENT  - Skip auto version increment (default: false)"
    echo ""
    echo "Examples:"
    echo "  $0                                    # Build with auto-incremented version"
    echo "  VERSION=v2.0.0 $0                    # Build with specific version"
    echo "  UPDATE_DEPLOYMENT=true $0            # Build and update YAML files"
    echo "  CREATE_GIT_TAG=true $0               # Build and create git tag"
    echo ""
    exit 0
fi

# Configuration
REGISTRY="${REGISTRY:-ferterahadi}"
IMAGE_NAME="rabbitmq-vscaler"
VERSION="${VERSION:-v1.0.0}"
FULL_IMAGE="${REGISTRY}/${IMAGE_NAME}:${VERSION}"
LATEST_IMAGE="${REGISTRY}/${IMAGE_NAME}:latest"

# Auto-increment version if not specified
if [ "$VERSION" = "v1.0.0" ] && [ -z "$SKIP_VERSION_INCREMENT" ]; then
    # Try to get the latest tag from git
    if git rev-parse --git-dir > /dev/null 2>&1; then
        LATEST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "v1.0.0")
        if [[ $LATEST_TAG =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
            MAJOR=${BASH_REMATCH[1]}
            MINOR=${BASH_REMATCH[2]}
            PATCH=${BASH_REMATCH[3]}
            NEW_PATCH=$((PATCH + 1))
            VERSION="v${MAJOR}.${MINOR}.${NEW_PATCH}"
            FULL_IMAGE="${REGISTRY}/${IMAGE_NAME}:${VERSION}"
            echo "🏷️  Auto-incrementing version to: ${VERSION}"
        fi
    fi
fi

echo "🚀 Building RabbitMQ Vertical Scaler"
echo "📦 Image: ${FULL_IMAGE}"

# Enable BuildKit for better caching and smaller images
export DOCKER_BUILDKIT=1

# Build the Docker image
echo "🔨 Building optimized Docker image..."
docker build --no-cache \
  -t "${FULL_IMAGE}" \
  .

# Show image size
echo "📏 Image size:"
docker images "${FULL_IMAGE}" --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}"

# Push to registry
echo "📤 Pushing to registry..."
docker push "${FULL_IMAGE}"

# If VERSION is not 'latest', also tag and push as latest
if [ "$VERSION" != "latest" ]; then
    echo "🏷️  Tagging as latest..."
    docker tag "${FULL_IMAGE}" "${LATEST_IMAGE}"
    docker push "${LATEST_IMAGE}"
fi

echo "✅ Build complete!"
echo "🔧 Update your Kubernetes deployment with:"
echo "   image: ${FULL_IMAGE}"

# Optional: Update deployment files automatically
if [ "$UPDATE_DEPLOYMENT" = "true" ]; then
    echo "🔄 Updating deployment files..."
    
    # Update any generated YAML files in current directory
    for yaml_file in *-scaler.yaml; do
        if [ -f "$yaml_file" ]; then
            echo "  📝 Updating $yaml_file"
            sed -i.bak "s|image: ferterahadi/rabbitmq-vscaler:.*|image: ${FULL_IMAGE}|g" "$yaml_file"
        fi
    done
    
    # Update any deployment files in common paths
    for path in "../k8s/prod/prep" "../k8s" "../deployments" "."; do
        if [ -d "$path" ]; then
            find "$path" -name "*.yaml" -o -name "*.yml" | while read -r file; do
                if grep -q "ferterahadi/rabbitmq-vscaler" "$file" 2>/dev/null; then
                    echo "  📝 Updating $file"
                    sed -i.bak "s|image: ferterahadi/rabbitmq-vscaler:.*|image: ${FULL_IMAGE}|g" "$file"
                fi
            done
        fi
    done
    
    echo "✅ Deployment files updated!"
fi

# Optional: Create git tag for the version
if [ "$CREATE_GIT_TAG" = "true" ] && git rev-parse --git-dir > /dev/null 2>&1; then
    echo "🏷️  Creating git tag: ${VERSION}"
    git tag -a "${VERSION}" -m "Release ${VERSION}"
    echo "📤 Push the tag with: git push origin ${VERSION}"
fi 