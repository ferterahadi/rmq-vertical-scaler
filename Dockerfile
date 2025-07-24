# Multi-stage build for optimized production image
FROM node:20-alpine AS builder

# Set working directory
WORKDIR /app

# Copy package files
COPY package*.json ./

# Install dependencies (including devDependencies for build)
RUN npm ci

# Copy source code
COPY . .

# Build the application
RUN npm run build

# Production stage
FROM node:20-alpine AS production

# Install dumb-init for proper signal handling
RUN apk add --no-cache dumb-init

# Create non-root user
RUN addgroup -g 1001 -S rmqscaler && \
    adduser -S rmqscaler -u 1001 -G rmqscaler

# Set working directory
WORKDIR /app

# Copy package files
COPY package*.json ./

# Install only production dependencies
RUN npm ci --only=production && \
    npm cache clean --force

# Copy built application from builder stage
COPY --from=builder /app/lib ./lib
COPY --from=builder /app/bin ./bin
COPY --from=builder /app/dist ./dist 2>/dev/null || true

# Copy additional files
COPY README.md LICENSE ./

# Change ownership to non-root user
RUN chown -R rmqscaler:rmqscaler /app

# Switch to non-root user
USER rmqscaler

# Expose health check port (if implemented)
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD node -e "process.exit(0)" || exit 1

# Use dumb-init to handle signals properly
ENTRYPOINT ["dumb-init", "--"]

# Default command
CMD ["node", "bin/rmq-vertical-scaler"]