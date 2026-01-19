#!/bin/bash
set -e

# Initialize firewall if script exists and we have capabilities
if [ -x /usr/local/bin/init-firewall.sh ] && [ -f /proc/net/ip_tables_names ]; then
    if ! sudo /usr/local/bin/init-firewall.sh 2>&1; then
        echo "Warning: Firewall initialization failed" >&2
    fi
fi

# Initialize config volume with image defaults if missing
INIT_DIR="$HOME/.claude-init"
CONFIG_DIR="$HOME/.claude"

if [ -d "$INIT_DIR" ]; then
    # Copy statusline.sh if missing
    [ ! -f "$CONFIG_DIR/statusline.sh" ] && cp "$INIT_DIR/statusline.sh" "$CONFIG_DIR/statusline.sh"

    # Initialize or merge settings.json
    if [ ! -f "$CONFIG_DIR/settings.json" ]; then
        cp "$INIT_DIR/settings.json" "$CONFIG_DIR/settings.json"
    else
        # Merge: init defaults first, user settings override
        jq -s '.[0] * .[1]' "$INIT_DIR/settings.json" "$CONFIG_DIR/settings.json" > "$CONFIG_DIR/settings.json.tmp" 2>/dev/null \
            && mv "$CONFIG_DIR/settings.json.tmp" "$CONFIG_DIR/settings.json" \
            || true
    fi
fi

# Setup git configuration from host
HOST_GITCONFIG="/tmp/host-gitconfig"
if [ -f "$HOST_GITCONFIG" ]; then
    # Copy host gitconfig, filtering out credential.helper lines (we configure our own)
    if ! grep -v '^[[:space:]]*helper[[:space:]]*=' "$HOST_GITCONFIG" > "$HOME/.gitconfig.tmp" 2>&1; then
        echo "Warning: Failed to filter host gitconfig" >&2
        cp "$HOST_GITCONFIG" "$HOME/.gitconfig" 2>/dev/null || true
    elif [ -s "$HOME/.gitconfig.tmp" ]; then
        mv "$HOME/.gitconfig.tmp" "$HOME/.gitconfig"
    else
        rm -f "$HOME/.gitconfig.tmp"
    fi
fi

# Configure git credential helper if HTTPS forwarding is enabled
if [ -n "$CLAWKER_HOST_PROXY" ] && [ "$CLAWKER_GIT_HTTPS" = "true" ]; then
    if ! git config --global credential.helper clawker 2>&1; then
        echo "Warning: Failed to configure git credential helper" >&2
    fi
fi

# If first argument starts with "-" or isn't a command, prepend "claude"
if [ "${1#-}" != "${1}" ] || [ -z "$(command -v "${1}" 2>/dev/null)" ]; then
    set -- claude "$@"
fi

exec "$@"
