#!/bin/bash
# Claucker Firewall Initialization
# Blocks outbound traffic except for allowed domains

set -e

# Skip if not running as root
if [ "$(id -u)" -ne 0 ]; then
    echo "Firewall init requires root, skipping..."
    exit 0
fi

# Check for iptables
if ! command -v iptables &> /dev/null; then
    echo "iptables not found, skipping firewall init..."
    exit 0
fi

# Flush existing rules
iptables -F OUTPUT 2>/dev/null || true
iptables -F INPUT 2>/dev/null || true

# Allow loopback
iptables -A OUTPUT -o lo -j ACCEPT
iptables -A INPUT -i lo -j ACCEPT

# Allow established connections
iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

# Allow DNS
iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
iptables -A OUTPUT -p tcp --dport 53 -j ACCEPT

# Allow HTTPS (needed for Claude API, package managers, etc.)
iptables -A OUTPUT -p tcp --dport 443 -j ACCEPT

# Allow HTTP (some package managers need this)
iptables -A OUTPUT -p tcp --dport 80 -j ACCEPT

# Allow SSH (for git operations)
iptables -A OUTPUT -p tcp --dport 22 -j ACCEPT

# Allow git protocol
iptables -A OUTPUT -p tcp --dport 9418 -j ACCEPT

# Log and drop everything else (optional, can be noisy)
# iptables -A OUTPUT -j LOG --log-prefix "CLAUCKER-BLOCKED: "
# iptables -A OUTPUT -j DROP

echo "Firewall initialized"
