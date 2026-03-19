#!/bin/bash
set -euo pipefail

ENVOY_IP="${CLAWKER_FIREWALL_ENVOY_IP}"
COREDNS_IP="${CLAWKER_FIREWALL_COREDNS_IP}"
CLAWKER_NET_CIDR="${CLAWKER_FIREWALL_NET_CIDR}"

# Resolve the container user's UID from CLAWKER_USER env var.
CONTAINER_UID=$(id -u "${CLAWKER_USER}")

# Root (uid 0) and Envoy bypass all rules — escape hatch for bypass proxy.
iptables -t nat -A OUTPUT -p tcp -m owner --uid-owner 0 -j RETURN
iptables -t nat -A OUTPUT -p udp -m owner --uid-owner 0 -j RETURN

# DNS: redirect container user's DNS queries to CoreDNS allowlist proxy.
# Only the container user is filtered — root DNS goes through Docker's default resolver.
iptables -t nat -A OUTPUT -p udp --dport 53 -m owner --uid-owner "${CONTAINER_UID}" -j DNAT --to-destination ${COREDNS_IP}:53
iptables -t nat -A OUTPUT -p tcp --dport 53 -m owner --uid-owner "${CONTAINER_UID}" -j DNAT --to-destination ${COREDNS_IP}:53

# TCP: redirect container user's outbound TCP to Envoy (SNI filtering).
iptables -t nat -A OUTPUT -p tcp -m owner --uid-owner "${CONTAINER_UID}" ! -d 127.0.0.0/8 -j DNAT --to-destination ${ENVOY_IP}:10000

# UDP: drop container user's non-DNS UDP (prevent exfiltration).
iptables -A OUTPUT -p udp -m owner --uid-owner "${CONTAINER_UID}" ! -d 127.0.0.0/8 -j DROP
