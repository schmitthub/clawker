#!/bin/bash
# Setup script for the adversarial test harness
# Run this once before starting the C2 server
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HARNESS_DIR="$(dirname "$SCRIPT_DIR")"
CERTS_DIR="${HARNESS_DIR}/certs"

# Resolve clawker data dir: env var > default XDG path
CLAWKER_DATA="${CLAWKER_DATA_DIR:-${HOME}/.local/share/clawker}"
FIREWALL_CA_CERT="${CLAWKER_DATA}/firewall/certs/ca-cert.pem"
FIREWALL_CA_KEY="${CLAWKER_DATA}/firewall/certs/ca-key.pem"

# All domains the attacker server needs certs for (compose aliases + localhost).
SANS="DNS:attacker,DNS:localhost,DNS:cdn-jsdelivr.net,DNS:hooks-slackapi.com,DNS:api-githubcdn.com,DNS:storage-googleapis.net,DNS:registry-nprnjs.org,IP:127.0.0.1"

echo "=== Clawker Adversarial Test Harness Setup ==="

mkdir -p "$CERTS_DIR"

if [ -f "${CERTS_DIR}/server.crt" ] && [ -f "${CERTS_DIR}/server.key" ]; then
    echo "Certs already exist at ${CERTS_DIR}/, skipping generation"
    echo "  (delete certs/ to regenerate)"
else
    if [ -f "$FIREWALL_CA_CERT" ] && [ -f "$FIREWALL_CA_KEY" ]; then
        echo "Found clawker firewall CA at ${CLAWKER_DATA}/firewall/certs/"
        echo "Signing attacker server cert with it..."
        cp "$FIREWALL_CA_CERT" "${CERTS_DIR}/ca-cert.pem"
        cp "$FIREWALL_CA_KEY" "${CERTS_DIR}/ca-key.pem"

        # Generate server key + CSR
        openssl ecparam -genkey -name prime256v1 -noout \
            -out "${CERTS_DIR}/server.key" 2>/dev/null

        openssl req -new -key "${CERTS_DIR}/server.key" \
            -out "${CERTS_DIR}/server.csr" \
            -subj "/CN=attacker/O=ClawkerTest" 2>/dev/null

        # Sign with the firewall CA
        openssl x509 -req \
            -in "${CERTS_DIR}/server.csr" \
            -CA "${CERTS_DIR}/ca-cert.pem" \
            -CAkey "${CERTS_DIR}/ca-key.pem" \
            -CAcreateserial \
            -out "${CERTS_DIR}/server.crt" \
            -days 365 \
            -extfile <(printf "subjectAltName=%s" "$SANS") 2>/dev/null

        rm -f "${CERTS_DIR}/server.csr" "${CERTS_DIR}/ca-cert.srl"
        echo "Certs signed by clawker firewall CA -> ${CERTS_DIR}/"
    else
        echo "Firewall CA not found at ${FIREWALL_CA_CERT}"
        echo "Generating self-signed cert (containers won't trust it without --insecure)..."
        openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
            -keyout "${CERTS_DIR}/server.key" \
            -out "${CERTS_DIR}/server.crt" \
            -days 365 -nodes \
            -subj "/CN=attacker/O=ClawkerTest" \
            -addext "subjectAltName=${SANS}" 2>/dev/null
        echo "Self-signed certs written to ${CERTS_DIR}/"
    fi
fi

echo ""
echo "Setup complete. Start the C2:"
echo "  cd test/adversarial && docker compose up -d"
