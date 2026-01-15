# Architecture Migration Status

## Migration: Current → "Padded Cell" Design

**Status**: PHASE 3 IN PROGRESS
**Plan Location**: `~/.claude/plans/rosy-churning-puffin.md`
**Design Document**: `.claude/docs/DESIGN.md`

## Design Decisions (User Confirmed)
- **Package name**: `pkg/whail` (whale jail - a Docker jail wrapper)
- **API style**: Docker SDK-like for familiarity
- **Swarm commands**: Deferred to future project (not in this migration)
- **Phase 3 pivot**: Docker CLI mimicry (commands mirror Docker CLI structure)

## Key Architectural Changes

### From
```
cmd/clawker → pkg/cmd/* → internal/engine → Docker SDK
```

### To
```
cmd/clawker → pkg/cmd/* → internal/docker → pkg/whail → Docker SDK
```

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
| **Phase 3** | Docker CLI Mimicry | 🔄 IN PROGRESS |
| **Phase 4** | Remove legacy code | ⏳ NOT STARTED |
| **Phase 5** | Documentation updates | ⏳ NOT STARTED |

## Phase 3: Docker CLI Mimicry

**Pivot**: Phase 3 was replanned to implement Docker CLI command structure.

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

### Task 3.3: Implement Container Commands - ⏳ NEXT

### Remaining Tasks (3.3-3.7)

See `architecture_migration_tasks` memory for full task list.

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

## How to Resume

**Say**: "Continue working on the architecture migration"

**Then**:
1. Read `architecture_migration_tasks` memory for detailed task list
2. Check `Current Phase` and `Next Task` in that memory
3. Resume work from where last session stopped
4. **Before context runs out**: Update the task list and Session Log

**Task List Memory**: `architecture_migration_tasks`
**Plan File**: `~/.claude/plans/rosy-churning-puffin.md`
