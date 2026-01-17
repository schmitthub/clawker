# Architecture Migration Status

## Migration: Current → "Padded Cell" Design

**Status**: ✅ MIGRATION COMPLETE (2026-01-16)
**Plan Location**: `~/.claude/plans/purring-stargazing-floyd.md` (Phase 4)
**Design Document**: `.claude/docs/DESIGN.md`
**SDK Mapping**: `.claude/docs/docker-cli-sdk-mapping.md`

## Design Decisions (User Confirmed)
- **Package name**: `pkg/whail` (whale jail - a Docker jail wrapper)
- **API style**: Docker SDK-like for familiarity
- **Swarm commands**: Deferred to future project (not in this migration)
- **Phase 3 refocused**: Docker CLI mimicry with clear separation of concerns

### Phase 3 Key Design Decisions (2026-01-15)

1. **Two Command Interface Patterns**:
   - **Top-level shortcuts** (`clawker run/start`) - Aliases to `container run` (Docker CLI pattern)
   - **Management Commands** (`clawker container *`) - Docker-compatible, positional container names
   - Top-level `run`/`start` ARE aliases that delegate to `container run`

2. **Architecture Constraint** (CRITICAL):
   - All Docker SDK calls MUST go through `pkg/whail`
   - Never call Docker SDK directly from commands or internal/docker
   - If whail lacks a method, scaffold with `// TODO: implement in whail`

3. **Canonical vs Shortcut Structure**:
   - Canonical: `container list`, `container remove` (management subcommands)
   - Shortcuts: Cobra aliases (`ls`, `ps`, `rm`) on same command
   - Top-level project commands are NOT shortcuts (different semantics)

## Key Architectural Changes

### From
```
cmd/clawker → pkg/cmd/* → internal/engine → Docker SDK
```

### After (Current - Migration Complete)
```
cmd/clawker → pkg/cmd/* → internal/docker → pkg/whail → Docker SDK
```

## Phase 4: Remove Legacy Code - COMPLETED (2026-01-16)

**Key Changes:**
- Deleted `internal/engine/` directory entirely
- All code now uses `internal/docker.Client` which wraps `pkg/whail.Engine`
- Removed deprecated `Engine()` and `CloseEngine()` from `pkg/cmdutil/factory.go`
- Migrated `pkg/cmdutil/output.go` to use `whail.DockerError`
- Migrated `internal/build/build.go` to use `docker.Client`
- Migrated `internal/workspace/` package to use `docker.Client`
- Migrated `pkg/cmd/monitor/up.go` to use `docker.Client`
- Added methods to `internal/docker/client.go`: `IsMonitoringActive`, `ImageExists`, `BuildImage`
- Added `internal/docker/volume.go` with `EnsureVolume`, `CopyToVolume`, and tar helpers
- Added `IsAlpineImage` to `internal/docker/names.go`
- Deleted `pkg/cmd/remove/` and `pkg/cmd/prune/` packages (user cleanup)
- Updated root.go and root_test.go to reflect current command structure

**Type Mappings Applied:**
- `engine.Engine` → `docker.Client`
- `engine.NewEngine(ctx)` → `docker.NewClient(ctx)`
- `engine.ClawkerContainer` → `docker.Container`
- `engine.ContainerName()` → `docker.ContainerName()`
- `engine.VolumeName()` → `docker.VolumeName()`
- `engine.ImageTag()` → `docker.ImageTag()`
- `engine.ImageLabels()` → `docker.ImageLabels()`
- `engine.IsAlpineImage()` → `docker.IsAlpineImage()`
- `*engine.DockerError` → `*whail.DockerError`

## Completed Packages

### 1. `pkg/whail/` - External Engine (Reusable "whale jail" library)
- Label-based selector injection
- Configurable label prefix
- Docker SDK-like API for familiarity
- Whitelist interface (only exposed methods accessible)
- Standalone for use in other projects

**Files created:**
- `engine.go` - Core Engine with configurable LabelConfig
- `container.go` - Container operations (Create, Start, Stop, Remove, List, Inspect, Attach, Wait, Logs, Resize, ExecCreate, ExecAttach, ExecResize, FindByName, IsManaged, **Kill, Pause, Unpause, Restart, Rename, Top, Stats, StatsOneShot, Update**)
- `volume.go` - Volume operations with label injection
- `network.go` - Network operations with label injection
- `image.go` - Image operations with label injection
- `labels.go` - Label utilities (MergeLabels, LabelConfig, etc.)
- `errors.go` - Generic Docker errors (22+ error types)
- `copy.go` - **NEW** File copy operations (CopyToContainer, CopyFromContainer, ContainerStatPath)

### 2. `internal/docker/` - Clawker Docker middleware (thin layer)
- Initializes `pkg/whail` with clawker's labels
- Handles naming conventions (`clawker.project.agent`)
- Uses embedded whail.Engine for all Docker operations

**Files created:**
- `labels.go` - Clawker label constants and helper functions
- `names.go` - Container/volume naming functions
- `client.go` - Client wrapper around whail.Engine

### 3. `pkg/cmdutil/factory.go` - Updated
- Added `Client(ctx)` method for lazy-initialized docker.Client
- Added `CloseClient()` method
- Marked `Engine()` and `CloseEngine()` as deprecated

## Implementation Phases

| Phase | Description | Status |
|-------|-------------|--------|
| **Phase 1** | Create `pkg/whail` foundation | ✅ COMPLETED |
| **Phase 2** | Create `internal/docker` layer | ✅ COMPLETED |
| **Phase 3** | Docker CLI Mimicry | ✅ COMPLETED |
| **Phase 4** | Remove legacy code | ✅ COMPLETED (2026-01-16) |
| **Phase 5** | Documentation updates | ✅ COMPLETED |

## Phase 3: Docker CLI Mimicry (REFOCUSED 2026-01-15)

**Plan File**: `~/.claude/plans/curried-floating-pizza.md`

### Session Execution Order
```
A.1 → A.2 → A.3 → B → C → D → E → G → F
```

| Session | Description | Est. Time | Status |
|---------|-------------|-----------|--------|
| A.1 | Container: restart, rename, wait | 20 min | ✅ |
| A.2 | Container: top, stats, update | 25 min | ✅ |
| A.3 | Container: exec, attach, cp | 30 min | ✅ |
| B | Volume subcommands | 30 min | ✅ |
| C | Network subcommands | 30 min | ✅ |
| D | Image subcommands | 30 min | ✅ |
| E | Missing whail prune methods | 30 min | ✅ |
| G | Documentation update | 30 min | ✅ |
| F | Container create/run | 45 min | ✅ |

**Total**: ~4.5 hours across 9 sessions

### Task 3.1: Add Missing whail Methods - ✅ COMPLETED (2026-01-15)

Added to `pkg/whail/container.go`:
- `ContainerKill(ctx, id, signal)` - Send signal to container
- `ContainerPause(ctx, id)` - Pause running container
- `ContainerUnpause(ctx, id)` - Unpause paused container
- `ContainerRestart(ctx, id, timeout)` - Restart with optional timeout
- `ContainerRename(ctx, id, newName)` - Rename container
- `ContainerTop(ctx, id, args)` - Get running processes
- `ContainerStats(ctx, id, stream)` - Stream resource usage (returns io.ReadCloser)
- `ContainerStatsOneShot(ctx, id)` - Single stats snapshot (returns StatsResponseReader)
- `ContainerUpdate(ctx, id, config)` - Update resource constraints

Added to `pkg/whail/copy.go` (NEW FILE):
- `CopyToContainer(ctx, id, dstPath, content, opts)` - Copy tar to container
- `CopyFromContainer(ctx, id, srcPath)` - Copy tar from container
- `ContainerStatPath(ctx, id, path)` - Stat path inside container

Added to `pkg/whail/errors.go`:
- `ErrContainerKillFailed`
- `ErrContainerRestartFailed`
- `ErrContainerPauseFailed`
- `ErrContainerUnpauseFailed`
- `ErrContainerRenameFailed`
- `ErrContainerTopFailed`
- `ErrContainerStatsFailed`
- `ErrContainerUpdateFailed`
- `ErrCopyToContainerFailed`
- `ErrCopyFromContainerFailed`
- `ErrContainerStatPathFailed`
- `ErrContainerWaitFailed`
- `ErrContainerListFailed`

Tests added to `pkg/whail/container_test.go`:
- `TestContainerKill`
- `TestContainerPauseUnpause`
- `TestContainerRestart`
- `TestContainerRename`
- `TestContainerTop`
- `TestContainerWait` (added in PR review)
- `TestContainerAttach` (added in PR review)

### Task 3.1.1: PR Review Fixes - ✅ COMPLETED (2026-01-15)

Fixed issues identified during comprehensive PR review:

**Critical Fix:**
- `ContainerWait` now wraps SDK channel errors for consistent user-friendly messaging

**Important Fixes:**
- Removed duplicate `generateCopyContainerName` helper from `copy_test.go` (uses shared `generateContainerName`)
- Added `TestContainerWait` with channel semantics testing (nil wait channel for unmanaged)
- Added `TestContainerAttach` with managed/unmanaged verification
- `IsContainerManaged` now wraps non-NotFound errors with `ErrContainerInspectFailed`

### Task 3.2: Create Management Command Structure - ✅ COMPLETED (2026-01-15)

Created parent management commands for Docker CLI mimicry:
- `pkg/cmd/container/container.go` - Container management parent command
- `pkg/cmd/image/image.go` - Image management parent command
- `pkg/cmd/volume/volume.go` - Volume management parent command
- `pkg/cmd/network/network.go` - Network management parent command
- Updated `pkg/cmd/root/root.go` to register all management commands
- Added tests: `container_test.go`, `image_test.go`, `volume_test.go`, `network_test.go`
- Updated `pkg/cmd/root/root_test.go` to verify management commands registered

Commands appear in CLI help as "Additional help topics" until subcommands are added (Task 3.3).

### Task 3.3: Implement Container Commands - 🔄 IN PROGRESS (2026-01-15)

Created 18 container subcommands in `pkg/cmd/container/`:

**Completed Commands:**
| Command | File | Description |
|---------|------|-------------|
| `list` | `list.go` | List containers (aliases: ls, ps) |
| `remove` | `remove.go` | Remove containers (alias: rm) |
| `start` | `start.go` | Start stopped containers |
| `stop` | `stop.go` | Stop running containers |
| `logs` | `logs.go` | Fetch container logs |
| `inspect` | `inspect.go` | Display detailed info (JSON) |
| `kill` | `kill.go` | Kill with custom signal |
| `pause` | `pause.go` | Pause container processes |
| `unpause` | `pause.go` | Resume paused containers |

**All commands:**
- Accept container names as positional arguments (Docker-like)
- Use `internal/docker.Client` instead of legacy `internal/engine`
- Include comprehensive unit tests

**Remaining Commands (broken into sessions):**

Session A.1: ✅ COMPLETED
- [x] `restart`, `rename`, `wait`

Session A.2: ✅ COMPLETED
- [x] `top`, `stats`, `update`

Session A.3: ✅ COMPLETED
- [x] `exec`, `attach`, `cp`

Session B: ✅ COMPLETED
- [x] `volume list`, `volume inspect`, `volume create`, `volume remove`, `volume prune`

Session C: ✅ COMPLETED (2026-01-16)
- [x] `network list`, `network inspect`, `network create`, `network remove`, `network prune`

Session D: ✅ COMPLETED (2026-01-16)
- [x] `image list`, `image inspect`, `image remove`, `image build`, `image prune`

Session E: ✅ COMPLETED (2026-01-16)
- [x] `VolumesPrune(ctx, all bool)` - with `all` param to include named volumes
- [x] `NetworksPrune(ctx)` - managed label filter injection
- [x] `ImagesPrune(ctx, dangling bool)` - managed label filter injection
- [x] `ImageInspect` was already implemented
- [x] Error constructors: `ErrVolumesPruneFailed`, `ErrNetworksPruneFailed`, `ErrImagesPruneFailed`
- [x] Updated prune commands to use new whail methods instead of list+remove workaround
- [x] Fixed test ordering issue in TestImageRemove affecting TestImageInspect

Session F: ✅ COMPLETED (2026-01-16)
- [x] `create`, `run`

### Remaining Tasks

| Task | Description | Session | Status |
|------|-------------|---------|--------|
| 3.4 | Volume commands | B | ✅ |
| 3.5 | Network commands | C | ✅ |
| 3.6 | Image commands | D | ✅ |
| 3.7 | Missing whail prune methods | E | ✅ |
| 3.8 | Documentation update | G | ✅ |
| 3.9 | Full test suite | - | ✅ |
| 3.10 | Container create/run (advanced) | F | ✅ |

See `architecture_migration_tasks` memory for detailed checklists.

## Key Design Patterns

### Selector Injection (pkg/whail)
```go
// All list operations automatically inject label filters
func (e *Engine) ContainerList(ctx context.Context, opts ListOptions) ([]Container, error) {
    opts.Filters = e.injectManagedFilter(opts.Filters)
    return e.cli.ContainerList(ctx, opts)
}
```

### Managed Container Check Pattern
```go
func (e *Engine) ContainerKill(ctx context.Context, containerID, signal string) error {
    isManaged, err := e.IsContainerManaged(ctx, containerID)
    if err != nil {
        return ErrContainerKillFailed(containerID, err)
    }
    if !isManaged {
        return ErrContainerNotFound(containerID)
    }
    // ... perform operation
}
```

### Client Embedding (internal/docker)
```go
type Client struct {
    *whail.Engine  // Embedded - all whail methods available directly
}
```

## Important Learnings

1. **ContainerStatsOneShot** returns `container.StatsResponseReader`, not `container.StatsResponse`
2. **types.ContainerPathStat** is deprecated - use `container.PathStat` instead
3. All new container methods follow the same pattern: check `IsContainerManaged` first, return `ErrContainerNotFound` if not managed
4. Tests use table-driven pattern with `setupFunc` and `cleanupFunc` for each test case
5. Integration tests require Docker running and use a shared `testEngine` instance
6. **ContainerWait returns channels** - errors from SDK must be wrapped in a goroutine to maintain consistent UX
7. **IsContainerManaged** returns `(false, nil)` for not-found containers - callers cannot distinguish "not found" from "unmanaged"
8. **Error wrapping consistency** - all methods should wrap errors consistently, including in helper functions like `IsContainerManaged`
9. **Test helper deduplication** - test files in the same package share helpers; avoid duplicate functions like `generateCopyContainerName`
10. **Channel-based methods need special testing** - verify both the response channel AND error channel behavior, including nil checks
11. **CLI command naming**: Use long names for files (e.g., `list.go` not `ls.go`), alias short names in cobra
12. **Cobra test pattern**: Override `RunE` to capture options without executing, use `splitArgs` helper for parsing
13. **Commands use positional args**: Docker-like interface - `clawker container rm NAME` not `clawker container rm --name NAME`
14. **Parent commands**: Add subcommands alphabetically with `cmd.AddCommand()`, Cobra auto-sorts in help output
15. **Test expectedSubcommands**: Keep sorted alphabetically to match Cobra's output order
16. **Global flag shorthand conflict**: Don't use `-d` shorthand in subcommands - it conflicts with the global `--debug` flag
17. **VolumesPrune all param**: Docker only prunes anonymous volumes by default; pass `all=true` filter to include named volumes
18. **Test ordering**: Tests run in file order; avoid using shared test fixtures in tests that modify them (TestImageRemove used testImageTag which TestImageInspect needed)
19. **Prune API signatures**: VolumesPrune takes `all bool`, ImagesPrune takes `dangling bool`, NetworksPrune has no params

## How to Resume

**Say**: "Continue working on the architecture migration" or "Start Session A.1"

**Then**:
1. Read `architecture_migration_tasks` memory for detailed task list
2. Read the plan file for implementation details
3. Check `Current Phase` and `Next Task` in task memory
4. Resume work from where last session stopped
5. **Before context runs out**: Update the task list and Session Log

**Task List Memory**: `architecture_migration_tasks`
**Plan File**: `~/.claude/plans/curried-floating-pizza.md`
**SDK Mapping**: `.claude/docs/docker-cli-sdk-mapping.md`

## Migration Complete Summary

The architecture migration from `internal/engine` to `pkg/whail` + `internal/docker` is **complete**.

### Final Architecture
```
cmd/clawker → pkg/cmd/* → internal/docker → pkg/whail → Docker SDK
```

### What Was Achieved
- Created `pkg/whail` as a reusable, label-isolated Docker engine library
- Created `internal/docker` as a thin clawker-specific wrapper
- Implemented full Docker CLI mimicry (`container`, `volume`, `network`, `image` commands)
- Removed legacy `internal/engine` package entirely
- All tests passing, binary builds successfully

### Key Files
- `pkg/whail/` - Reusable Docker engine (container, volume, network, image, copy operations)
- `internal/docker/` - Clawker middleware (labels, names, client)
- `pkg/cmd/container/` - 18 container subcommands
- `pkg/cmd/volume/` - 5 volume subcommands
- `pkg/cmd/network/` - 5 network subcommands
- `pkg/cmd/image/` - 5 image subcommands

---

## Quick Reference

### Command Pattern for Management Subcommands
```go
func NewCmd<Action>(f *cmdutil.Factory) *cobra.Command {
    cmd := &cobra.Command{
        Use:     "<action> RESOURCE [RESOURCE...]",
        Aliases: []string{"<alias>"},
        Args:    cobra.MinimumNArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            client, err := docker.NewClient(ctx)
            if err != nil { return cmdutil.HandleError(err) }
            defer client.Close()
            // Call whail methods through client
        },
    }
    return cmd
}
```

### Verification After Each Session
```bash
go test ./pkg/cmd/<package>/...  # Unit tests
go test ./...                     # Full suite
go build ./cmd/clawker           # Build binary
./bin/clawker <command> --help   # Manual test
```
