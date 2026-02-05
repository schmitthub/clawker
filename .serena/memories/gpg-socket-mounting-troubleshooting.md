# GPG Socket Mounting Troubleshooting

## Investigation Date: 2026-02-05 (Branch: a/run-worktree-bug)

## Problem Statement

`git commit -m "test"` fails inside clawker containers with:
```
error: gpg failed to sign the data
fatal: failed to write commit object
```

## Key Findings

### 1. Docker Desktop Socket Mounting Works ✅

Contrary to past session documentation, Docker Desktop 4.x+ with VirtioFS correctly mounts Unix sockets.

**Evidence:**
```bash
# Socket mounts as type 'socket' (s)
docker run --rm -v ~/.gnupg/S.gpg-agent.extra:/tmp/gpg.sock alpine stat /tmp/gpg.sock
# Output: srw-rw-rw- ... socket

# Basic connectivity works
docker run --rm -v ~/.gnupg/S.gpg-agent.extra:/tmp/gpg.sock alpine sh -c '
  apk add gnupg && echo "GETINFO version" | gpg-connect-agent -S /tmp/gpg.sock /bye'
# Output: D 2.4.9 / OK
```

### 2. Extra Socket Restriction Mode ❌

The `S.gpg-agent.extra` socket is designed for **restricted remote access** and blocks key operations:

| Command | Extra Socket Result | Main Socket Result |
|---------|--------------------|--------------------|
| `GETINFO version` | ✅ D 2.4.9 OK | ✅ D 2.4.9 OK |
| `KEYINFO --list` | ❌ Empty (just OK) | ✅ Lists keygrips |
| `HAVEKEY <keygrip>` | ❌ ERR 67108881 No secret key | ✅ OK |
| Signing | ❌ No secret key | ⚠️ Needs more setup |

**The extra socket fundamentally cannot be used for signing operations.**

### 3. Host Uses Modern Keyboxd Architecture

The host GPG (2.4.9) uses `use-keyboxd` (checked via `~/.gnupg/common.conf`):

```bash
ls -la ~/.gnupg/
# Shows:
# - pubring.kbx is a DIRECTORY, not a file
# - S.keyboxd socket exists
# - common.conf contains "use-keyboxd"
```

Keys are managed by the `keyboxd` daemon, not by reading pubring.kbx directly.

### 4. Current Clawker Implementation

**File:** `internal/workspace/gpg.go`

- Uses `S.gpg-agent.extra` socket (wrong socket for signing)
- Mounts to `/home/claude/.gnupg/S.gpg-agent`
- `UseGPGAgentProxy()` returns `false` (proxy disabled)
- No handling for keyboxd

### 5. Container State When Failing

Inside running clawker container:
```bash
ls -la /home/claude/.gnupg/
# S.gpg-agent exists as socket (srw-rw-rw-) owned by root:root

gpg-connect-agent 'GETINFO version' '/bye'
# gpg-connect-agent: connection to agent is in restricted mode
# D 2.4.9 / OK

gpg --list-secret-keys
# (empty - no keys visible)

git config --global --list | grep gpg
# user.signingkey=9FBA9516F54DE00F
# commit.gpgsign=true
```

### 6. Host Key Details

```bash
gpg --with-keygrip --list-secret-keys
# sec   ed25519 2025-11-06 [SC]
#       9581ED65E697F1B46745FF839FBA9516F54DE00F
#       Keygrip = 4AD8D8978B64863D552B7BBB052C8276AEF0F6DC
```

## What Past Sessions Got Wrong

1. **Assumed socket mounting was broken** - Skipped macOS tests citing "Docker Desktop issues"
2. **Tested connectivity, not functionality** - `GETINFO version` works but `HAVEKEY` fails
3. **Documented success prematurely** - Memory file claimed "VirtioFS handles sockets correctly"
4. **Ignored GPG architecture** - Didn't account for keyboxd setup
5. **No end-to-end tests** - Never tested actual `git commit` signing

## Existing Test Files

- `test/internals/gpgagent_test.go` - Superficial tests, key ones skipped on macOS
- `internal/workspace/gpg.go` - Implementation (uses wrong socket)
- `internal/workspace/gpg_test.go` - Unit tests
- `internal/hostproxy/gpg_agent.go` - Proxy fallback (currently disabled)

## Solution Direction (For Future Implementation)

Options to explore:
1. Use main socket (`S.gpg-agent`) instead of extra socket - but may have security implications
2. Configure GPG agent to allow signing on extra socket (`--extra-socket-name` option)
3. Use SSH agent-style forwarding for GPG (gpg-agent SSH emulation)
4. Proxy approach with full Assuan protocol implementation

## Test Requirements

Tests should verify the **desired behavior** (black-box) that currently fails:

1. GPG agent socket accessible in container
2. Key information visible (`gpg --list-secret-keys`)
3. Can sign data (`echo test | gpg --sign`)
4. Git commit signing works (`git commit -S`)
5. Cross-platform design (macOS, Linux, Windows scaffolding)

## Commands for Quick Verification

```bash
# Inside container
gpg-connect-agent 'KEYINFO --list' '/bye'     # Should list keys
gpg --list-secret-keys                         # Should show signing key
echo test | gpg -u $KEYID --sign --armor       # Should produce signature
git commit --allow-empty -S -m "test"          # Should create signed commit
```

## Related Files

- `internal/workspace/gpg.go` - Mount configuration
- `internal/workspace/CLAUDE.md` - Package docs (partially outdated)
- `internal/hostproxy/gpg_agent.go` - Proxy implementation
- `.serena/memories/docker-desktop-socket-mounting.md` - DELETED (was incorrect)
