#!/bin/bash
set -e

# Start clawkerd in the background (runs as root).
# Init orchestration happens via gRPC — the control plane receives
# READY events directly from clawkerd, no file polling needed.
clawkerd &

# Drop privileges and exec the main command as the claude user.
exec su-exec claude "$@"
