# =============================================================================
# Multi-stage Dockerfile for ServerPanel backend
# =============================================================================

# --- Build stage ---
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o agent ./cmd/agent

# --- Server image ---
FROM alpine:3.19 AS server
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /build/server .
EXPOSE 8080
CMD ["./server"]

# --- Agent image ---
FROM alpine:3.19 AS agent
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /build/agent .
EXPOSE 8443
CMD ["./agent"]
