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

**Workaround:** set `agent.claude_code.mount_projects: false` in
`clawker.yaml` to disable the bind mount. Auto-memory and session
history will live inside the per-agent config volume instead of being
shared with host Claude Code.

`chown -R 1001:1001 ~/.claude/projects` and `--user $(id -u):$(id -g)`
are NOT viable workarounds:

- `chown` strips the host user's ownership of their own `~/.claude/`
  config dir, breaking host Claude Code.
- `--user` breaks the container entrypoint, which needs root for
  `chgrp /var/run/docker.sock`, `chown ~/.ssh`, and the `gosu`
  privilege drop to the `claude` user.
- The container UID `1001` is baked into the image at build time
  (`consts.ContainerUID`), so it cannot be overridden at runtime.

## env_file quoted values

`agent.env_file` may include quotes as part of the value if the `.env`
file uses quoted values. Workaround: use bare values.

```
# Safe
FOO_API_KEY=sk-abc123

# May break — quotes become part of the value
FOO_API_KEY="sk-abc123"
```
