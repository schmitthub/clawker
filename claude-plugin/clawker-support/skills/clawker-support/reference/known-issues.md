# Known Issues

Active bugs and workarounds. Check this before deep-diving into
troubleshooting — the user's problem may already be documented here.

## ~/.claude/projects bind mount — Linux UID mismatch

When `agent.claude_code.mount_projects` is enabled (default) on a Linux
host whose login UID is not `1001`, the container's `claude` user may
not be able to write to host-owned `~/.claude/projects/` files. Symptom:
session history and auto-memory fail to persist across runs. macOS hosts
are unaffected (Docker Desktop's virtiofs translates ownership).

clawker emits a warning at container create time (printed to stderr).
Workarounds:

- `chown -R 1001:1001 ~/.claude/projects` on the host
- run with `--user $(id -u):$(id -g)` so container writes match host UID
- set `agent.claude_code.mount_projects: false` in `clawker.yaml`

## env_file quoted values

`agent.env_file` may include quotes as part of the value if the `.env`
file uses quoted values. Workaround: use bare values.

```
# Safe
FOO_API_KEY=sk-abc123

# May break — quotes become part of the value
FOO_API_KEY="sk-abc123"
```
