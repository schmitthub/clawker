#!/usr/bin/env bash
#
# clawker container entrypoint. Three jobs:
#
#   1. Launch /usr/local/bin/clawkerd in the background.
#   2. Block on consts.AgentReadyFifo until CP-driven init dispatches
#      its terminal AgentReady command (see init.go).
#   3. Drop privileges to consts.ContainerUser and exec the user CMD.
#
# /run is a writable container layer; stale fifo + ready marker from a
# prior boot must be cleared because neither carries cross-boot
# meaning. Paths in this script mirror consts (HostGitConfigStagingPath
# stays out of the loop; only the fifo + ready-marker live here).
#
# Failure modes:
#   - clawkerd binary missing → background exits immediately, fifo
#     never written, timeout fires, container exits 1.
#   - CP unreachable / init step fails → clawkerd alive but no
#     AgentReady arrives, timeout fires, container exits 1.
#
# Default timeout 660s = 600s post-init ceiling
# (consts.InitStepTimeoutPostInitSeconds) + 60s slack. Hand-
# maintained alignment — bumping init.go's post-init ceiling
# requires bumping the literal below in the same change.

set -e

rm -f /run/clawker/agent.fifo /var/run/clawker/ready
mkdir -p /run/clawker /var/run/clawker
mkfifo -m 0600 /run/clawker/agent.fifo

/usr/local/bin/clawkerd &

timeout "${CLAWKER_INIT_TIMEOUT:-660}" cat /run/clawker/agent.fifo >/dev/null \
    || { echo "[clawker] init timeout (${CLAWKER_INIT_TIMEOUT:-660}s) — clawkerd output above" >&2; exit 1; }

# consts.ReadyMarkerPath. Docker HEALTHCHECK + external readiness
# probes look for this. Cleared above so a stale marker doesn't
# falsely report ready before init completes on restart.
touch /var/run/clawker/ready

# Default the user CMD to `claude` if no command specified or first
# argument is a flag (preserves the docker-image convention for
# `docker run <image> --help` working).
if [ "${1#-}" != "${1}" ] || [ -z "$(command -v "${1}" 2>/dev/null)" ]; then
    set -- claude "$@"
fi

exec gosu "${CLAWKER_USER:-claude}" "$@"
