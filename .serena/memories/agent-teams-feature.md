# Agent Teams Feature: Host Claude Config Forwarding

## Status: Design Phase (WIP) — Container Init Feature Landed
Last updated: 2026-02-09

## Problem

When clawker creates a container, the container's `~/.claude` directory is backed by a Docker **named volume** (persistent but isolated from host). This prevents:
1. Agent teams from coordinating between host and container (shared `teams/` and `tasks/` directories)
2. Host plugins, commands, skills, settings from being available inside containers without manual re-installation
3. ~~Credentials from being forwarded~~ **SOLVED** — see Foundational Infrastructure below

## Foundational Infrastructure (Superseded by Container Init Feature)

**NOTE**: The `clawker-globals` volume approach (PR #104) has been **superseded** by the Container Init Feature (branch `a/containerfs-init`). The new approach:
- Uses one-time `containerfs.PrepareClaudeConfig()` + `containerfs.PrepareCredentials()` at container creation time
- Copies host config/credentials directly into per-agent config volumes (not a shared global volume)
- The old globals volume was renamed to `clawker-share` (optional, read-only, gated by `agent.enable_shared_dir: true`)
- Entrypoint credential symlink section was removed

### What Was Implemented (PR #104, now superseded)

**Global volume for cross-project/cross-agent credential persistence (OLD APPROACH):**

1. **Volume**: `clawker-globals` — a single Docker volume shared by ALL containers → NOW: `clawker-share` (optional, read-only)
2. **Mount point**: `/home/claude/.clawker-globals/` inside every container → NOW: `/home/claude/.clawker-share/` (when enabled)
3. **Symlink**: Entrypoint created `~/.claude/.credentials.json` → `~/.clawker-globals/.credentials.json` → NOW: Removed, credentials injected at creation time
4. **Migration**: Existed per-agent credentials auto-migrate → NOW: Removed
5. **Error handling**: Guarded `cp`/`ln -s` with `migration_ok` flag → NOW: Simpler, init runs once at creation

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

## Design Decision: Same-Absolute-Path Bind Mount + Symlink

**Pattern precedent**: `buildWorktreeGitMount()` in `internal/workspace/setup.go` — mounts the host's `.git` directory at the **same absolute path** inside the container so internal path references resolve correctly.

**Approach**: Mount the host's entire `~/.claude` directory at its original host path in the container, then symlink the container's `~/.claude` → host path.

```
Host:       /Users/andrew/.claude/  (the real directory)
Container:  /Users/andrew/.claude/  (bind mount, same absolute path)
            /home/claude/.claude → /Users/andrew/.claude  (symlink)
```

### Why This Works
- All absolute path references in `installed_plugins.json`, `known_marketplaces.json` resolve correctly — they already point to the host path
- Claude Code in the container accesses config via `$HOME/.claude` → symlink → host path → bind mount → actual host files
- Bidirectional read-write for teams/tasks/todos works naturally
- **Zero path rewriting** needed in entrypoint or anywhere else
- Matches the proven worktree mount pattern exactly

### Why Not Selective Mounts or Staging
- Selective mounts require enumerating every subdirectory (fragile, breaks when Claude Code adds new directories)
- Staging + entrypoint copy adds complexity and loses real-time bidirectional sync
- Path rewriting via `sed` is fragile and may miss references in future file formats
- The whole-directory mount with same-absolute-path is simpler, more robust, and battle-tested (Docker Desktop already mounts `/Users` by default on macOS)

## Claude Code `~/.claude` Directory Structure

```
~/.claude/
├── .credentials.json          # OAuth tokens (SENSITIVE)
├── settings.json              # User settings, enabled plugins, status line
├── statusline.sh              # Custom status line script
├── history.jsonl              # Command history
├── plugins/                   # Installed plugins + marketplace cache
│   ├── installed_plugins.json # Contains absolute installPath references
│   ├── known_marketplaces.json # Contains absolute installLocation references
│   ├── cache/                 # Plugin code cache
│   └── marketplaces/          # Marketplace repos + external plugins
├── projects/                  # Per-project session data (keyed by slugified path)
├── session-env/               # Per-session environment
├── tasks/                     # Agent teams task files (per-session)
├── teams/                     # Agent teams configuration
├── todos/                     # Todo files per session
├── plans/                     # Plan files per session
├── debug/                     # Debug logs per session
├── cache/                     # General cache
├── downloads/                 # Downloads
├── paste-cache/               # Paste cache
└── shell-snapshots/           # Shell state snapshots
```

**Files with absolute host path references**:
- `plugins/installed_plugins.json` — `installPath` field (e.g., `/Users/andrew/.claude/plugins/cache/...`)
- `plugins/known_marketplaces.json` — `installLocation` field
- `projects/` directory keys — slugified workspace paths

## Architecture

### Mount Strategy

```
┌─────────────────────────────────────────────────────────┐
│ Container                                                │
│                                                          │
│  /home/claude/.claude → /Users/andrew/.claude  (symlink) │
│                         ↕ (bind mount, read-write)       │
│  /Users/andrew/.claude ←→ Host /Users/andrew/.claude     │
│                                                          │
│  Config volume at /home/claude/.claude-local (fallback)  │
│  - Container-specific overrides if needed                │
└─────────────────────────────────────────────────────────┘
```

### Config Volume Changes

**Current**: Docker volume `clawker.<project>.<agent>-config` mounted at `/home/claude/.claude`

**New (when forwarding enabled)**: 
- The config volume is still created but NOT mounted at `~/.claude`
- Instead, mount host `~/.claude` at its original absolute path
- Entrypoint creates symlink: `/home/claude/.claude` → host path
- The `~/.claude-init/` directory (image defaults) is still used for `statusline.sh` and base `settings.json` merge

**When forwarding disabled** (default): Behavior unchanged — existing Docker volume at `~/.claude`.

### Integration with Existing Patterns

| Existing Pattern | How Agent Teams Feature Follows It |
|---|---|
| `buildWorktreeGitMount()` | Same-absolute-path bind mount (`Source == Target`) |
| `SetupGitCredentials()` | Called alongside in run.go, returns `{Mounts, Env}` |
| `GetConfigVolumeMounts()` | Conditionally skip when forwarding enabled |
| `RuntimeEnvOpts` | New env vars: `CLAWKER_HOST_CLAUDE_DIR`, `CLAWKER_CLAUDE_CONFIG_FORWARD` |
| `entrypoint.sh` init blocks | New block for symlink creation + settings merge |

## Config Schema Changes

### New section in `clawker.yaml`

```yaml
agent:
  claude_config:
    forward: true    # Enable host ~/.claude forwarding (default: false)
```

### Schema struct (`internal/config/schema.go`)

```go
type ClaudeConfigForwarding struct {
    Forward *bool `yaml:"forward,omitempty" mapstructure:"forward"` // default false
}

func (c *ClaudeConfigForwarding) IsEnabled() bool {
    return c != nil && c.Forward != nil && *c.Forward
}
```

Added to `AgentConfig`:
```go
type AgentConfig struct {
    // ... existing fields ...
    ClaudeConfig *ClaudeConfigForwarding `yaml:"claude_config,omitempty" mapstructure:"claude_config"`
}
```

**Design note**: Start with a single `forward` toggle. Granular per-directory control (credentials, plugins, teams independently) can be added later as sub-fields when the need arises.

## Implementation Files

### New: `internal/workspace/claude_config.go`

Follows `gitconfig.go` pattern:

```go
type ClaudeConfigSetupResult struct {
    Mounts          []mount.Mount
    Env             []string
    SkipConfigVolume bool  // When true, caller skips GetConfigVolumeMounts
}

func SetupClaudeConfig(cfg *config.ClaudeConfigForwarding) ClaudeConfigSetupResult
func HostClaudeConfigDir() string  // Resolves ~/.claude on the host
```

**Mount construction**: 
```go
// Mount host ~/.claude at its original absolute path
mount.Mount{
    Type:   mount.TypeBind,
    Source: hostClaudeDir,     // e.g., /Users/andrew/.claude
    Target: hostClaudeDir,     // Same absolute path (worktree pattern)
    ReadOnly: false,
}
```

**Env vars**:
- `CLAWKER_CLAUDE_CONFIG_FORWARD=true` — signals entrypoint to create symlink
- `CLAWKER_HOST_CLAUDE_DIR=/Users/andrew/.claude` — tells entrypoint where to point symlink

### Modified: `internal/workspace/setup.go` → `SetupMounts()`

When `ClaudeConfig.IsEnabled()`:
- Skip `GetConfigVolumeMounts()` (no config volume at `~/.claude`)
- Still create history volume (separate mount point)

### Modified: `internal/bundler/assets/entrypoint.sh`

New block (after existing init, before git config):
```bash
if [ "$CLAWKER_CLAUDE_CONFIG_FORWARD" = "true" ] && [ -n "$CLAWKER_HOST_CLAUDE_DIR" ]; then
    # Remove or move existing ~/.claude (from volume or image)
    if [ -d "$CONFIG_DIR" ] && [ ! -L "$CONFIG_DIR" ]; then
        # Preserve init defaults for merging
        mv "$CONFIG_DIR" "${CONFIG_DIR}.local" 2>/dev/null || true
    fi
    
    # Create symlink: ~/.claude → host path
    ln -sfn "$CLAWKER_HOST_CLAUDE_DIR" "$CONFIG_DIR"
    
    # Merge clawker-specific settings (statusline, etc.) into host settings
    if [ -d "$INIT_DIR" ] && [ -f "$INIT_DIR/settings.json" ]; then
        # Merge: host settings take precedence, clawker init provides defaults
        jq -s '.[0] * .[1]' "$INIT_DIR/settings.json" "$CONFIG_DIR/settings.json" \
            > "$CONFIG_DIR/settings.json.tmp" 2>/dev/null \
            && mv "$CONFIG_DIR/settings.json.tmp" "$CONFIG_DIR/settings.json" \
            || true
    fi
fi
```

### Modified: `internal/cmd/container/run/run.go` (and `create/create.go`)

After `SetupGitCredentials`:
```go
// Setup Claude Code config forwarding
claudeSetup := workspace.SetupClaudeConfig(cfg.Agent.ClaudeConfig)
workspaceMounts = append(workspaceMounts, claudeSetup.Mounts...)
containerOpts.Env = append(containerOpts.Env, claudeSetup.Env...)
```

And conditionally skip config volume mounts:
```go
// In SetupMounts or in the caller:
if !claudeSetup.SkipConfigVolume {
    mounts = append(mounts, workspace.GetConfigVolumeMounts(...)...)
}
```

### Modified: `internal/docker/env.go` → `RuntimeEnvOpts`

New fields:
```go
type RuntimeEnvOpts struct {
    // ... existing ...
    ClaudeConfigForward  bool   // Set CLAWKER_CLAUDE_CONFIG_FORWARD
    HostClaudeDir        string // Set CLAWKER_HOST_CLAUDE_DIR
}
```

## Agent Teams Coordination

With the whole `~/.claude` mounted read-write:

1. **Host creates team** → writes to `~/.claude/teams/{team-name}/config.json`
2. **Container sees it** immediately via bind mount
3. **Container spawns as teammate** — reads same task list from `~/.claude/tasks/{team-name}/`
4. **File locking** for task claiming works (same filesystem)
5. **Messages** flow through shared filesystem

The `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` env var should be injected when forwarding is enabled (or it comes from the forwarded `settings.json`).

## Security Considerations

- **Credentials (SOLVED)**: `.credentials.json` is now shared via `clawker-globals` volume with `chmod 600` enforcement. All containers share the same OAuth tokens. Acceptable because containers already have code access and network access.
- **Global volume isolation**: Global volumes use `managed + purpose` labels only — no project/agent scoping. This is intentional for cross-agent sharing but means any container can read/write another's credentials.
- **Bidirectional writes**: Container can modify all host Claude config (when full forwarding enabled). This is the desired behavior (read-write requirement)
- **Opt-in only**: `forward: false` by default — no impact on existing users
- **Firewall**: Container firewall still applies — plugins with network MCP servers would need firewall domains configured

## Decisions Made

1. **Config volume**: Skip entirely when forwarding enabled (don't create or mount)
2. **Read-write**: Full bidirectional sharing (user confirmed)
3. **Teams/tasks sharing**: Shared between host and container via bind mount (user confirmed)
4. **Output**: Design memory only (this document) — implementation planning is a future session
5. **Global volume for credentials**: `clawker-globals` volume persists OAuth tokens across all projects/agents (implemented PR #104)
6. **Global volume naming**: Hyphenated `clawker-<purpose>` (not dot-separated like agent volumes) — intentionally different to prevent cleanup collisions
7. **Entrypoint symlink pattern**: Mount at staging path → symlink individual files. Transparent to Claude Code, proven pattern for future extensions
8. **Error handling in entrypoint**: Guarded shell operations with `migration_ok` flag, `emit_error` for critical failures, graceful degradation for non-critical failures

## Open Questions (Pending)

1. **Settings merge**: Should clawker defaults (statusline.sh) be merged into host settings? Needs more thought — risk of modifying host files vs. not having clawker statusline. Possible: merge into a copy, not the original.
2. **Full path list**: User to provide complete list of files with absolute path references (partially confirmed: `installed_plugins.json`, `known_marketplaces.json`, `projects/` keys)
3. **CLI flag**: Should there be a `--forward-claude-config` flag on `container run` for per-invocation control?
4. **Credential refresh**: Currently one-shot at container start. Live refresh via inotify/fsnotify could be future work.

## Implementation Sequence

### Phase 0: Container Init Feature ✅ COMPLETE (branch a/containerfs-init, supersedes PR #104)
- `internal/containerfs/` — host config preparation (settings, plugins, credentials, onboarding)
- `internal/cmd/container/opts/init.go` — `InitContainerConfig`, `InjectOnboardingFile` orchestration
- `internal/config/schema.go` — `ClaudeCodeConfig`, `AgentConfig.ClaudeCode`, `AgentConfig.EnableSharedDir`
- `internal/workspace/strategy.go` — `EnsureShareVolume()`, `GetShareVolumeMount()` (read-only), `ConfigVolumeResult`
- `internal/workspace/setup.go` — `SetupMountsResult` with `ConfigVolumeResult`
- `internal/bundler/assets/entrypoint.sh` — removed credential symlink section
- `internal/cmd/container/run/run.go` + `create/create.go` — wired init steps
- All 3510 unit tests passing

### Phase 1: Config Schema
- `internal/config/schema.go` — `ClaudeConfigForwarding` struct + methods
- `internal/config/defaults.go` — default values
- Unit tests for schema

### Phase 2: Mount Setup
- `internal/workspace/claude_config.go` — `SetupClaudeConfig()`, `HostClaudeConfigDir()`
- `internal/workspace/claude_config_test.go` — unit tests
- Modify `SetupMounts` to conditionally skip config volume

### Phase 3: Entrypoint
- `internal/bundler/assets/entrypoint.sh` — symlink creation + settings merge
- This changes image hash — requires rebuild

### Phase 4: Command Wiring
- `internal/cmd/container/run/run.go` — wire `SetupClaudeConfig`
- `internal/cmd/container/create/create.go` — wire `SetupClaudeConfig`
- `internal/docker/env.go` — new RuntimeEnvOpts fields

### Phase 5: Documentation
- Update CLAUDE.md files
- Update clawker.yaml template
- Update this memory
