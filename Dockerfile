# Stage 1: Build static Go binary
FROM golang:alpine AS builder

WORKDIR /app

RUN apk add --no-cache ca-certificates git

# Cache module downloads
COPY server/go.mod server/go.sum ./
RUN go mod download

# Copy source tree
COPY server/ .

# Compile stripped static binary (auto-detects architecture: amd64 / arm64)
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -extldflags '-static'" \
    -o /app/browse-gemini ./cmd/server

# Stage 2: Minimal runtime image (~10MB RAM footprint)
FROM alpine:latest

# ca-certificates for outbound TLS, tzdata for proper timestamps
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Create volume mount point for persistent OAuth client and token state
VOLUME ["/data"]

COPY --from=builder /app/browse-gemini /app/browse-gemini

ENV PORT=8080 \
    HOST=0.0.0.0 \
    DATA_DIR=/data

EXPOSE 8080

# Health check probe
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/health || exit 1

ENTRYPOINT ["/app/browse-gemini"]
