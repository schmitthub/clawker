# Container Init Feature - Implementation Plan

<important> This is a read only reference file, do not ever edit it</important>

## Context

New containers with Claude Code require authentication and plugin installation each time. This feature adds one-time host-to-container config copying and credential injection at container creation time, plus refactors the existing clawker-globals volume into a general-purpose shared directory.

**Decisions made:**
- All 4 work streams in one PR
- Init container pattern (busybox `CopyToVolume`) for copying host config to volume
- Clean break for globals→share migration (alpha, zero users)
- One-time copy at container creation (`run`/`create` commands)

---

## Work Streams

### 1. Config Schema Changes

**Files:** `internal/config/schema.go`, `internal/config/defaults.go`, `internal/config/validator.go`, `internal/config/CLAUDE.md`

Add to `AgentConfig`:
```go
type AgentConfig struct {
    // ... existing fields ...
    ClaudeCode     *ClaudeCodeConfig `yaml:"claude_code,omitempty" mapstructure:"claude_code"`
    EnableSharedDir *bool            `yaml:"enable_shared_dir,omitempty" mapstructure:"enable_shared_dir"`
}

type ClaudeCodeConfig struct {
    Config      ClaudeCodeConfigOptions `yaml:"config" mapstructure:"config"`
    UseHostAuth *bool                   `yaml:"use_host_auth,omitempty" mapstructure:"use_host_auth"`
}

type ClaudeCodeConfigOptions struct {
    Strategy string `yaml:"strategy" mapstructure:"strategy"` // "copy" or "fresh"
}
```

Helper methods following existing `*bool` pattern (see `GitCredentialsConfig`, `SecurityConfig.EnableHostProxy`):
- `ClaudeCodeConfig.UseHostAuthEnabled() bool` — default `true`
- `ClaudeCodeConfig.ConfigStrategy() string` — default `"fresh"`
- `AgentConfig.SharedDirEnabled() bool` — default `false`

**Validation** (`validator.go`): Add `validateClaudeCode()` — strategy must be `"copy"` or `"fresh"`

**Defaults** (`defaults.go`): No changes to `DefaultConfig()` (nil pointer = defaults via helper methods). Update `DefaultConfigYAML` with commented examples.

---

### 2. New Package: `internal/containerfs/`

New leaf package for preparing host claude config for container injection.

**Files to create:**
- `internal/containerfs/containerfs.go` — core logic
- `internal/containerfs/containerfs_test.go` — unit tests
- `internal/containerfs/CLAUDE.md` — package docs

**Public API:**
```go
// ResolveHostConfigDir returns the claude config dir ($CLAUDE_CONFIG_DIR or ~/.claude/).
// Returns error if directory doesn't exist.
func ResolveHostConfigDir() (string, error)

// PrepareClaudeConfig creates a staging directory with host claude config
// prepared for container injection. Caller must call cleanup() when done.
//
// Handles: settings.json enabledPlugins merge, agents/, skills/, commands/,
// plugins/ (excluding cache), known_marketplaces.json path fixup, symlink resolution.
func PrepareClaudeConfig(hostConfigDir, containerHomeDir string) (stagingDir string, cleanup func(), err error)

// PrepareCredentials creates a staging directory with credentials.json.
// Sources: keyring first, then fallback to $CLAUDE_CONFIG_DIR/.credentials.json.
func PrepareCredentials(hostConfigDir string) (stagingDir string, cleanup func(), err error)

// PrepareOnboardingTar creates a tar archive containing ~/.claude.json
// with {hasCompletedOnboarding: true} for CopyToContainer.
func PrepareOnboardingTar() (io.Reader, error)
```

**Copy logic details:**
- `settings.json`: Read host file, extract `enabledPlugins` key only, write to staging. Entrypoint's existing jq merge handles combining with image defaults.
- `agents/`, `skills/`, `commands/`: Copy entire dirs. Resolve symlinks (use `filepath.EvalSymlinks` + copy real content).
- `plugins/`: Copy everything EXCEPT `cache/` dir and `install-counts-cache.json`. For `known_marketplaces.json`: read, replace all `"installPath"` values from host abs path to container abs path (`/home/claude/.claude/plugins/...`).
- Missing files/dirs: Log to file (not error), skip silently.

**Credential logic:**
1. Call `keyring.GetClaudeCodeCredentials()`
2. On error: fall back to `$CLAUDE_CONFIG_DIR/.credentials.json`
3. On still no creds: return actionable error (disable flag or authenticate first)
4. Write creds as JSON to staging dir

**Imports:** `internal/keyring`, `internal/logger`, stdlib only. No docker imports (leaf package).

---

### 3. Globals → Share Refactoring

**Files to modify:**
- `internal/workspace/strategy.go` — rename constants, make read-only, add config check
- `internal/workspace/setup.go` — pass config for conditional mounting
- `internal/docker/names.go` — verify `GlobalVolumeName` still works (no change needed, purpose param changes)
- `internal/bundler/assets/entrypoint.sh` — remove credentials symlink section (lines 51-86)
- `.serena/memories/claude-code-authentication.md` — update

**Changes:**
```go
// strategy.go
const SharePurpose = "share"                                    // was GlobalsPurpose = "globals"
const ShareStagingPath = "/home/claude/.clawker-share"          // was GlobalsStagingPath

func EnsureShareVolume(ctx context.Context, cli *docker.Client) error  // was EnsureGlobalsVolume
func GetShareVolumeMount() mount.Mount                                  // was GetGlobalsVolumeMount
// Mount is now ReadOnly: true
```

```go
// setup.go — conditional based on config
if cfg.Config.Agent.SharedDirEnabled() {
    if err := EnsureShareVolume(ctx, client); err != nil { ... }
    mounts = append(mounts, GetShareVolumeMount())
}
```

**Entrypoint changes:** Remove lines 51-86 (credentials symlink logic). The credentials are now injected directly into the config volume at creation time.

---

### 4. Container Init Orchestration

**File to create:** `internal/cmd/container/shared.go`

This file contains shared logic used by both `run` and `create` commands.

```go
package container

// InitContainerConfig handles one-time claude config initialization for new containers.
// Called after EnsureConfigVolumes when the config volume was freshly created.
//
// Steps:
// 1. If strategy=="copy": prepare host claude config, CopyToVolume
// 2. If use_host_auth: prepare credentials, CopyToVolume
func InitContainerConfig(ctx context.Context, client *docker.Client, opts InitConfigOpts) error

type InitConfigOpts struct {
    ProjectName string
    AgentName   string
    ClaudeCode  *config.ClaudeCodeConfig
    IOStreams   *iostreams.IOStreams
}

// InjectOnboardingFile writes ~/.claude.json to a created (not started) container.
// Must be called after ContainerCreate and before ContainerStart.
func InjectOnboardingFile(ctx context.Context, client *docker.Client, containerID string) error
```

**Modified `EnsureConfigVolumes`** (`internal/workspace/strategy.go`):
```go
type ConfigVolumeResult struct {
    ConfigCreated  bool
    HistoryCreated bool
}

func EnsureConfigVolumes(ctx context.Context, cli *docker.Client, projectName, agentName string) (*ConfigVolumeResult, error)
```

Returns whether config volume was newly created, so callers know if init is needed.

---

### 5. Command Integration

**Files:** `internal/cmd/container/run/run.go`, `internal/cmd/container/create/create.go`

**Modified flow in `runRun()` and `runCreate()`:**
```
1. SetupMounts()                          // existing (now conditional share volume)
2. EnsureConfigVolumes()                  // existing, now returns ConfigVolumeResult
   if result.ConfigCreated:
     InitContainerConfig()                // NEW - copies config + creds to volume
3. [host proxy, git creds, env vars]      // existing
4. ContainerCreate()                      // existing
5. InjectOnboardingFile(containerID)      // NEW - writes ~/.claude.json if use_host_auth
6. ContainerStart / attachThenStart       // existing
```

**Important:** `InitContainerConfig` runs BETWEEN volume creation and container creation. This uses the init container (busybox) pattern to write to the volume. Then `InjectOnboardingFile` uses `CopyToContainer` on the created (but not started) main container.

---

### 6. Template Update

**File:** `templates/` (DefaultConfigYAML in `defaults.go`)

Add commented claude_code section:
```yaml
agent:
  # Claude Code configuration
  # claude_code:
  #   config:
  #     # "copy" copies host ~/.claude/ config, "fresh" starts clean
  #     strategy: "fresh"
  #   # Use host authentication tokens in container
  #   use_host_auth: true
  # Enable shared directory (read-only, mounted at ~/.clawker-share)
  # enable_shared_dir: false
```

---

## File Summary

| File | Action |
|------|--------|
| `internal/config/schema.go` | Add `ClaudeCodeConfig`, `ClaudeCodeConfigOptions`, fields on `AgentConfig` |
| `internal/config/defaults.go` | Update `DefaultConfigYAML` |
| `internal/config/validator.go` | Add `validateClaudeCode()` |
| `internal/config/CLAUDE.md` | Update schema docs |
| `internal/containerfs/containerfs.go` | **NEW** — host config preparation, credential resolution |
| `internal/containerfs/containerfs_test.go` | **NEW** — unit tests |
| `internal/containerfs/CLAUDE.md` | **NEW** — package docs |
| `internal/workspace/strategy.go` | Rename globals→share, add read-only, return `ConfigVolumeResult` |
| `internal/workspace/setup.go` | Conditional share volume mounting |
| `internal/bundler/assets/entrypoint.sh` | Remove credentials symlink section |
| `internal/cmd/container/shared.go` | **NEW** — `InitContainerConfig`, `InjectOnboardingFile` |
| `internal/cmd/container/run/run.go` | Wire init steps into run flow |
| `internal/cmd/container/create/create.go` | Wire init steps into create flow |
| `.serena/memories/container-init-feature.md` | Update with progress |
| `.serena/memories/claude-code-authentication.md` | Update (remove globals, add containerfs) |
| `internal/workspace/CLAUDE.md` | Update (share volume) |

---

## Verification

### Unit Tests
```bash
go test ./internal/config/... -v                    # Schema, validation, defaults
go test ./internal/containerfs/... -v               # Config preparation, credential resolution
go test ./internal/workspace/... -v                 # Share volume, ConfigVolumeResult
go test ./internal/cmd/container/... -v             # Shared init logic
```

### Integration Tests
```bash
go test ./test/internals/... -v -timeout 10m        # Entrypoint changes
```

### Manual Verification
1. `clawker run -it @` with default config (fresh strategy) — container starts normally, no copy
2. Set `agent.claude_code.config.strategy: copy` — verify host config copied to container's `~/.claude/`
3. Set `agent.claude_code.use_host_auth: true` — verify credentials in container, onboarding bypassed
4. Set `agent.enable_shared_dir: true` — verify `clawker-share` volume mounted read-only
5. Set `agent.enable_shared_dir: false` — verify no shared volume mounted
6. Run `clawker container start` on existing container — verify no re-copy (one-time only)

### Full Test Suite
```bash
make test                                            # All unit tests pass
```
