#!/usr/bin/env bash
# localenv-dotenv.sh — Update .env with CLAWKER_*_DIR vars.
# Called by `make localenv`. Preserves all non-CLAWKER_*_DIR entries.
#
# Usage: localenv-dotenv.sh KEY=VALUE [KEY=VALUE ...]

set -euo pipefail

DOTENV=".env"

# Collect new CLAWKER vars from args
declare -a new_vars=("$@")

if [[ -f "$DOTENV" ]]; then
    # Strip existing CLAWKER_*_DIR lines, preserve everything else
    tmp=$(mktemp)
    grep -v '^CLAWKER_.*_DIR=' "$DOTENV" > "$tmp" || true
    # Remove trailing blank lines
    sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' "$tmp" > "$DOTENV"
    rm -f "$tmp"
else
    touch "$DOTENV"
fi

# Append new vars
for var in "${new_vars[@]}"; do
    echo "$var" >> "$DOTENV"
done
