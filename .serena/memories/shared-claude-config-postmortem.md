# Shared Claude Config Postmortem

## Status: ABANDONED (2026-02-09)

## What We Tried
Branch `a/shared-plugins` attempted to share `.claude.json` and `~/.claude/` across containers using three modes:
- **team**: shared + host bridge (bind mount host `~/.claude` at same absolute path)
- **unified**: shared named volume across all containers
- **isolated**: per-agent Docker volumes (existing behavior)

## Why It Failed
Every sharing approach introduced cascading complexity:
1. **EBUSY on VirtioFS**: macOS Docker Desktop uses VirtioFS which gives EBUSY errors on concurrent file access to bind-mounted files
2. **Tool cache races**: Multiple containers writing to `~/.claude/cache/` and tool directories simultaneously corrupt state
3. **oh-my-zsh corruption**: Named home volumes with shared state caused shell initialization corruption
4. **Defensive env var whack-a-mole**: Each problem required another env var or entrypoint guard, creating fragile layered workarounds

## Key Insight
**Sharing mutable state across containers fights the container model.** Containers are designed for isolation. Every approach to share mutable config directories introduces complexity that the container model was designed to eliminate.

## What Survived
- `clawker-globals` volume (PR #104): Shares only credential files (`~/.claude/.credentials.json`) via a purpose-built global volume with symlinks. This works because credentials are effectively append-only (written once, read many times).

## Lesson Learned
Only share **immutable or append-only** data across containers. Never share mutable config directories. If coordination is needed, use explicit message-passing (files in a shared volume with clear ownership) rather than shared mutable state.

## Code Reverted
All 5 commits on `a/shared-plugins` (27 files, ~1970 insertions) were reverted by resetting the branch to `origin/main`. Types removed: `ClaudeConfigSharing`, `ClaudeConfigMode*` constants, `GetSharedClaudeStateMount`, `EnsureSharedConfigDir`, `host_shared.go`, `claudestate.go`, `isolated_paths.go`.
