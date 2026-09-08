#!/usr/bin/env bash
# Read shell words and heredocs without executing the supplied command.
set -euo pipefail
exec python3 "$(git rev-parse --show-toplevel)/.agents/hooks/go_commands.py"
