# Build stage
FROM node:18-alpine AS builder

WORKDIR /app

# Copy package files from src directory
COPY src/package.json src/yarn.lock ./

# Install all dependencies (including dev dependencies for webpack)
RUN yarn install --frozen-lockfile

# Copy source code and webpack config from src directory
COPY src/scale.js src/webpack.config.js ./

# Build the application with webpack
RUN yarn build

# Production stage
FROM node:18-alpine AS production

# Install dumb-init for proper signal handling
RUN apk add --no-cache dumb-init

WORKDIR /app

# Copy package files for production dependencies only
COPY src/package.json src/yarn.lock ./

# Install only production dependencies and clean cache
RUN yarn install --production --frozen-lockfile && \
    yarn cache clean && \
    rm -rf /tmp/* /var/cache/apk/*

# Copy the bundled application from build stage
COPY --from=builder /app/dist/scale.js ./
COPY --from=builder /app/dist/scale.js.map ./

# Create non-root user
RUN addgroup -g 1001 -S scaler && \
    adduser -S -D -H -u 1001 -s /sbin/nologin -G scaler scaler && \
    chown -R scaler:scaler /app

# Switch to non-root user
USER scaler

# Use dumb-init to handle signals properly
ENTRYPOINT ["dumb-init", "--"]

# Run the bundled application with source map support
CMD ["node", "--enable-source-maps", "scale.js"]