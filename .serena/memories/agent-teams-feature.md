# Agent Teams Feature: Host Claude Config Forwarding

## Status: Design Phase (WIP) — Foundational Infrastructure Landed, Shared Config Abandoned
Last updated: 2026-02-09

## IMPORTANT: Shared Claude Config Abandoned

After extensive investigation (branch `a/shared-plugins`), sharing `.claude.json` and `~/.claude/` across containers was abandoned. Every approach (bind mounts, named volumes, VirtioFS) introduced cascading complexity: EBUSY errors, tool cache races, oh-my-zsh corruption, defensive env var whack-a-mole. **Sharing mutable state across containers fights the container model.**

All 5 commits on `a/shared-plugins` were reverted. The `ClaudeConfigSharing` type, `team`/`unified` modes, and related workspace/config/docker changes are gone. Only `isolated` mode exists (the default — each container gets its own Docker volume for `~/.claude`).

**What survived**: The `clawker-globals` volume (PR #104) for credential sharing remains — it's a clean, minimal solution that doesn't share mutable config state.

**Learnings**: See `shared-claude-config-postmortem` Serena memory for full analysis. Key insight: only share immutable/append-only data across containers, never mutable config directories.

## Problem

When clawker creates a container, the container's `~/.claude` directory is backed by a Docker **named volume** (persistent but isolated from host). This prevents:
1. Agent teams from coordinating between host and container (shared `teams/` and `tasks/` directories)
2. Host plugins, commands, skills, settings from being available inside containers without manual re-installation
3. ~~Credentials from being forwarded~~ **SOLVED** — see Foundational Infrastructure below

## Foundational Infrastructure (Implemented — PR #104)

The `clawker-globals` Docker volume was implemented as the first piece of agent-teams infrastructure. It solves credential sharing immediately and establishes patterns for sharing any host Claude files across containers.

### What's Implemented

**Global volume for cross-project/cross-agent credential persistence:**

1. **Volume**: `clawker-globals` — a single Docker volume shared by ALL containers, regardless of project or agent
2. **Mount point**: `/home/claude/.clawker-globals/` inside every container
3. **Symlink**: Entrypoint creates `~/.claude/.credentials.json` → `~/.clawker-globals/.credentials.json`
4. **Migration**: Existing per-agent credentials auto-migrate to global volume on first run
5. **Error handling**: Guarded `cp`/`ln -s` with `migration_ok` flag, `emit_error` on critical failures, `chmod 600` on credentials

### Key APIs & Patterns (Reusable for Agent Teams)

```go
// Naming: global volumes use hyphenated names (not dot-separated like agent volumes)
docker.GlobalVolumeName(purpose string) string  // → "clawker-<purpose>" (e.g. "clawker-globals")

// Labels: global volumes only have managed + purpose (no project/agent labels)
// This prevents accidental cleanup by removeAgentVolumes which filters by project/agent labels
docker.GlobalVolumeLabels(purpose string) map[string]string

// Volume lifecycle
workspace.EnsureGlobalsVolume(ctx, cli) error        // Creates volume if missing
workspace.GetGlobalsVolumeMount() mount.Mount         // Returns mount config
workspace.GetGlobalsVolumeMount() is called in SetupMounts() for every container

// Entrypoint pattern: mount at staging path, symlink to expected location
// This allows transparent reads/writes — Claude Code doesn't know about the volume
```

### Why This Matters for Agent Teams

1. **Credential sharing is solved**: When spinning up a team of agents in containers, they all authenticate via the same global volume. No per-agent OAuth dance needed.

2. **The pattern is extensible**: `GlobalVolumeName(purpose)` accepts any purpose string. Future global volumes for teams can follow the same pattern:
   - `clawker-teams` — shared team config, task assignments, coordination files
   - `clawker-plugins` — shared plugin cache across all agents
   - `clawker-sessions` — shared session metadata for team coordination

3. **The entrypoint symlink pattern is proven**: Mount at a staging path (`~/.clawker-<purpose>/`), symlink individual files to their expected locations. Claude Code sees normal file paths, writes persist to the volume, all containers see the same data.

4. **Label isolation is correct**: Global volumes use `managed + purpose` labels only (no project/agent), so they survive agent cleanup but are removed by `volume prune`. Agent-scoped volumes use `managed + project + agent + purpose` labels.

### Key Files

| File | Role |
|------|------|
| `internal/docker/names.go` | `GlobalVolumeName(purpose)` naming convention |
| `internal/docker/labels.go` | `GlobalVolumeLabels(purpose)` label convention |
| `internal/workspace/strategy.go` | `EnsureGlobalsVolume()`, `GetGlobalsVolumeMount()`, constants |
| `internal/workspace/setup.go` | Wired into `SetupMounts()` — every container gets the global volume |
| `internal/bundler/assets/entrypoint.sh` | Symlink creation, migration, error handling, permissions |
| `test/internals/scripts_test.go` | Integration tests for credential symlink + migration |

### Extending for Agent Teams

When implementing the full agent teams feature, the global volume can be extended:

```bash
# Entrypoint pattern for additional shared files:
TEAMS_STAGING="$HOME/.clawker-teams"
if [ -d "$TEAMS_STAGING" ]; then
    # Symlink teams and tasks directories for coordination
    ln -sfn "$TEAMS_STAGING/teams" "$CONFIG_DIR/teams"
    ln -sfn "$TEAMS_STAGING/tasks" "$CONFIG_DIR/tasks"
    ln -sfn "$TEAMS_STAGING/todos" "$CONFIG_DIR/todos"
fi
```

Or, if the full `~/.claude` bind mount approach (Phase 2 below) is implemented, the global volume becomes the fallback for non-forwarded mode while the bind mount handles the forwarded case.

---

## Implementation Sequence

### Phase 0: Global Volume Infrastructure ✅ COMPLETE (PR #104)
- `internal/docker/names.go` — `GlobalVolumeName(purpose)` naming
- `internal/docker/labels.go` — `GlobalVolumeLabels(purpose)` labels
- `internal/workspace/strategy.go` — `EnsureGlobalsVolume()`, `GetGlobalsVolumeMount()`, constants
- `internal/workspace/setup.go` — wired into `SetupMounts()`
- `internal/bundler/assets/entrypoint.sh` — credential symlink + migration + error handling + `chmod 600`
- `test/internals/scripts_test.go` — integration tests (symlink + migration)
- All unit + integration tests passing

### Phase 1-5: ABANDONED
All shared config phases (config schema, mount setup, entrypoint changes, command wiring, documentation) were abandoned. See "Shared Claude Config Abandoned" note at top of this document.