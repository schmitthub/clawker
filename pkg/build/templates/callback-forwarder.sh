#!/bin/sh
# callback-forwarder - Poll host proxy and forward OAuth callbacks
#
# This script polls the host proxy for captured OAuth callback data and
# forwards it to the local HTTP server (Claude Code's callback listener).
#
# Environment variables:
#   CLAWKER_HOST_PROXY: Host proxy URL (required)
#   CALLBACK_SESSION: Session ID to poll for (required)
#   CALLBACK_PORT: Local port to forward callback to (required)
#
# Usage:
#   callback-forwarder
#   callback-forwarder -v  # verbose mode

set -e

VERBOSE=false
TIMEOUT=${TIMEOUT:-300}
POLL_INTERVAL=${POLL_INTERVAL:-2}
CLEANUP=${CLEANUP:-true}

# Parse flags
while [ $# -gt 0 ]; do
    case "$1" in
        -v|--verbose)
            VERBOSE=true
            shift
            ;;
        *)
            shift
            ;;
    esac
done

# Validate environment
if [ -z "$CLAWKER_HOST_PROXY" ]; then
    CLAWKER_HOST_PROXY="http://host.docker.internal:18374"
fi

if [ -z "$CALLBACK_SESSION" ]; then
    echo "Error: CALLBACK_SESSION not set" >&2
    exit 1
fi

if [ -z "$CALLBACK_PORT" ]; then
    echo "Error: CALLBACK_PORT not set" >&2
    exit 1
fi

# Remove trailing slash from proxy URL
CLAWKER_HOST_PROXY="${CLAWKER_HOST_PROXY%/}"

DATA_URL="${CLAWKER_HOST_PROXY}/callback/${CALLBACK_SESSION}/data"
DELETE_URL="${CLAWKER_HOST_PROXY}/callback/${CALLBACK_SESSION}"

if [ "$VERBOSE" = true ]; then
    echo "Waiting for OAuth callback..." >&2
    echo "  Session: $CALLBACK_SESSION" >&2
    echo "  Port: $CALLBACK_PORT" >&2
    echo "  Proxy: $CLAWKER_HOST_PROXY" >&2
    echo "  Timeout: ${TIMEOUT}s" >&2
fi

# Calculate deadline
START_TIME=$(date +%s)
DEADLINE=$((START_TIME + TIMEOUT))

# Poll for callback data
while [ $(date +%s) -lt $DEADLINE ]; do
    response=$(curl -sf "$DATA_URL" 2>&1) || {
        if [ "$VERBOSE" = true ]; then
            echo "Poll error, retrying..." >&2
        fi
        sleep $POLL_INTERVAL
        continue
    }

    # Check for 404 (session not found)
    if echo "$response" | grep -q '"error".*not found'; then
        echo "Error: session not found or expired" >&2
        exit 1
    fi

    # Check if callback received
    received=$(echo "$response" | jq -r '.received // false')

    if [ "$received" != "true" ]; then
        # No callback yet, keep polling
        sleep $POLL_INTERVAL
        continue
    fi

    # Callback received! Extract data
    if [ "$VERBOSE" = true ]; then
        echo "Callback received, forwarding to localhost:$CALLBACK_PORT" >&2
    fi

    # Extract callback data
    method=$(echo "$response" | jq -r '.callback.method // "GET"')
    path=$(echo "$response" | jq -r '.callback.path // "/"')
    query=$(echo "$response" | jq -r '.callback.query // ""')
    body=$(echo "$response" | jq -r '.callback.body // ""')

    # Build local URL
    local_url="http://localhost:${CALLBACK_PORT}${path}"
    if [ -n "$query" ]; then
        local_url="${local_url}?${query}"
    fi

    # Forward the callback
    if [ -n "$body" ]; then
        forward_response=$(curl -sf -X "$method" "$local_url" -d "$body" 2>&1) || {
            echo "Error forwarding callback to $local_url" >&2
            # Don't exit with error - callback was captured
        }
    else
        forward_response=$(curl -sf -X "$method" "$local_url" 2>&1) || {
            echo "Error forwarding callback to $local_url" >&2
            # Don't exit with error - callback was captured
        }
    fi

    if [ "$VERBOSE" = true ]; then
        echo "Callback forwarded successfully" >&2
    fi

    # Cleanup session
    if [ "$CLEANUP" = true ]; then
        curl -sf -X DELETE "$DELETE_URL" >/dev/null 2>&1 || true
    fi

    exit 0
done

echo "Timeout waiting for OAuth callback" >&2
exit 1
