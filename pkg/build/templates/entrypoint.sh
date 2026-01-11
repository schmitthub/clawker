#!/bin/bash
set -e

# Initialize firewall if script exists and we have capabilities
if [ -x /usr/local/bin/init-firewall.sh ] && [ -f /proc/net/ip_tables_names ]; then
    sudo /usr/local/bin/init-firewall.sh 2>/dev/null || true
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

# If first argument starts with "-" or isn't a command, prepend "claude"
if [ "${1#-}" != "${1}" ] || [ -z "$(command -v "${1}" 2>/dev/null)" ]; then
    set -- claude "$@"
fi

exec "$@"
