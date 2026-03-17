#!/bin/bash
set -euo pipefail

ENVOY_IP="${CLAWKER_FIREWALL_ENVOY_IP}"
COREDNS_IP="${CLAWKER_FIREWALL_COREDNS_IP}"
CLAWKER_NET_CIDR="${CLAWKER_FIREWALL_NET_CIDR}"

# Root (uid 0) bypasses all rules — escape hatch
iptables -t nat -A OUTPUT -m owner --uid-owner 0 -j RETURN

# Preserve Docker DNS (127.0.0.11) and loopback
iptables -t nat -A OUTPUT -d 127.0.0.0/8 -j RETURN

# Preserve clawker-net internal traffic
iptables -t nat -A OUTPUT -d ${CLAWKER_NET_CIDR} -j RETURN

# DNS redirect: all non-root UDP/TCP 53 → CoreDNS
iptables -t nat -A OUTPUT -p udp --dport 53 -j DNAT --to-destination ${COREDNS_IP}:53
iptables -t nat -A OUTPUT -p tcp --dport 53 -j DNAT --to-destination ${COREDNS_IP}:53

# TCP DNAT: all non-root TCP → Envoy
iptables -t nat -A OUTPUT -p tcp -j DNAT --to-destination ${ENVOY_IP}:10000

# Drop all other UDP (prevent exfiltration)
iptables -A OUTPUT -p udp ! -d 127.0.0.0/8 -m owner ! --uid-owner 0 -j DROP
