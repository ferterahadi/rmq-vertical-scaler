# Build stage
FROM node:18-alpine AS builder

WORKDIR /app

# Copy package files
COPY package.json ./
COPY yarn.lock ./

# Install all dependencies (including dev for webpack)
RUN yarn install

# Copy source code
COPY scale.js webpack.config.js ./

# Build with webpack
RUN npm run build

# Final stage - use distroless for minimal size
FROM gcr.io/distroless/nodejs18-debian11

# Copy only the bundled file and minimal dependencies
WORKDIR /app
COPY --from=builder /app/dist/bundle.js ./
COPY --from=builder /app/node_modules/@kubernetes/client-node ./node_modules/@kubernetes/client-node

# Run as non-root user (distroless has a built-in nonroot user with UID 65532)
USER nonroot

# No need for npm - run node directly
CMD ["bundle.js"]