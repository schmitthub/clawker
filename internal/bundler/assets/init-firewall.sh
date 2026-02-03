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

# Built-in IP range source configurations
# These map source names to their API URLs and jq filters
get_builtin_url() {
    local name="$1"
    case "$name" in
        github)       echo "https://api.github.com/meta" ;;
        google-cloud) echo "https://www.gstatic.com/ipranges/cloud.json" ;;
        google)       echo "https://www.gstatic.com/ipranges/goog.json" ;;
        cloudflare)   echo "https://api.cloudflare.com/client/v4/ips" ;;
        aws)          echo "https://ip-ranges.amazonaws.com/ip-ranges.json" ;;
        *)            echo "" ;;
    esac
}

get_builtin_filter() {
    local name="$1"
    case "$name" in
        github)       echo '(.web + .api + .git + .copilot + .packages + .pages + .importer + .actions)[]' ;;
        google-cloud) echo '.prefixes[].ipv4Prefix // empty' ;;
        google)       echo '.prefixes[].ipv4Prefix // empty' ;;
        cloudflare)   echo '.result.ipv4_cidrs[]' ;;
        aws)          echo '.prefixes[].ip_prefix' ;;
        *)            echo "" ;;
    esac
}

# Special validation for GitHub response (has specific field structure)
validate_github_response() {
    local response="$1"
    if ! echo "$response" | jq -e '.web and .api and .git and .copilot and .packages and .pages and .importer and .actions and .domains' >/dev/null 2>&1; then
        return 1
    fi
    return 0
}

# Process a single IP range source
# Args: name, url, jq_filter, required (true/false)
process_ip_range_source() {
    local name="$1"
    local url="$2"
    local jq_filter="$3"
    local required="$4"

    echo "Fetching IP ranges from $name ($url)..."
    local response
    response=$(curl -s --connect-timeout 10 "$url" || true)

    if [ -z "$response" ]; then
        if [ "$required" = "true" ]; then
            echo "ERROR: Failed to fetch required IP ranges from $name"
            exit 1
        else
            echo "WARNING: Failed to fetch IP ranges from $name, skipping"
            return 0
        fi
    fi

    # Special validation for GitHub
    if [ "$name" = "github" ]; then
        if ! validate_github_response "$response"; then
            if [ "$required" = "true" ]; then
                echo "ERROR: GitHub API response missing required fields"
                exit 1
            else
                echo "WARNING: GitHub API response invalid, skipping"
                return 0
            fi
        fi
    fi

    echo "Processing $name IPs..."
    local cidrs
    cidrs=$(echo "$response" | jq -r "$jq_filter" 2>/dev/null || true)

    if [ -z "$cidrs" ]; then
        if [ "$required" = "true" ]; then
            echo "ERROR: No CIDRs extracted from $name response"
            exit 1
        else
            echo "WARNING: No CIDRs extracted from $name, skipping"
            return 0
        fi
    fi

    # Use aggregate tool if available (for CIDR compression), otherwise process directly
    local process_cmd="cat"
    if command -v aggregate >/dev/null 2>&1; then
        process_cmd="aggregate -q"
    fi

    while read -r cidr; do
        # Skip empty lines
        [ -z "$cidr" ] && continue
        # Skip IPv6 ranges (ipset is IPv4 only)
        if [[ "$cidr" =~ : ]]; then
            echo "Skipping IPv6 range (ipset hash:net is IPv4 only): $cidr"
            continue
        fi
        # Validate IPv4 CIDR or single IP
        if [[ "$cidr" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}(/[0-9]{1,2})?$ ]]; then
            echo "Adding $name range: $cidr"
            ipset add allowed-domains "$cidr" -exist 2>/dev/null || true
        else
            echo "WARNING: Invalid CIDR from $name: $cidr, skipping"
        fi
    done < <(echo "$cidrs" | $process_cmd)
}

# Process IP range sources from environment variable (JSON array)
# Format: [{"name":"github","url":"...","jq_filter":"...","required":true}, ...]
IP_RANGE_SOURCES="${CLAWKER_FIREWALL_IP_RANGE_SOURCES:-}"

if [ -n "$IP_RANGE_SOURCES" ] && [ "$IP_RANGE_SOURCES" != "[]" ]; then
    echo "Processing IP range sources..."

    # Count sources for progress
    source_count=$(echo "$IP_RANGE_SOURCES" | jq -r 'length')
    echo "Found $source_count IP range source(s) to process"

    echo "$IP_RANGE_SOURCES" | jq -c '.[]' | while read -r source; do
        name=$(echo "$source" | jq -r '.name // empty')
        url=$(echo "$source" | jq -r '.url // empty')
        jq_filter=$(echo "$source" | jq -r '.jq_filter // empty')
        required=$(echo "$source" | jq -r '.required // false')

        if [ -z "$name" ]; then
            echo "WARNING: IP range source missing name, skipping"
            continue
        fi

        # Use built-in URL/filter if not specified
        if [ -z "$url" ]; then
            url=$(get_builtin_url "$name")
        fi
        if [ -z "$jq_filter" ]; then
            jq_filter=$(get_builtin_filter "$name")
        fi

        # Default 'required' for github source
        if [ "$required" = "null" ] || [ -z "$required" ]; then
            if [ "$name" = "github" ]; then
                required="true"
            else
                required="false"
            fi
        fi

        if [ -z "$url" ]; then
            echo "WARNING: Unknown IP range source '$name' with no URL specified"
            continue
        fi

        if [ -z "$jq_filter" ]; then
            echo "WARNING: No jq filter for IP range source '$name'"
            continue
        fi

        process_ip_range_source "$name" "$url" "$jq_filter" "$required"
    done
else
    echo "No IP range sources configured"
fi

# Read configured domains from environment variable (JSON array)
# CLAWKER_FIREWALL_DOMAINS is set during Docker build from clawker.yaml config
if [ -n "${CLAWKER_FIREWALL_DOMAINS:-}" ] && [ "$CLAWKER_FIREWALL_DOMAINS" != "[]" ]; then
    echo "Processing configured firewall domains..."
    while read -r domain; do
        if [ -z "$domain" ]; then
            continue
        fi
        echo "Resolving $domain..."
        ips=$(dig +noall +answer A "$domain" | awk '$4 == "A" {print $5}')
        if [ -z "$ips" ]; then
            echo "WARNING: Failed to resolve $domain, skipping"
            continue
        fi

        while read -r ip; do
            if [[ ! "$ip" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
                echo "WARNING: Invalid IP from DNS for $domain: $ip, skipping"
                continue
            fi
            echo "Adding $ip for $domain"
            ipset add allowed-domains "$ip" -exist 2>/dev/null || true
        done < <(echo "$ips")
    done < <(echo "$CLAWKER_FIREWALL_DOMAINS" | jq -r '.[]')
else
    echo "No custom firewall domains configured"
fi

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
    "docker.io" \
    "pypi.org"; do
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

# Verify GitHub API access (only if github source was configured)
# Check if github is in the IP range sources
if [ -n "$IP_RANGE_SOURCES" ] && echo "$IP_RANGE_SOURCES" | jq -e '.[] | select(.name == "github")' >/dev/null 2>&1; then
    if ! curl --connect-timeout 5 https://api.github.com/zen >/dev/null 2>&1; then
        echo "ERROR: Firewall verification failed - unable to reach https://api.github.com"
        exit 1
    else
        echo "Firewall verification passed - able to reach https://api.github.com as expected"
    fi
fi
