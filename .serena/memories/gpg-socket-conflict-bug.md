# GPG Socket Conflict Bug [OPEN]

**Status:** OPEN - Not yet addressed
**Discovered:** 2026-02-05 during bridge lifecycle investigation
**Related:** Bridge lifecycle fix (PR on branch `a/bridge-bug`)

## Bridge Lifecycle Status (a/bridge-bug branch)

**Completed layers:**
- Layer 1: Docker events stream in bridge daemon — `watchContainerEvents` subscribes to `die` events, auto-stops bridge on container death
- Layer 2: Stop/rm hooks — `container stop` and `container rm` call `StopBridge` before Docker operation
- Layer 3: EnsureBridge container inspect — future work (separate PR)

## Problem

Inside containers, `gpg-agent --daemon` auto-starts and steals the `S.gpg-agent` Unix socket from the bridge's `clawker-socket-server`. After this happens, GPG operations inside the container talk to the local gpg-agent (which has no keys) instead of being forwarded through the bridge to the host's GPG agent.

## Evidence

`ss -lnx` inside container shows TWO listeners on `/home/claude/.gnupg/S.gpg-agent`:
- inode 131205 (backlog 64) — `gpg-agent --daemon` (PID 11185, started 04:31)
- inode 119855 (backlog 4096) — `clawker-socket-server` (PID 24, started 04:29)

The socket-server creates the socket first, then gpg-agent replaces it when auto-started by the first GPG operation (likely the pubkey import triggered by the bridge).

## Impact

- `gpg --list-secret-keys` returns empty inside container (connects to local agent, no keys)
- `ssh-add -l` works fine (SSH agent forwarding unaffected)
- `gpg-connect-agent 'KEYINFO --list' '/bye'` returns `OK` with no keys (local agent)

## Potential Fixes

1. **Prevent gpg-agent auto-start:** Add `no-autostart` to GPG config inside container, or set `GNUPGHOME` env to prevent agent spawning
2. **Kill competing gpg-agent:** Socket-server or entrypoint script kills any auto-started gpg-agent after creating sockets
3. **Use abstract sockets or alternative paths:** Avoid the standard `S.gpg-agent` path that gpg-agent auto-binds to
4. **Socket-server resilience:** Detect when socket is stolen and re-bind

## Files Likely Involved

- Container-side: `internal/hostproxy/internals/cmd/clawker-socket-server/main.go` (socket-server binary code)
- Container entrypoint scripts: `internal/hostproxy/internals/` (container setup)
- Bridge start: `internal/socketbridge/bridge.go` (pubkey sending triggers gpg-agent)
