# Attacker C2 server for adversarial testing of clawker containers
# Captures exfil attempts in SQLite across multiple protocols

# Build stage
FROM golang:1.25.8-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY attacker-server/go.mod attacker-server/go.sum ./
RUN go mod download

COPY attacker-server/main.go ./

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /attacker-server .

# Runtime stage
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates wget \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /attacker-server /usr/local/bin/attacker-server

RUN mkdir -p /payloads /data

EXPOSE 8443 9443 8080 5353/udp

HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q --no-check-certificate --spider https://localhost:8443/health || exit 1

ENV TLS_CERT=/certs/server.crt
ENV TLS_KEY=/certs/server.key
ENV LISTEN_ADDR=:8443
ENV HTTP_PORT=:8080
ENV PAYLOADS_DIR=/payloads

ENTRYPOINT ["attacker-server"]
