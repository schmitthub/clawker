#!/usr/bin/env bash
#
# clawker container entrypoint. Three jobs:
#
#   1. Launch /usr/local/bin/clawkerd in the background.
#   2. Block on a fifo until CP-driven init signals AgentReady.
#   3. Drop privileges to the unprivileged user and exec the user CMD.
#
# Everything else (config seed, git config, ssh known_hosts, firewall
# readiness wait, post-init script execution) moved to the CP-driven
# InitSequence dispatched over the ClawkerdService.Session stream — see
# internal/controlplane/agent/init.go.
#
# The fifo is the synchronization primitive. The entrypoint creates it
# (root, 0600), backgrounds clawkerd, and `cat`s it — kernel-level
# blocking read. CP runs the init plan against clawkerd; the terminal
# AgentReady command opens the fifo O_WRONLY|O_NONBLOCK on the daemon
# side and writes one byte. The kernel unblocks both ends; the
# entrypoint proceeds to exec gosu CMD.
#
# Reset semantics on container restart: /run is the writable layer (not
# tmpfs by default), so a stale fifo and ready marker from the prior
# boot survive `docker stop` + `docker start`. We `rm -f` both at the
# top — neither carries any cross-boot meaning.
#
# Failure modes:
#   - clawkerd binary missing → background exits immediately, fifo never
#     written, timeout fires, container exits 1. clawkerd's stderr
#     (Go runtime panic / logger init failure) is in the same docker
#     stream as the timeout line — `docker logs <id>` shows both.
#   - CP unreachable / init step fails → clawkerd is alive but no
#     AgentReady arrives, timeout fires, container exits 1.

set -e

rm -f /run/clawker/agent.fifo /var/run/clawker/ready
mkdir -p /run/clawker /var/run/clawker
mkfifo -m 0600 /run/clawker/agent.fifo

/usr/local/bin/clawkerd &

# 660s default = 600s post-init step ceiling (initStepTimeoutPostInit
# in internal/controlplane/agent/init.go) + 60s slack for the other six
# init steps. Keep these two values aligned: an entrypoint timeout
# below the post-init server-side timeout would kill the container
# while CP is still patiently waiting for a slow user post-init.
timeout "${CLAWKER_INIT_TIMEOUT:-660}" cat /run/clawker/agent.fifo >/dev/null \
    || { echo "[clawker] init timeout (${CLAWKER_INIT_TIMEOUT:-660}s) — clawkerd output above" >&2; exit 1; }

# Docker HEALTHCHECK + external readiness probes look for this marker.
# Cleared at the top of this script so a stale marker from the prior
# boot does not falsely report ready before init completes on restart.
touch /var/run/clawker/ready

# Default the user CMD to `claude` if no command specified or first
# argument is a flag (preserves the docker-image convention for
# `docker run <image> --help` working).
if [ "${1#-}" != "${1}" ] || [ -z "$(command -v "${1}" 2>/dev/null)" ]; then
    set -- claude "$@"
fi

exec gosu "${CLAWKER_USER:-claude}" "$@"
