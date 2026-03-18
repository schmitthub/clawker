#!/bin/sh
# firewall-bypass — temporary unrestricted egress for clawker containers.
#
# Starts a Dante SOCKS5 proxy as root (uid 0 bypasses iptables DNAT rules
# installed by init-firewall.sh). Writes proxychains config to the system
# default so `proxychains4 <cmd>` just works with no extra flags.
#
# Danted runs in the foreground so logs stream back to the caller and
# ctrl+c cleanly stops the proxy. A background watchdog enforces the timeout.
#
# Usage:
#   firewall-bypass [timeout_secs]   Start bypass (default: 30s)
#   firewall-bypass stop             Stop active bypass
#   firewall-bypass status           Check if bypass is active
#
# This script must be run as root.

set -eu

DANTE_CONF="/run/firewall-bypass-danted.conf"
DANTE_PID="/run/firewall-bypass-danted.pid"
PROXYCHAINS_CONF="/etc/proxychains4.conf"
SOCKS_PORT=9100

cleanup() {
    # Kill the watchdog if it's still running.
    [ -n "${WATCHDOG_PID:-}" ] && kill "$WATCHDOG_PID" 2>/dev/null || true
    # Kill danted if it wrote a PID file.
    [ -f "$DANTE_PID" ] && kill "$(cat "$DANTE_PID")" 2>/dev/null || true
    rm -f "$DANTE_CONF" "$DANTE_PID" "$PROXYCHAINS_CONF"
}

stop_bypass() {
    if [ -f "$DANTE_PID" ]; then
        kill "$(cat "$DANTE_PID")" 2>/dev/null || true
    fi
    rm -f "$DANTE_CONF" "$DANTE_PID" "$PROXYCHAINS_CONF"
}

check_status() {
    if [ -f "$DANTE_PID" ] && kill -0 "$(cat "$DANTE_PID")" 2>/dev/null; then
        echo "active"
        exit 0
    else
        echo "inactive"
        exit 1
    fi
}

start_bypass() {
    timeout_secs="${1:-30}"

    trap cleanup EXIT INT TERM

    # Write Dante SOCKS5 config — listens on loopback, routes via eth0 as root.
    cat > "$DANTE_CONF" <<DANTED
logoutput: stderr
internal: 127.0.0.1 port = $SOCKS_PORT
external: eth0
socksmethod: none
user.privileged: root
user.unprivileged: root
client pass {
    from: 127.0.0.0/8 to: 0.0.0.0/0
}
socks pass {
    from: 127.0.0.0/8 to: 0.0.0.0/0
}
DANTED

    # Write proxychains config to system default location.
    cat > "$PROXYCHAINS_CONF" <<PCHAINS
strict_chain
proxy_dns
[ProxyList]
socks5 127.0.0.1 $SOCKS_PORT
PCHAINS
    chmod 644 "$PROXYCHAINS_CONF"

    # Timeout watchdog — kills this script after the deadline.
    (
        sleep "$timeout_secs"
        echo "[firewall-bypass] timeout reached (${timeout_secs}s), stopping proxy"
        kill $$ 2>/dev/null
    ) &
    WATCHDOG_PID=$!

    echo "[firewall-bypass] starting SOCKS proxy on 127.0.0.1:${SOCKS_PORT} (timeout: ${timeout_secs}s)"
    echo "[firewall-bypass] use 'proxychains4 <command>' for unrestricted egress"

    # Run danted in the foreground — logs stream to stderr, ctrl+c stops it.
    # The -N flag keeps danted in the foreground (no daemonize).
    exec danted -f "$DANTE_CONF" -N
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
