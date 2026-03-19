#!/bin/bash
# firewall-bypass — temporary unrestricted egress for clawker containers.
#
# Starts a Dante SOCKS5 proxy as root (uid 0 bypasses iptables DNAT rules
# installed by init-firewall.sh). Writes proxychains config to the system
# default so `proxychains4 <cmd>` just works with no extra flags.
#
# Danted runs in the background; a timeout watchdog kills it after the
# deadline. The script blocks until danted exits (timeout, stop, or crash).
#
# Usage:
#   firewall-bypass [timeout_secs]   Start bypass (default: 30s)
#   firewall-bypass stop             Stop active bypass
#   firewall-bypass status           Check if bypass is active
#
# This script must be run as root.

set -euo pipefail

SOCKS_PORT=9100
PIDFILE="/run/firewall-bypass.pid"
DANTED_CONF="/run/firewall-bypass-danted.conf"
PROXYCHAINS_CONF="/etc/proxychains4.conf"
DANTED_PID=""
TIMEOUT_PID=""

if [ "$(id -u)" -ne 0 ]; then
    echo "ERROR: firewall-bypass must be run as root" >&2
    exit 1
fi

remove_files() {
    rm -f "$PIDFILE" "$DANTED_CONF" "$PROXYCHAINS_CONF"
}

kill_gracefully() {
    local target_pid="$1"
    local label="${2:-process}"
    if ! kill -0 "$target_pid" 2>/dev/null; then
        return 0
    fi
    kill "$target_pid" 2>/dev/null || true
    sleep 0.2
    if kill -0 "$target_pid" 2>/dev/null; then
        echo "WARN: $label still running (PID $target_pid) after SIGTERM, sending SIGKILL" >&2
        kill -9 "$target_pid" 2>/dev/null || true
    fi
}

cleanup() {
    if [ -n "${TIMEOUT_PID:-}" ]; then
        kill "$TIMEOUT_PID" 2>/dev/null || true
    fi
    if [ -n "${DANTED_PID:-}" ]; then
        kill_gracefully "$DANTED_PID" "danted"
    fi
    remove_files
}

stop_bypass() {
    if [ -f "$PIDFILE" ]; then
        local pid
        pid=$(cat "$PIDFILE" 2>/dev/null)
        if ! echo "$pid" | grep -qE '^[0-9]+$'; then
            echo "WARN: corrupt pidfile, removing" >&2
            remove_files
            return 0
        fi
        if kill -0 "$pid" 2>/dev/null; then
            kill_gracefully "$pid" "danted"
            echo "Stopped firewall bypass proxy (PID $pid)"
        else
            echo "Proxy not running (stale PID $pid)"
        fi
        remove_files
    else
        echo "No active firewall bypass proxy"
    fi
}

check_status() {
    if [ -f "$PIDFILE" ]; then
        local pid
        pid=$(cat "$PIDFILE" 2>/dev/null)
        if echo "$pid" | grep -qE '^[0-9]+$' && kill -0 "$pid" 2>/dev/null; then
            echo "active"
            exit 0
        fi
        remove_files
    fi
    echo "inactive"
    exit 1
}

start_bypass() {
    local timeout_secs="${1:-30}"

    # Validate timeout is a positive integer.
    if ! echo "$timeout_secs" | grep -qE '^[0-9]+$' || [ "$timeout_secs" -eq 0 ]; then
        echo "ERROR: timeout must be a positive integer (seconds), got: $timeout_secs" >&2
        exit 1
    fi

    # Idempotent: if already running, show status and exit.
    if [ -f "$PIDFILE" ]; then
        local existing_pid
        existing_pid=$(cat "$PIDFILE" 2>/dev/null)
        if echo "$existing_pid" | grep -qE '^[0-9]+$' && kill -0 "$existing_pid" 2>/dev/null; then
            echo "Firewall bypass proxy already running on localhost:$SOCKS_PORT (PID $existing_pid)"
            return 0
        fi
        remove_files
    fi

    # Detect default route interface for Dante external binding.
    # Reads /proc/net/route directly — no iproute2 dependency.
    # Destination 00000000 = default route; field 1 = interface name.
    local ext_iface
    ext_iface=$(awk '$2 == "00000000" {print $1; exit}' /proc/net/route 2>/dev/null)
    if [ -z "$ext_iface" ]; then
        echo "ERROR: cannot determine default route interface — no default IPv4 route in /proc/net/route" >&2
        exit 1
    fi

    # Write Dante SOCKS5 config — listens on loopback, routes via detected interface as root.
    # user.unprivileged must be root — Dante forks child processes that create outbound sockets,
    # and those sockets must be owned by uid 0 to bypass the iptables RETURN rule.
    cat > "$DANTED_CONF" <<DEOF
logoutput: stdout
internal: 127.0.0.1 port = $SOCKS_PORT
external: $ext_iface
clientmethod: none
socksmethod: none
user.privileged: root
user.unprivileged: root

client pass {
    from: 127.0.0.0/8 to: 0.0.0.0/0
    log: connect disconnect error
}

socks pass {
    from: 127.0.0.0/8 to: 0.0.0.0/0
    command: connect udpassociate
    protocol: tcp udp
    log: connect disconnect error
}

socks pass {
    from: 127.0.0.0/8 to: 0.0.0.0/0
    command: bindreply udpreply
    log: connect disconnect error
}
DEOF

    # Write proxychains config to system default location.
    cat > "$PROXYCHAINS_CONF" <<PCEOF
strict_chain
proxy_dns
tcp_read_time_out 15000
tcp_connect_time_out 8000
[ProxyList]
socks5 127.0.0.1 $SOCKS_PORT
PCEOF
    chmod 644 "$PROXYCHAINS_CONF"

    # Set trap BEFORE starting danted so signals during startup still clean up.
    trap 'set +e; cleanup; exit 0' INT TERM HUP EXIT

    # Start danted in background.
    danted -f "$DANTED_CONF" &
    DANTED_PID=$!

    # Wait for SOCKS port to be ready (up to 3s).
    local ready=0
    for _i in 1 2 3 4 5 6; do
        if ! kill -0 "$DANTED_PID" 2>/dev/null; then
            echo "ERROR: danted exited immediately — check config" >&2
            remove_files
            exit 1
        fi
        if echo > /dev/tcp/127.0.0.1/$SOCKS_PORT 2>/dev/null; then
            ready=1
            break
        fi
        sleep 0.5
    done
    if [ "$ready" -eq 0 ]; then
        echo "ERROR: SOCKS port $SOCKS_PORT not listening after 3s" >&2
        cleanup
        exit 1
    fi

    echo "$DANTED_PID" > "$PIDFILE"


    # Background timeout killer — sends SIGTERM to danted after timeout.
    (sleep "$timeout_secs" && kill "$DANTED_PID" 2>/dev/null) &
    TIMEOUT_PID=$!

    # Block until danted exits (killed by timeout, external stop, crash, or signal).
    wait "$DANTED_PID" 2>/dev/null || true

    # Cancel timeout if danted exited early.
    kill "$TIMEOUT_PID" 2>/dev/null || true
    wait "$TIMEOUT_PID" 2>/dev/null || true

    cleanup
}

case "${1:-}" in
    stop)
        stop_bypass
        ;;
    status)
        check_status
        ;;
    *)
        start_bypass "${1:-30}"
        ;;
esac
