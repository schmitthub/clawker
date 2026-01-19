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

# Build JSON request using jq for proper escaping
json_req=$(jq -n \
    --arg action "$ACTION" \
    --arg protocol "$protocol" \
    --arg host "$host" \
    --arg path "$path" \
    --arg username "$username" \
    --arg password "$password" \
    '{action: $action, protocol: $protocol, host: $host, path: $path, username: $username, password: $password}')

# POST to host proxy with proper error handling
curl_stderr=$(mktemp)
trap 'rm -f "$curl_stderr"' EXIT

response=$(curl -s -w '\n%{http_code}' -X POST \
    -H "Content-Type: application/json" \
    -d "$json_req" \
    "$CLAWKER_HOST_PROXY/git/credential" 2>"$curl_stderr")
curl_exit=$?

if [ $curl_exit -ne 0 ]; then
    echo "error: failed to contact host proxy: $(cat "$curl_stderr")" >&2
    exit 1
fi

# Extract HTTP status code (last line) and response body
http_code=$(printf '%s' "$response" | tail -n1)
response_body=$(printf '%s' "$response" | sed '$d')

# Check for HTTP errors
if [ "$http_code" -ge 400 ] 2>/dev/null; then
    error_msg=$(printf '%s' "$response_body" | jq -r '.error // "request failed"' 2>/dev/null || echo "request failed with status $http_code")
    echo "error: $error_msg" >&2
    exit 1
fi

# Parse response based on action
if [ "$ACTION" = "get" ]; then
    # Check if success is true using jq for proper JSON parsing
    success=$(printf '%s' "$response_body" | jq -r '.success // empty' 2>/dev/null)

    if [ "$success" = "true" ]; then
        # Extract fields using jq and output in git credential format
        printf '%s' "$response_body" | jq -r '
            to_entries[] |
            select(.key != "success" and .key != "error" and .value != "" and .value != null) |
            "\(.key)=\(.value)"' 2>/dev/null
        exit 0
    else
        error=$(printf '%s' "$response_body" | jq -r '.error // "credential lookup failed"' 2>/dev/null)
        echo "error: $error" >&2
        exit 1
    fi
fi

exit 0
