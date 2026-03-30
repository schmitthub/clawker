#!/bin/bash
# firewall.sh — unified iptables management for clawker agent containers.
#
# Usage:
#   firewall.sh enable <envoy_ip> <coredns_ip> <net_cidr> [host_proxy_url] [tcp_mappings]
#   firewall.sh disable
#
# tcp_mappings is a semicolon-separated list of dst_port|envoy_port entries
# for non-TLS traffic (SSH, raw TCP). Each entry creates a per-port DNAT rule
# before the catch-all TLS DNAT. DNS (CoreDNS) is the domain gate.
#   Example: "22|10001;5432|10002"
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

# flush_rules removes all clawker-managed iptables and ip6tables rules for the container user.
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

    # Flush nat POSTROUTING SNAT rules for the container user UID.
    indices=$(iptables -t nat -L POSTROUTING --line-numbers -n 2>/dev/null \
        | grep "owner UID match ${CONTAINER_UID}" \
        | awk '{print $1}' \
        | sort -rn) || true
    for idx in $indices; do
        iptables -t nat -D POSTROUTING "$idx" 2>/dev/null || true
    done

    # Flush ip6tables rules — mirrors the iptables sections above.
    if command -v ip6tables >/dev/null 2>&1; then
        indices=$(ip6tables -t nat -L OUTPUT --line-numbers -n 2>/dev/null \
            | grep "owner UID match ${CONTAINER_UID}" \
            | awk '{print $1}' \
            | sort -rn) || true
        for idx in $indices; do
            ip6tables -t nat -D OUTPUT "$idx" 2>/dev/null || true
        done

        indices=$(ip6tables -L OUTPUT --line-numbers -n 2>/dev/null \
            | grep "owner UID match ${CONTAINER_UID}" \
            | awk '{print $1}' \
            | sort -rn) || true
        for idx in $indices; do
            ip6tables -D OUTPUT "$idx" 2>/dev/null || true
        done

        indices=$(ip6tables -t nat -L OUTPUT --line-numbers -n 2>/dev/null \
            | grep "owner UID match 0" \
            | awk '{print $1}' \
            | sort -rn) || true
        for idx in $indices; do
            ip6tables -t nat -D OUTPUT "$idx" 2>/dev/null || true
        done
    fi
}

# emit_agent_identity pushes an agent→IP mapping to Loki so Grafana can
# join Envoy access logs with agent names. Uses OTEL env vars as a signal
# that monitoring is configured. Runs as root (bypasses firewall rules).
# Best-effort — silently no-ops if monitoring isn't up or vars aren't set.
emit_agent_identity() {
    local client_ip="${1:-}"
    [ -z "${client_ip}" ] && return
    [ -z "${CLAWKER_AGENT:-}" ] && return
    [ -z "${OTEL_EXPORTER_OTLP_LOGS_ENDPOINT:-}" ] && return

    local agent="${CLAWKER_AGENT}"
    local project="${CLAWKER_PROJECT:-}"
    local ts
    ts=$(date +%s)000000000

    local line
    line=$(printf '{"source":"agent_map","agent":"%s","project":"%s","client_ip":"%s","action":"enable"}' \
        "${agent}" "${project}" "${client_ip}")

    local payload
    payload=$(printf '{"streams":[{"stream":{"service_name":"envoy","source":"agent_map","agent":"%s","client_ip":"%s","project":"%s","action":"enable"},"values":[["%s",%s]]}]}' \
        "${agent}" "${client_ip}" "${project}" "${ts}" "$(printf '%s' "${line}" | sed 's/"/\\"/g; s/^/"/; s/$/"/')")

    local loki_port="${CLAWKER_LOKI_PORT:-3100}"
    curl -s -m 2 -X POST "http://loki:${loki_port}/loki/api/v1/push" \
        -H "Content-Type: application/json" \
        -d "${payload}" >/dev/null 2>&1 || true
}

enable_firewall() {
    local envoy_ip="$1"
    local coredns_ip="$2"
    local net_cidr="$3"
    local host_proxy="${4:-}"
    local tcp_mappings="${5:-}"

    # Idempotent: flush existing rules before re-applying.
    flush_rules

    # Source IP fix for per-agent attribution in Envoy/CoreDNS logs.
    #
    # Problem: containers are on two Docker networks (default bridge eth0 + clawker-net
    # eth1). The default route goes through eth0. When iptables DNATs an outbound packet
    # to the Envoy IP on clawker-net, the kernel re-routes from eth0 to eth1 — but the
    # source address was already selected as the eth0 IP (172.17.0.x). The VM's host-level
    # MASQUERADE catches this cross-bridge source mismatch and rewrites it to the gateway
    # (172.18.0.1), hiding the real container identity.
    #
    # Fix: two parts.
    # 1. /32 routes ensure DNAT'd packets use the clawker-net interface directly.
    # 2. SNAT in POSTROUTING rewrites the source to the container's clawker-net IP for
    #    packets going to firewall containers. This is done INSIDE the container namespace
    #    before the packet hits the VM's iptables, so the VM never sees a cross-bridge source.
    local clawker_dev clawker_src
    clawker_dev=$(ip -4 route show "${net_cidr}" 2>/dev/null | awk '{print $3}') || true
    clawker_src=$(ip -4 addr show dev "${clawker_dev}" 2>/dev/null | awk '/inet /{sub(/\/.*/, "", $2); print $2}') || true
    if [ -n "${clawker_dev}" ]; then
        ip route replace "${envoy_ip}/32" dev "${clawker_dev}" 2>/dev/null || true
        ip route replace "${coredns_ip}/32" dev "${clawker_dev}" 2>/dev/null || true
    fi
    if [ -n "${clawker_src}" ]; then
        iptables -t nat -A POSTROUTING -d "${envoy_ip}" -m owner --uid-owner "${CONTAINER_UID}" -j SNAT --to-source "${clawker_src}"
        iptables -t nat -A POSTROUTING -d "${coredns_ip}" -m owner --uid-owner "${CONTAINER_UID}" -j SNAT --to-source "${clawker_src}"
    fi

    # Root (uid 0) bypasses all rules — escape hatch.
    iptables -t nat -A OUTPUT -p tcp -m owner --uid-owner 0 -j RETURN
    iptables -t nat -A OUTPUT -p udp -m owner --uid-owner 0 -j RETURN
    ip6tables -t nat -A OUTPUT -p tcp -m owner --uid-owner 0 -j RETURN 2>/dev/null || true
    ip6tables -t nat -A OUTPUT -p udp -m owner --uid-owner 0 -j RETURN 2>/dev/null || true

    # DNS: point resolv.conf to CoreDNS for domain filtering.
    # Container was created with --dns 1.1.1.2,1.0.0.2 (Cloudflare) so Docker's
    # internal DNS (127.0.0.11) forwards external queries there by default.
    # Flipping the nameserver to CoreDNS activates firewall DNS filtering.
    # CoreDNS has forward zones for docker.internal and monitoring stack names
    # that delegate back to Docker's DNS (127.0.0.11) for internal resolution.
    # Disable reverses this by restoring 127.0.0.11 (Docker → Cloudflare).
    sed "s/^nameserver .*/nameserver ${coredns_ip}/" /etc/resolv.conf > /tmp/resolv.clawker.tmp && cat /tmp/resolv.clawker.tmp > /etc/resolv.conf && rm -f /tmp/resolv.clawker.tmp

    # Also keep iptables DNAT as defense-in-depth (catches hardcoded DNS servers).
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

    # Per-port TCP/SSH DNAT rules (before catch-all).
    # Each mapping routes a destination port to a dedicated Envoy TCP listener.
    # Format: "dst_port|envoy_port;dst_port|envoy_port;..."
    # Port-only matching — DNS (CoreDNS) is the domain gate, iptables just routes by port.
    # Limitation: two different domains on the same non-TLS port share one Envoy listener.
    if [ -n "${tcp_mappings}" ]; then
        IFS=';' read -ra MAPPINGS <<< "${tcp_mappings}"
        for mapping in "${MAPPINGS[@]}"; do
            [ -z "${mapping}" ] && continue
            IFS='|' read -r dst_port envoy_port <<< "${mapping}"
            [ -z "${dst_port}" ] || [ -z "${envoy_port}" ] && continue
            iptables -t nat -A OUTPUT -p tcp --dport "${dst_port}" \
                -m owner --uid-owner "${CONTAINER_UID}" \
                -j DNAT --to-destination "${envoy_ip}:${envoy_port}"
        done
    fi

    # TCP catch-all: redirect container user's outbound TCP to Envoy TLS listener (SNI filtering).
    iptables -t nat -A OUTPUT -p tcp -m owner --uid-owner "${CONTAINER_UID}" ! -d 127.0.0.0/8 -j DNAT --to-destination "${envoy_ip}:10000"

    # UDP: allow intra-network, drop everything else (prevent exfiltration).
    iptables -A OUTPUT -p udp -m owner --uid-owner "${CONTAINER_UID}" -d "${net_cidr}" -j RETURN
    iptables -A OUTPUT -p udp -m owner --uid-owner "${CONTAINER_UID}" ! -d 127.0.0.0/8 -j DROP

    # ICMP: drop all outbound ICMP from the container user.
    # Prevents ICMP tunneling (ptunnel, icmpsh) which can exfiltrate data at ~50-100 KB/s.
    # ICMP is neither TCP nor UDP, so it's not caught by the rules above.
    iptables -A OUTPUT -p icmp -m owner --uid-owner "${CONTAINER_UID}" -j DROP

    # IPv6 egress: deny-by-default for TCP, UDP, and ICMPv6.
    # Envoy and CoreDNS bind to IPv4-only Docker bridge addresses, so IPv6 TCP cannot be
    # DNAT-ed through the allowlist the way IPv4 is. An agent with IPv6 connectivity could
    # otherwise bypass the Envoy SNI filter and CoreDNS domain gate entirely via IPv6 TCP.
    # Dropping all non-loopback IPv6 egress closes that bypass.  Root (uid 0) is already
    # exempted via the RETURN rules added above.
    if command -v ip6tables >/dev/null 2>&1; then
        ip6tables -A OUTPUT -p tcp    -m owner --uid-owner "${CONTAINER_UID}" ! -d ::1/128 -j DROP 2>/dev/null || true
        ip6tables -A OUTPUT -p udp    -m owner --uid-owner "${CONTAINER_UID}" ! -d ::1/128 -j DROP 2>/dev/null || true
        ip6tables -A OUTPUT -p icmpv6 -m owner --uid-owner "${CONTAINER_UID}" ! -d ::1/128 -j DROP 2>/dev/null || true
    fi

    # Emit agent identity to Loki so the dashboard can resolve client_ip → agent.
    # Runs as root (bypasses firewall), best-effort (monitoring may not be up).
    emit_agent_identity "${clawker_src:-}"
}

disable_firewall() {
    flush_rules
    # Restore Docker's nameserver (reverse of enable's sed).
    sed "s/^nameserver .*/nameserver 127.0.0.11/" /etc/resolv.conf > /tmp/resolv.clawker.tmp && cat /tmp/resolv.clawker.tmp > /etc/resolv.conf && rm -f /tmp/resolv.clawker.tmp
}

case "${1:-}" in
    enable)
        shift
        if [ $# -lt 3 ]; then
            echo "Usage: firewall.sh enable <envoy_ip> <coredns_ip> <net_cidr> [host_proxy_url] [tcp_mappings]" >&2
            exit 1
        fi
        enable_firewall "$@"
        ;;
    disable)
        disable_firewall
        ;;
    *)
        echo "Usage: firewall.sh {enable|disable}" >&2
        echo "  enable <envoy_ip> <coredns_ip> <net_cidr> [host_proxy_url] [tcp_mappings]" >&2
        echo "  disable" >&2
        exit 1
        ;;
esac
