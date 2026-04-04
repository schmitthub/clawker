#!/bin/bash
# Setup script for the adversarial test harness
# Run this once before starting the C2 server
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HARNESS_DIR="$(dirname "$SCRIPT_DIR")"
CERTS_DIR="${HARNESS_DIR}/certs"

echo "=== Clawker Adversarial Test Harness Setup ==="

# Generate self-signed TLS certs
if [ -f "${CERTS_DIR}/server.crt" ] && [ -f "${CERTS_DIR}/server.key" ]; then
    echo "Certs already exist at ${CERTS_DIR}/, skipping generation"
else
    echo "Generating self-signed TLS certificate..."
    mkdir -p "$CERTS_DIR"
    openssl req -x509 -newkey rsa:2048 \
        -keyout "${CERTS_DIR}/server.key" \
        -out "${CERTS_DIR}/server.crt" \
        -days 365 -nodes \
        -subj "/CN=attacker/O=ClawkerTest/C=US" 2>/dev/null
    echo "Certs written to ${CERTS_DIR}/"
fi

echo ""
echo "Setup complete. Start the C2:"
echo "  cd test/adversarial && docker compose up -d"
