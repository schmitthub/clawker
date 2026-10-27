#!/bin/sh
# host-open - Open URLs via the clawkerd host-action relay
# This script is used as the BROWSER environment variable so CLI tools
# (like Claude Code) can open URLs on the host machine.
#
# For OAuth flows with a localhost redirect_uri (or callback) parameter:
# 1. One POST /callback/register sends the auth URL to the host proxy.
#    The host proxy checks the URL, binds the redirect port on the host,
#    and opens the browser.
# 2. callback-forwarder polls for the captured callback and replays it
#    to the local listener in this container.

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

# Decode the URL escapes that a redirect URI prefix uses (":" and "/")
url_decode() {
    printf '%s' "$1" | sed 's/%3[Aa]/:/g; s#%2[Ff]#/#g'
}

# Function to extract value from URL parameter
get_param() {
    echo "$1" | grep -oE "[?&]$2=[^&]+" | head -1 | sed "s/^.$2=//"
}

# Succeeds if the URL has a redirect_uri or callback parameter to localhost.
# The host proxy does the full redirect check.
is_oauth_url() {
    redirect_uri=$(get_param "$1" "redirect_uri")
    if [ -z "$redirect_uri" ]; then
        redirect_uri=$(get_param "$1" "callback")
    fi
    case "$(url_decode "$redirect_uri")" in
        http://localhost:*|http://127.0.0.1:*) return 0 ;;
    esac
    return 1
}

# POST a JSON body to the relay; print the response body.
post_json() {
    curl -sf -X POST "$CLAWKER_HOST_PROXY$1" \
        -H "Content-Type: application/json" \
        -d "$2"
}

open_oauth() {
    body=$(jq -nc --arg u "$1" '{authUrl: $u, ttlSeconds: 300}')
    response=$(post_json /callback/register "$body")
    session_id=$(printf '%s' "$response" | jq -r '.sessionId // empty' 2>/dev/null)
    port=$(printf '%s' "$response" | jq -r '.port // empty' 2>/dev/null)

    if [ -z "$session_id" ] || [ -z "$port" ]; then
        echo "Error: Failed to start the OAuth flow via the host proxy" >&2
        echo "Response: $response" >&2
        echo "" >&2
        echo "Possible causes:" >&2
        echo "  - Host proxy is not running or not enabled for this agent" >&2
        echo "  - The auth URL is not allowed by the firewall" >&2
        echo "  - The callback port is already in use on the host" >&2
        exit 1
    fi

    if ! command -v callback-forwarder >/dev/null 2>&1; then
        echo "Error: callback-forwarder not found in PATH" >&2
        echo "OAuth callback cannot be forwarded. Authentication will fail." >&2
        echo "" >&2
        echo "The clawker image may be corrupted. Try rebuilding:" >&2
        echo "  clawker build --no-cache" >&2
        exit 1
    fi
    CALLBACK_SESSION="$session_id" CALLBACK_PORT="$port" callback-forwarder &
}

open_url() {
    response=$(post_json /open/url "$(jq -nc --arg u "$1" '{url: $u}')")
    if [ "$(printf '%s' "$response" | jq -r '.success' 2>/dev/null)" != "true" ]; then
        echo "Failed to open URL via host proxy: $response" >&2
        return 1
    fi
}

if is_oauth_url "$URL"; then
    open_oauth "$URL"
else
    open_url "$URL"
fi
