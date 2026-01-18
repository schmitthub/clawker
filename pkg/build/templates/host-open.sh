#!/bin/sh
# host-open - Open URLs via clawker host proxy
# This script is used as the BROWSER environment variable so CLI tools
# (like Claude Code) can open URLs on the host machine.

URL="$1"
if [ -z "$URL" ]; then
    echo "Usage: host-open <url>" >&2
    exit 1
fi

if [ -z "$CLAWKER_HOST_PROXY" ]; then
    echo "Error: CLAWKER_HOST_PROXY not set" >&2
    echo "This script requires the clawker host proxy to be running." >&2
    exit 1
fi

# Escape the URL for JSON (handle quotes and backslashes)
escaped_url=$(printf '%s' "$URL" | sed 's/\\/\\\\/g; s/"/\\"/g')

# Send request to host proxy to open the URL
response=$(curl -sf -X POST "$CLAWKER_HOST_PROXY/open/url" \
    -H "Content-Type: application/json" \
    -d "{\"url\": \"$escaped_url\"}" 2>&1)

if [ $? -ne 0 ]; then
    echo "Failed to open URL via host proxy" >&2
    echo "Response: $response" >&2
    exit 1
fi

# Check if the response indicates success
if echo "$response" | grep -q '"success":true'; then
    exit 0
else
    echo "Failed to open URL: $response" >&2
    exit 1
fi
