#!/bin/sh
# git-credential-clawker - Git credential helper that forwards to host proxy
# Usage: git-credential-clawker <get|store|erase>
#
# This script reads git credential protocol input from stdin, converts it to JSON,
# POSTs to the clawker host proxy, and outputs the response in git credential format.

set -e

# Ensure host proxy is configured
if [ -z "$CLAWKER_HOST_PROXY" ]; then
    echo "error: CLAWKER_HOST_PROXY not set" >&2
    exit 1
fi

ACTION="$1"
if [ -z "$ACTION" ]; then
    echo "usage: git-credential-clawker <get|store|erase>" >&2
    exit 1
fi

# Read git credential protocol input from stdin
protocol=""
host=""
path=""
username=""
password=""

while IFS= read -r line; do
    [ -z "$line" ] && break
    case "$line" in
        protocol=*) protocol="${line#protocol=}" ;;
        host=*)     host="${line#host=}" ;;
        path=*)     path="${line#path=}" ;;
        username=*) username="${line#username=}" ;;
        password=*) password="${line#password=}" ;;
    esac
done

# Build JSON request - escape special characters for safety
escape_json() {
    printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g; s/	/\\t/g'
}

json_req=$(cat <<EOF
{
    "action": "$(escape_json "$ACTION")",
    "protocol": "$(escape_json "$protocol")",
    "host": "$(escape_json "$host")",
    "path": "$(escape_json "$path")",
    "username": "$(escape_json "$username")",
    "password": "$(escape_json "$password")"
}
EOF
)

# POST to host proxy
response=$(curl -s -X POST \
    -H "Content-Type: application/json" \
    -d "$json_req" \
    "$CLAWKER_HOST_PROXY/git/credential" 2>/dev/null)

# Check for curl failure
if [ -z "$response" ]; then
    exit 1
fi

# Parse response based on action
if [ "$ACTION" = "get" ]; then
    # Extract credentials from JSON response and output in git credential format
    # Check if success is true
    success=$(echo "$response" | grep -o '"success":[^,}]*' | cut -d: -f2 | tr -d ' ')

    if [ "$success" = "true" ]; then
        # Extract fields using portable sed - handle escaped quotes in values
        resp_protocol=$(echo "$response" | sed -n 's/.*"protocol":"\([^"]*\)".*/\1/p')
        resp_host=$(echo "$response" | sed -n 's/.*"host":"\([^"]*\)".*/\1/p')
        resp_username=$(echo "$response" | sed -n 's/.*"username":"\([^"]*\)".*/\1/p')
        resp_password=$(echo "$response" | sed -n 's/.*"password":"\([^"]*\)".*/\1/p')

        [ -n "$resp_protocol" ] && echo "protocol=$resp_protocol"
        [ -n "$resp_host" ] && echo "host=$resp_host"
        [ -n "$resp_username" ] && echo "username=$resp_username"
        [ -n "$resp_password" ] && echo "password=$resp_password"
    fi
fi

exit 0
