#!/bin/bash
set -euo pipefail

ENVOY_IP="${CLAWKER_FIREWALL_ENVOY_IP}"
COREDNS_IP="${CLAWKER_FIREWALL_COREDNS_IP}"
CLAWKER_NET_CIDR="${CLAWKER_FIREWALL_NET_CIDR}"
TCP_RULES="${CLAWKER_FIREWALL_TCP_RULES:-}"

# Resolve the container user's UID from CLAWKER_USER env var.
CONTAINER_UID=$(id -u "${CLAWKER_USER}")

# Root (uid 0) and Envoy bypass all rules — escape hatch for bypass proxy.
iptables -t nat -A OUTPUT -p tcp -m owner --uid-owner 0 -j RETURN
iptables -t nat -A OUTPUT -p udp -m owner --uid-owner 0 -j RETURN

# DNS: redirect container user's DNS queries to CoreDNS allowlist proxy.
# Only the container user is filtered — root DNS goes through Docker's default resolver.
iptables -t nat -A OUTPUT -p udp --dport 53 -m owner --uid-owner "${CONTAINER_UID}" -j DNAT --to-destination ${COREDNS_IP}:53
iptables -t nat -A OUTPUT -p tcp --dport 53 -m owner --uid-owner "${CONTAINER_UID}" -j DNAT --to-destination ${COREDNS_IP}:53

# TCP rules: per-rule DNAT to dedicated Envoy TCP listener ports.
# Format: "dst:port:envoyPort,dst:port:envoyPort,..." where port=0 means any port.
# These MUST come before the catch-all TLS redirect so they take priority.
if [ -n "${TCP_RULES}" ]; then
    IFS=',' read -ra RULES <<< "${TCP_RULES}"
    for rule in "${RULES[@]}"; do
        IFS=':' read -r dst dst_port envoy_port <<< "${rule}"
        if [ "${dst_port}" = "0" ]; then
            # Any port — resolve hostname and DNAT all TCP to this host.
            iptables -t nat -A OUTPUT -p tcp -d "${dst}" -m owner --uid-owner "${CONTAINER_UID}" -j DNAT --to-destination "${ENVOY_IP}:${envoy_port}"
        else
            # Specific port.
            iptables -t nat -A OUTPUT -p tcp -d "${dst}" --dport "${dst_port}" -m owner --uid-owner "${CONTAINER_UID}" -j DNAT --to-destination "${ENVOY_IP}:${envoy_port}"
        fi
    done
fi

# TCP catch-all: redirect container user's remaining outbound TCP to Envoy TLS listener (SNI filtering).
iptables -t nat -A OUTPUT -p tcp -m owner --uid-owner "${CONTAINER_UID}" ! -d 127.0.0.0/8 -j DNAT --to-destination ${ENVOY_IP}:10000

# UDP: drop container user's non-DNS UDP (prevent exfiltration).
iptables -A OUTPUT -p udp -m owner --uid-owner "${CONTAINER_UID}" ! -d 127.0.0.0/8 -j DROP
