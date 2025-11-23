# Single-stage Dockerfile for Swarm relay
FROM golang:1.24

WORKDIR /app

# Cache modules
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source
COPY . .

# Build binary for the container's native architecture
RUN CGO_ENABLED=1 go build -o /app/swarm

# Copy default .env.example so container can run with minimal config.
# You can override by binding your own .env at runtime.
COPY .env.example /app/.env

# Expose default relay port; actual port is set via RELAY_PORT env
EXPOSE 3334

# Use the same .env-driven config as bare-metal
ENV CGO_ENABLED=1

# Entrypoint simply runs the relay; configure via mounted .env or env vars
ENTRYPOINT ["/app/swarm"]
