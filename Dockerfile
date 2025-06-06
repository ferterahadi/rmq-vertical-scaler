FROM node:18-alpine

# Install dumb-init for proper signal handling
RUN apk add --no-cache dumb-init

WORKDIR /app

# Copy package files
COPY package.json yarn.lock ./

# Install production dependencies only
RUN yarn install --production --frozen-lockfile && \
    yarn cache clean

# Copy application source
COPY scale.js ./

# Create non-root user
RUN addgroup -g 1001 -S scaler && \
    adduser -S -D -H -u 1001 -s /sbin/nologin -G scaler scaler && \
    chown -R scaler:scaler /app

# Switch to non-root user
USER scaler

# Use dumb-init to handle signals properly
ENTRYPOINT ["dumb-init", "--"]

# Run the application directly
CMD ["node", "scale.js"]