#!/bin/bash
# firewall.sh — unified iptables management for clawker agent containers.
#
# Usage:
#   firewall.sh enable <envoy_ip> <coredns_ip> <net_cidr> [host_proxy_url]
#   firewall.sh disable
#
# The enable command applies iptables rules that redirect the container user's
# outbound traffic through Envoy (TCP) and CoreDNS (DNS). The disable command
# flushes all such rules, giving unrestricted egress.
#
# Both commands are idempotent — enable flushes before re-applying, disable is
# safe to call when already disabled.
#
# This script must be run as root.

set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
    echo "ERROR: firewall.sh must be run as root" >&2
    exit 1
fi

# Resolve the container user's UID from CLAWKER_USER env var (set at create time).
CONTAINER_UID=$(id -u "${CLAWKER_USER}")

# flush_rules removes all clawker-managed iptables rules for the container user.
# Idempotent — safe to call when no rules exist.
flush_rules() {
    # Flush nat OUTPUT rules for the container user UID.
    # Delete in reverse order to avoid index shifting.
    local indices
    indices=$(iptables -t nat -L OUTPUT --line-numbers -n 2>/dev/null \
        | grep "owner UID match ${CONTAINER_UID}" \
        | awk '{print $1}' \
        | sort -rn) || true
    for idx in $indices; do
        iptables -t nat -D OUTPUT "$idx" 2>/dev/null || true
    done

    # Flush filter OUTPUT rules for the container user UID.
    indices=$(iptables -L OUTPUT --line-numbers -n 2>/dev/null \
        | grep "owner UID match ${CONTAINER_UID}" \
        | awk '{print $1}' \
        | sort -rn) || true
    for idx in $indices; do
        iptables -D OUTPUT "$idx" 2>/dev/null || true
    done

    # Also flush the root uid 0 RETURN rules we add (nat only).
    indices=$(iptables -t nat -L OUTPUT --line-numbers -n 2>/dev/null \
        | grep "owner UID match 0" \
        | awk '{print $1}' \
        | sort -rn) || true
    for idx in $indices; do
        iptables -t nat -D OUTPUT "$idx" 2>/dev/null || true
    done
}

enable_firewall() {
    local envoy_ip="$1"
    local coredns_ip="$2"
    local net_cidr="$3"
    local host_proxy="${4:-}"

    # Idempotent: flush existing rules before re-applying.
    flush_rules

    # Root (uid 0) bypasses all rules — escape hatch.
    iptables -t nat -A OUTPUT -p tcp -m owner --uid-owner 0 -j RETURN
    iptables -t nat -A OUTPUT -p udp -m owner --uid-owner 0 -j RETURN
    ip6tables -t nat -A OUTPUT -p tcp -m owner --uid-owner 0 -j RETURN 2>/dev/null || true
    ip6tables -t nat -A OUTPUT -p udp -m owner --uid-owner 0 -j RETURN 2>/dev/null || true

    # DNS: redirect container user's DNS queries to CoreDNS allowlist proxy.
    iptables -t nat -A OUTPUT -p udp --dport 53 -m owner --uid-owner "${CONTAINER_UID}" -j DNAT --to-destination "${coredns_ip}:53"
    iptables -t nat -A OUTPUT -p tcp --dport 53 -m owner --uid-owner "${CONTAINER_UID}" -j DNAT --to-destination "${coredns_ip}:53"

    # Intra-network: clawker-net traffic bypasses firewall entirely.
    iptables -t nat -A OUTPUT -p tcp -m owner --uid-owner "${CONTAINER_UID}" -d "${net_cidr}" -j RETURN

    # Host proxy: allow container user to reach the host proxy for git credentials, browser auth, etc.
    if [ -n "${host_proxy}" ]; then
        local hp_host hp_port hp_addrs hp_ip
        hp_host=$(echo "${host_proxy}" | sed -E 's|https?://||' | cut -d: -f1)
        hp_port=$(echo "${host_proxy}" | sed -E 's|https?://||' | cut -d: -f2 | cut -d/ -f1)
        hp_addrs=$( { getent ahosts "${hp_host}" 2>/dev/null || true; getent ahostsv4 "${hp_host}" 2>/dev/null || true; } | awk '{print $1}' | sort -u )
        if [ -n "${hp_addrs}" ] && [ -n "${hp_port}" ]; then
            while read -r hp_ip; do
                [ -z "${hp_ip}" ] && continue
                if [[ "${hp_ip}" =~ : ]]; then
                    ip6tables -t nat -A OUTPUT -p tcp -d "${hp_ip}" --dport "${hp_port}" -m owner --uid-owner "${CONTAINER_UID}" -j RETURN 2>/dev/null || true
                else
                    iptables -t nat -A OUTPUT -p tcp -d "${hp_ip}" --dport "${hp_port}" -m owner --uid-owner "${CONTAINER_UID}" -j RETURN
                fi
            done <<< "${hp_addrs}"
        fi
    fi

    # TCP catch-all: redirect container user's outbound TCP to Envoy TLS listener (SNI filtering).
    iptables -t nat -A OUTPUT -p tcp -m owner --uid-owner "${CONTAINER_UID}" ! -d 127.0.0.0/8 -j DNAT --to-destination "${envoy_ip}:10000"

    # UDP: allow intra-network, drop everything else (prevent exfiltration).
    iptables -A OUTPUT -p udp -m owner --uid-owner "${CONTAINER_UID}" -d "${net_cidr}" -j RETURN
    iptables -A OUTPUT -p udp -m owner --uid-owner "${CONTAINER_UID}" ! -d 127.0.0.0/8 -j DROP
}

disable_firewall() {
    flush_rules
}

case "${1:-}" in
    enable)
        shift
        if [ $# -lt 3 ]; then
            echo "Usage: firewall.sh enable <envoy_ip> <coredns_ip> <net_cidr> [host_proxy_url]" >&2
            exit 1
        fi
        enable_firewall "$@"
        ;;
    disable)
        disable_firewall
        ;;
    *)
        echo "Usage: firewall.sh {enable|disable}" >&2
        echo "  enable <envoy_ip> <coredns_ip> <net_cidr> [host_proxy_url]" >&2
        echo "  disable" >&2
        exit 1
        ;;
esac
