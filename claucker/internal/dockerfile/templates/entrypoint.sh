#!/bin/bash
set -e

# Initialize firewall if script exists and we have capabilities
if [ -x /usr/local/bin/init-firewall.sh ] && [ -f /proc/net/ip_tables_names ]; then
    sudo /usr/local/bin/init-firewall.sh 2>/dev/null || true
fi

# If first argument starts with "-" or isn't a command, prepend "claude"
if [ "${1#-}" != "${1}" ] || [ -z "$(command -v "${1}" 2>/dev/null)" ]; then
    set -- claude "$@"
fi

exec "$@"
