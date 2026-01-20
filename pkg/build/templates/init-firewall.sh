#!/bin/bash
set -euo pipefail  # Exit on error, undefined vars, and pipeline failures
IFS=$'\n\t'       # Stricter word splitting

# Fix Docker socket permissions for Docker-outside-of-Docker
# On macOS with Docker Desktop, the socket is owned by root:root and chgrp doesn't work
# So we make it world-readable/writable (this is safe since we're inside an isolated container)
if [ -S /var/run/docker.sock ]; then
    chmod 666 /var/run/docker.sock 2>/dev/null || true
fi

# 1. Extract Docker DNS info BEFORE any flushing
DOCKER_DNS_RULES=$(iptables-save -t nat | grep "127\.0\.0\.11" || true)

# Flush existing rules and delete existing ipsets
iptables -F
iptables -X
iptables -t nat -F
iptables -t nat -X
iptables -t mangle -F
iptables -t mangle -X
ipset destroy allowed-domains 2>/dev/null || true

# 2. Selectively restore ONLY internal Docker DNS resolution
if [ -n "$DOCKER_DNS_RULES" ]; then
    echo "Restoring Docker DNS rules..."
    iptables -t nat -N DOCKER_OUTPUT 2>/dev/null || true
    iptables -t nat -N DOCKER_POSTROUTING 2>/dev/null || true
    echo "$DOCKER_DNS_RULES" | xargs -L 1 iptables -t nat
else
    echo "No Docker DNS rules to restore"
fi

# First allow DNS and localhost before any restrictions
# Allow outbound DNS
iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
# Allow inbound DNS responses
iptables -A INPUT -p udp --sport 53 -j ACCEPT
# Allow outbound SSH
iptables -A OUTPUT -p tcp --dport 22 -j ACCEPT
# Allow inbound SSH responses
iptables -A INPUT -p tcp --sport 22 -m state --state ESTABLISHED -j ACCEPT
# Allow localhost
iptables -A INPUT -i lo -j ACCEPT
iptables -A OUTPUT -o lo -j ACCEPT

# Create ipset with CIDR support
ipset create allowed-domains hash:net

# Fetch GitHub meta information and aggregate + add their IP ranges
echo "Fetching GitHub IP ranges..."
gh_ranges=$(curl -s https://api.github.com/meta)
if [ -z "$gh_ranges" ]; then
    echo "ERROR: Failed to fetch GitHub IP ranges"
    exit 1
fi

if ! echo "$gh_ranges" | jq -e '.web and .api and .git and .copilot and .packages and .pages and .importer and .actions and .domains' >/dev/null; then
    echo "ERROR: GitHub API response missing required fields"
    exit 1
fi

echo "Processing GitHub IPs..."
while read -r cidr; do
    if [[ ! "$cidr" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}/[0-9]{1,2}$ ]]; then
        echo "ERROR: Invalid CIDR range from GitHub meta: $cidr"
        exit 1
    fi
    echo "Adding GitHub range $cidr"
    if ! ipset add allowed-domains "$cidr" 2>&1 | grep -v "Element cannot be added to the set: it's already added"; then
        # Ignore "already added" errors, but fail on others
        error_msg=$(ipset add allowed-domains "$cidr" 2>&1 || true)
        if [[ ! "$error_msg" =~ "already added" ]]; then
            echo "ERROR: Failed to add $cidr: $error_msg"
            exit 1
        fi
    fi
done < <(echo "$gh_ranges" | jq -r '(.web + .api + .git + .copilot + .packages + .pages + .importer + .actions)[]' | aggregate -q)

# Resolve and add other allowed domains
for domain in \
    "registry.npmjs.org" \
    "api.anthropic.com" \
    "sentry.io" \
    "statsig.anthropic.com" \
    "statsig.com" \
    "marketplace.visualstudio.com" \
    "vscode.blob.core.windows.net" \
    "update.code.visualstudio.com" \
    "registry-1.docker.io" \
    "production.cloudflare.docker.com" \
    "proxy.golang.org" \
    "sum.golang.org" \
    "docker.io"; do
    echo "Resolving $domain..."
    ips=$(dig +noall +answer A "$domain" | awk '$4 == "A" {print $5}')
    if [ -z "$ips" ]; then
        echo "ERROR: Failed to resolve $domain"
        exit 1
    fi

    while read -r ip; do
        if [[ ! "$ip" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
            echo "ERROR: Invalid IP from DNS for $domain: $ip"
            exit 1
        fi
        echo "Adding $ip for $domain"
        if ! ipset add allowed-domains "$ip" 2>&1 | grep -v "Element cannot be added to the set: it's already added"; then
            echo "INFO: $ip for $domain is already in the set, skipping"
            # Ignore "already added" errors, but fail on others
            error_msg=$(ipset add allowed-domains "$ip" 2>&1 || true)
            if [[ ! "$error_msg" =~ "already added" ]]; then
                echo "ERROR: Failed to add $ip: $error_msg"
                exit 1
            fi
        fi
    done < <(echo "$ips")
done

# Get host IP from default route
HOST_IP=$(ip route | grep default | cut -d" " -f3)
if [ -z "$HOST_IP" ]; then
    echo "ERROR: Failed to detect host IP"
    exit 1
fi

HOST_NETWORK=$(echo "$HOST_IP" | sed "s/\.[0-9]*$/.0\/24/")
echo "Host network detected as: $HOST_NETWORK"

# Set up remaining iptables rules
iptables -A INPUT -s "$HOST_NETWORK" -j ACCEPT
iptables -A OUTPUT -d "$HOST_NETWORK" -j ACCEPT

# Allow access to host machine via host.docker.internal (for MCP servers, etc.)
# Handle both IPv4 (iptables) and IPv6 (ip6tables) addresses
# Note: getent hosts only returns one address (prefers IPv6), so we combine with ahostsv4
# to ensure we get both IPv4 and IPv6 addresses for Docker Desktop compatibility.
host_addrs=$( (getent hosts host.docker.internal 2>/dev/null | awk '{print $1}'; getent ahostsv4 host.docker.internal 2>/dev/null | awk '{print $1}') | sort -u )
if [ -n "$host_addrs" ]; then
    while read -r host_ip; do
        if [[ "$host_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            echo "Allowing host.docker.internal (IPv4): $host_ip"
            iptables -A INPUT -s "$host_ip" -j ACCEPT
            iptables -A OUTPUT -d "$host_ip" -j ACCEPT
        elif [[ "$host_ip" =~ : ]]; then
            echo "Allowing host.docker.internal (IPv6): $host_ip"
            ip6tables -A INPUT -s "$host_ip" -j ACCEPT 2>/dev/null || echo "Note: ip6tables not available, skipping IPv6 rule"
            ip6tables -A OUTPUT -d "$host_ip" -j ACCEPT 2>/dev/null || true
        fi
    done <<< "$host_addrs"
else
    echo "Note: host.docker.internal not available (not running on Docker Desktop)"
fi

# Allow full access to Docker bridge networks (e.g., clawker-net)
# These show up as non-default routes when container is connected to user-defined networks
echo "Detecting Docker bridge networks..."
while read -r network; do
    if [ -n "$network" ]; then
        echo "Allowing Docker network: $network"
        iptables -A INPUT -s "$network" -j ACCEPT
        iptables -A OUTPUT -d "$network" -j ACCEPT
    fi
done < <(ip route | grep -v default | grep -v "dev lo" | awk '{print $1}' | grep -E "^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+$")

# Set default policies to DROP first
iptables -P INPUT DROP
iptables -P FORWARD DROP
iptables -P OUTPUT DROP

# First allow established connections for already approved traffic
iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

# Then allow only specific outbound traffic to allowed domains
iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT

# Explicitly REJECT all other outbound traffic for immediate feedback
iptables -A OUTPUT -j REJECT --reject-with icmp-admin-prohibited

echo "Firewall configuration complete"
echo "Verifying firewall rules..."
if curl --connect-timeout 5 https://example.com >/dev/null 2>&1; then
    echo "ERROR: Firewall verification failed - was able to reach https://example.com"
    exit 1
else
    echo "Firewall verification passed - unable to reach https://example.com as expected"
fi

# Verify GitHub API access
if ! curl --connect-timeout 5 https://api.github.com/zen >/dev/null 2>&1; then
    echo "ERROR: Firewall verification failed - unable to reach https://api.github.com"
    exit 1
else
    echo "Firewall verification passed - able to reach https://api.github.com as expected"
fi
