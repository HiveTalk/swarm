# Multi-stage Dockerfile with Bouquet client build

# Stage 1: Build Bouquet client (use full node image for better memory handling)
FROM node:20 AS bouquet-builder

WORKDIR /app/clients/bouquet

# Install pnpm
RUN corepack enable && corepack prepare pnpm@latest --activate

# Copy bouquet source
COPY clients/bouquet/package.json clients/bouquet/pnpm-lock.yaml ./

# Install dependencies
RUN pnpm install --frozen-lockfile

# Copy rest of bouquet source
COPY clients/bouquet/ ./

# Build bouquet with adequate memory
RUN NODE_OPTIONS='--max-old-space-size=4096' pnpm exec tsc && \
    NODE_OPTIONS='--max-old-space-size=4096' pnpm exec vite build

# Stage 2: Build Go binary
FROM golang:1.24-alpine AS go-builder

WORKDIR /app

# Install build dependencies for CGO
RUN apk add --no-cache git gcc musl-dev

# Cache modules
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source
COPY . .

# Copy built bouquet from previous stage
COPY --from=bouquet-builder /app/bouquet-dist ./bouquet-dist

# Build static binary
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o /app/swarm

# Stage 3: Runtime - minimal Alpine image
FROM alpine:latest

LABEL "language"="go"

WORKDIR /app

# Install runtime dependencies (CA certs for HTTPS, timezone data)
RUN apk add --no-cache ca-certificates tzdata

# Copy binary from builder
COPY --from=go-builder /app/swarm /app/swarm

# Copy public assets (for .well-known/nostr.json fallback)
COPY --from=go-builder /app/public /app/public

# Copy bouquet dist
COPY --from=go-builder /app/bouquet-dist /app/bouquet-dist

EXPOSE 3334

# Set default environment variables (can be overridden by Zeabur env vars)
ENV CGO_ENABLED=1
ENV RELAY_PORT=3334
ENV RELAY_NAME="Swarm Relay"
ENV RELAY_PUBKEY="8ad8f1f78c8e11966242e28a7ca15c936b23a999d5fb91bfe4e4472e2d6eaf55"
ENV RELAY_DESCRIPTION="Team Nostr relay"
ENV DB_ENGINE=badger
ENV DB_PATH=/app/db/
ENV BLOSSOM_ENABLED=false
ENV WEBSOCKET_URL=ws://localhost:3334
ENV NPUB_DOMAIN="hivetalk.org"
ENV TEAM_DOMAIN="swarm.hivetalk.org" 

# Entrypoint simply runs the relay; configure via env vars
ENTRYPOINT ["/app/swarm"]