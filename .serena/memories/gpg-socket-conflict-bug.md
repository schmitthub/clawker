# GPG Socket Conflict Bug [RESOLVED]

**Status:** RESOLVED — Multi-layered fix in `clawker-socket-server` (branch `a/gpg-socket-conflict`)
**Discovered:** 2026-02-05 during bridge lifecycle investigation

## Problem

Inside containers, `gpg-agent --daemon` auto-starts and steals the `S.gpg-agent` Unix socket from the bridge's `clawker-socket-server`. After this happens, GPG operations inside the container talk to the local gpg-agent (which has no keys) instead of being forwarded through the bridge to the host's GPG agent.

## Fix Applied (Multi-Layered)

Three layers of protection against gpg-agent socket conflict:

| Layer | Mechanism | Purpose |
|-------|-----------|---------|
| 1 | `gpg.conf` with `no-autostart` | Prevents GPG from spawning gpg-agent on any operation |
| 2 | `gpgconf --kill gpg-agent` after setup | Kills any pre-existing agent (GPG's sanctioned mechanism) |
| 3 | `gpg-agent.conf` with `no-grab`, `disable-scdaemon` | Sensible container defaults (do NOT prevent socket binding) |

**Key insight:** GnuPG 2.1+ mandates the standard socket — no `gpg-agent.conf` directive can prevent socket binding. Layer 3 is honest about this (comments say "sensible container defaults" not "defense-in-depth").

### Additional improvements:
- **File logging:** socket-server logs to `/var/log/clawker/socket-server.log` alongside stderr (1MB rotation)
- **Error visibility:** `getTargetUserFromPath()` now logs on all 4 failure paths; `os.Remove()` logs non-NotExist errors
- **`logf()`/`logln()`** helpers replace all direct `fmt.Fprintf(os.Stderr)` calls

### Test coverage:
- STEP 5b: `gpg.conf` contains `no-autostart`
- STEP 5c: `gpg-agent.conf` contains `no-grab` and `disable-scdaemon`
- STEP 6b: no competing `gpg-agent` process (now `require.Equal`, hard-fail)
- STEP 10: log file exists with expected lifecycle messages
