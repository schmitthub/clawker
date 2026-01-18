#!/bin/sh
# host-open - Open URLs via clawker host proxy
# This script is used as the BROWSER environment variable so CLI tools
# (like Claude Code) can open URLs on the host machine.
#
# For OAuth flows, it automatically:
# 1. Detects localhost callback URLs in the request
# 2. Registers a callback session with the host proxy
# 3. Rewrites the callback URL to use the proxy
# 4. Spawns callback-forwarder to forward the OAuth response

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

# Function to URL decode a string
url_decode() {
    printf '%s' "$1" | sed 's/+/ /g; s/%\([0-9A-Fa-f][0-9A-Fa-f]\)/\\x\1/g' | xargs -0 printf '%b'
}

# Function to URL encode a string
url_encode() {
    printf '%s' "$1" | jq -sRr @uri
}

# Function to extract value from URL parameter
get_param() {
    local url="$1"
    local param="$2"
    # Extract parameter value from URL
    echo "$url" | grep -oE "${param}=[^&]+" | head -1 | sed "s/${param}=//"
}

# Check if URL contains a localhost callback that we need to proxy
detect_oauth_callback() {
    local url="$1"

    # Look for redirect_uri or callback parameters pointing to localhost
    local redirect_uri=$(get_param "$url" "redirect_uri")
    if [ -z "$redirect_uri" ]; then
        redirect_uri=$(get_param "$url" "callback")
    fi
    if [ -z "$redirect_uri" ]; then
        return 1
    fi

    # URL decode the redirect_uri
    redirect_uri=$(url_decode "$redirect_uri")

    # Check if it points to localhost
    case "$redirect_uri" in
        http://localhost:*|http://127.0.0.1:*)
            echo "$redirect_uri"
            return 0
            ;;
    esac

    return 1
}

# Extract port and path from a localhost URL
parse_localhost_url() {
    local url="$1"

    # Extract port: match localhost:PORT or 127.0.0.1:PORT
    local port=$(echo "$url" | sed -n 's|http://\(localhost\|127\.0\.0\.1\):\([0-9]*\).*|\2|p')

    # Extract path: everything after the port
    local path=$(echo "$url" | sed -n 's|http://\(localhost\|127\.0\.0\.1\):[0-9]*\(/[^?]*\).*|\2|p')

    # Default to /callback if no path
    if [ -z "$path" ]; then
        path="/callback"
    fi

    echo "$port $path"
}

# Register callback with host proxy
register_callback() {
    local port="$1"
    local path="$2"

    local response=$(curl -sf -X POST "$CLAWKER_HOST_PROXY/callback/register" \
        -H "Content-Type: application/json" \
        -d "{\"port\": $port, \"path\": \"$path\", \"timeout_seconds\": 300}" 2>&1)

    if [ $? -ne 0 ]; then
        echo "" ""
        return 1
    fi

    local session_id=$(echo "$response" | jq -r '.session_id // empty')
    local proxy_base=$(echo "$response" | jq -r '.proxy_callback_base // empty')

    if [ -z "$session_id" ] || [ -z "$proxy_base" ]; then
        echo "" ""
        return 1
    fi

    echo "$session_id $proxy_base"
}

# Rewrite URL with new callback
rewrite_oauth_url() {
    local original_url="$1"
    local old_callback="$2"
    local new_callback="$3"

    # URL encode both callbacks for safe replacement
    local old_encoded=$(url_encode "$old_callback")
    local new_encoded=$(url_encode "$new_callback")

    # Replace the old callback with the new one
    # Try both encoded and unencoded versions
    local new_url="$original_url"
    new_url=$(echo "$new_url" | sed "s|redirect_uri=$old_encoded|redirect_uri=$new_encoded|g")
    new_url=$(echo "$new_url" | sed "s|redirect_uri=$old_callback|redirect_uri=$new_encoded|g")

    echo "$new_url"
}

# Main logic
main() {
    local original_callback
    original_callback=$(detect_oauth_callback "$URL")

    if [ -n "$original_callback" ]; then
        # This is an OAuth URL with a localhost callback
        # We need to intercept the callback

        local parsed
        parsed=$(parse_localhost_url "$original_callback")
        local port=$(echo "$parsed" | cut -d' ' -f1)
        local path=$(echo "$parsed" | cut -d' ' -f2-)

        if [ -z "$port" ]; then
            # Could not parse port, fall through to regular handling
            open_url "$URL"
            return $?
        fi

        # Register callback session
        local registered
        registered=$(register_callback "$port" "$path")
        local session_id=$(echo "$registered" | cut -d' ' -f1)
        local proxy_base=$(echo "$registered" | cut -d' ' -f2)

        if [ -z "$session_id" ]; then
            echo "Error: Failed to register OAuth callback session with host proxy" >&2
            echo "" >&2
            echo "The authentication callback cannot be intercepted." >&2
            echo "Possible causes:" >&2
            echo "  - Host proxy is not running" >&2
            echo "  - Host proxy is unreachable from container" >&2
            echo "" >&2
            echo "Try:" >&2
            echo "  1. Restart the container" >&2
            echo "  2. Use API key authentication instead" >&2
            exit 1
        fi

        # Build new callback URL pointing to proxy
        local new_callback="${proxy_base}${path}"

        # Rewrite the OAuth URL
        local rewritten_url
        rewritten_url=$(rewrite_oauth_url "$URL" "$original_callback" "$new_callback")

        # Spawn callback-forwarder in background
        if command -v callback-forwarder >/dev/null 2>&1; then
            CALLBACK_SESSION="$session_id" CALLBACK_PORT="$port" callback-forwarder &
        else
            echo "Error: callback-forwarder not found in PATH" >&2
            echo "OAuth callback will not be forwarded. Authentication may fail." >&2
        fi

        # Open the rewritten URL
        open_url "$rewritten_url"
        return $?
    fi

    # Not an OAuth URL, just open it normally
    open_url "$URL"
    return $?
}

# Open URL via host proxy
open_url() {
    local url="$1"

    # Escape the URL for JSON (handle quotes and backslashes)
    local escaped_url=$(printf '%s' "$url" | sed 's/\\/\\\\/g; s/"/\\"/g')

    # Send request to host proxy to open the URL
    local response=$(curl -sf -X POST "$CLAWKER_HOST_PROXY/open/url" \
        -H "Content-Type: application/json" \
        -d "{\"url\": \"$escaped_url\"}" 2>&1)

    if [ $? -ne 0 ]; then
        echo "Failed to open URL via host proxy" >&2
        echo "Response: $response" >&2
        return 1
    fi

    # Check if the response indicates success
    if echo "$response" | grep -q '"success":true'; then
        return 0
    else
        echo "Failed to open URL: $response" >&2
        return 1
    fi
}

main
