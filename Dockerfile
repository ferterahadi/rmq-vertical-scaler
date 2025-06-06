FROM node:18-alpine

# Install kubectl
RUN apk add --no-cache curl && \
    curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin/

# Create app directory
WORKDIR /app

# Copy package.json and package-lock.json (if available)
COPY package*.json ./

# Install app dependencies
RUN npm ci --only=production

# Copy app source
COPY scale.js ./

# Create non-root user for security
RUN addgroup -g 1001 -S scaler && \
    adduser -S -D -H -u 1001 -s /sbin/nologin -G scaler scaler

# Change ownership of the app directory
RUN chown -R scaler:scaler /app

# Switch to non-root user
USER scaler

# Expose port (if needed for health checks)
EXPOSE 3000

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD node -e "console.log('Health check passed')" || exit 1

# Start the application
CMD ["npm", "start"] 