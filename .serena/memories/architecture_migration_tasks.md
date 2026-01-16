# Architecture Migration Task List

**Last Updated**: 2026-01-15
**Current Phase**: Phase 3 - Docker CLI Mimicry (REFOCUSED)
**Plan File**: `~/.claude/plans/curried-floating-pizza.md`

## IMPORTANT: Phase 3 Refocused

Phase 3 has been **refocused** to implement Docker CLI command mimicry with clear separation:

### Key Design Decisions

1. **Two Parallel Command Interfaces** (Keep Both):
   - **Project Commands** (`clawker run/stop/logs`) - Project-aware, uses `--agent` flag
   - **Management Commands** (`clawker container *`) - Docker-compatible, positional container names
   - These serve different purposes and do NOT delegate to each other

2. **Architecture Constraint**:
   - All Docker SDK calls MUST go through `pkg/whail`
   - Never call Docker SDK directly from commands
   - If whail lacks a method, scaffold with `// TODO: implement in whail`

3. **Canonical vs Shortcut**:
   - Canonical: `container list`, `container remove`
   - Shortcuts: Cobra aliases (`ls`, `ps`, `rm`) on same command
   - Top-level project commands are NOT shortcuts (different semantics)

**Reference Documents**:

- `.claude/docs/docker-cli-sdk-mapping.md` - Docker CLI → SDK mapping
- `.claude/docs/desired_docker_top_level_commands.md` - Commands to implement
- `~/.claude/plans/curried-floating-pizza.md` - Full implementation plan

## Quick Resume

When starting a new session, say: **"Continue working on the architecture migration"**

The assistant should:

1. Read this memory
2. Check `Current Phase` and `Next Task`
3. Resume work from where the last session stopped

---

## Migration Overview

| From | To |
|------|-----|
| `internal/engine/` (monolithic) | `pkg/whail/` (heavy) + `internal/docker/` (thin) |

**Design Philosophy**:

- `pkg/whail` = Heavy-weight (all core Docker logic, configurable labels)
- `internal/docker` = Thin (just configure Engine with clawker preferences)

---

## Phase 1: Create `pkg/whail` Foundation

**Status**: COMPLETED (2026-01-14)
**Estimated Sessions**: 3-4

### Task 1.1: Create pkg/whail directory and engine.go

- [x] Create `pkg/whail/` directory
- [x] Create `pkg/whail/engine.go` with:
  - [x] `EngineOptions` struct (LabelPrefix, ManagedLabel, LabelConfig)
  - [x] `LabelConfig` struct (Default, Container, Volume, Network labels)
  - [x] `Engine` struct wrapping `*docker.Client`
  - [x] `NewEngine(ctx, opts)` constructor
  - [x] `Close()` method
  - [x] `HealthCheck(ctx)` method
  - [x] `injectManagedFilter()` helper
- [x] Write tests: `pkg/whail/engine_test.go` (integration tests with Docker)

### Task 1.2: Create container operations

- [x] Create `pkg/whail/container.go` with:
  - [x] `ContainerList(ctx, opts)` with label injection
  - [x] `ContainerCreate(ctx, config)` with label injection
  - [x] `ContainerStart(ctx, id)`
  - [x] `ContainerStop(ctx, id, timeout)`
  - [x] `ContainerRemove(ctx, id, opts)`
  - [x] `ContainerAttach(ctx, id, opts)`
  - [x] `ContainerWait(ctx, id, condition)`
  - [x] `ContainerLogs(ctx, id, opts)`
  - [x] `ContainerInspect(ctx, id)`
  - [x] `ContainerExec*` methods
  - [x] `ContainerResize(ctx, id, height, width)`
  - [x] `FindContainerByName(ctx, name)` helper
- [x] Write tests: consolidated in `pkg/whail/engine_test.go`

### Task 1.3: Create volume operations

- [x] Create `pkg/whail/volume.go` with:
  - [x] `VolumeCreate(ctx, opts)` with label injection
  - [x] `VolumeList(ctx, opts)` with label filtering
  - [x] `VolumeRemove(ctx, id, force)`
  - [x] `VolumeExists(ctx, name)`
  - [x] `VolumeInspect(ctx, name)`
- [x] Write tests: consolidated in `pkg/whail/engine_test.go`

### Task 1.4: Create network operations

- [x] Create `pkg/whail/network.go` with:
  - [x] `NetworkCreate(ctx, name, opts)` with label injection
  - [x] `NetworkList(ctx, opts)` with label filtering
  - [x] `NetworkRemove(ctx, id)`
  - [x] `NetworkExists(ctx, name)`
  - [x] `NetworkInspect(ctx, name)`
  - [x] `EnsureNetwork(ctx, name)` helper
- [x] Write tests: consolidated in `pkg/whail/engine_test.go`

### Task 1.5: Create image operations

- [x] Create `pkg/whail/image.go` with:
  - [x] `ImageBuild(ctx, buildContext, opts)`
  - [x] `ImagePull(ctx, ref, opts)`
  - [x] `ImageList(ctx, opts)` - note: images can have label filters
  - [x] `ImageRemove(ctx, id, opts)`
  - [x] `ImageExists(ctx, ref)`
- [x] Write tests: consolidated in `pkg/whail/engine_test.go`

### Task 1.6: Create labels and errors

- [x] Create `pkg/whail/labels.go` with:
  - [x] Generic label utilities
  - [x] `LabelFilter(prefix, key, value)` function
  - [x] `MergeLabels(base, override)` helper
- [x] Create `pkg/whail/errors.go`:
  - [x] Copy and adapt from `internal/engine/errors.go`
  - [x] Make generic (no clawker-specific references)
- [x] Write tests (`labels_test.go`, `errors_test.go`)

### Task 1.7: Run all tests and verify

- [x] `go test ./pkg/whail/...`
- [x] Verify no `internal/` imports in `pkg/whail`
- [x] Verify package is standalone/reusable

---

## Phase 2: Create `internal/docker` Layer (Thin)

**Status**: COMPLETED (2026-01-14)
**Actual Sessions**: 1

### Task 2.1: Create labels.go

- [x] Create `internal/docker/` directory
- [x] Create `internal/docker/labels.go` with:
  - [x] Clawker label key constants (`LabelProject`, `LabelAgent`, etc.)
  - [x] `ContainerLabels()`, `VolumeLabels()`, `ImageLabels()`, `NetworkLabels()`
  - [x] Filter functions: `ClawkerFilter()`, `ProjectFilter()`, `AgentFilter()`

### Task 2.2: Create names.go

- [x] Create `internal/docker/names.go`:
  - [x] `ContainerName(project, agent)`
  - [x] `VolumeName(project, agent, purpose)`
  - [x] `ParseContainerName(name)`
  - [x] `GenerateRandomName()`
  - [x] `ImageTag(project)`
  - [x] `NetworkName` constant

### Task 2.3: Create client.go

- [x] Create `internal/docker/client.go` with:
  - [x] `Client` struct wrapping `*whail.Engine`
  - [x] `NewClient(ctx)` that configures whail with clawker labels
  - [x] `Close()` method
  - [x] `Engine()` for direct access when needed
  - [x] `ListContainers()`, `ListContainersByProject()`
  - [x] `FindContainerByAgent()` with graceful not-found handling
  - [x] `RemoveContainerWithVolumes()`

### Task 2.4: agent.go - SKIPPED

- Not needed: Commands compose operations directly
- No `RunAgent`, `StopAgent` etc. methods exist in current codebase
- Adding them would be new functionality, not migration

### Task 2.5: Run tests and verify

- [x] Created `labels_test.go` - unit tests for label constants and helpers
- [x] Created `names_test.go` - unit tests for naming functions
- [x] Created `client_test.go` - integration tests for Client
- [x] `go test ./internal/docker/...` - all tests pass
- [x] `go test ./...` - full test suite passes

---

## Phase 3: Docker CLI Mimicry

**Status**: REPLANNED - Ready to Start
**Estimated Sessions**: 4-6
**Full Plan**: `~/.claude/plans/rosy-churning-puffin.md`

### Task 3.1: Add Missing whail Methods

**Status**: COMPLETED (2026-01-15)

**Files**: `pkg/whail/container.go`, new `pkg/whail/copy.go`
**Reference**: `docker-cli-map.md` sections for each command

- [x] Add `ContainerKill(ctx, id, signal)`
- [x] Add `ContainerPause(ctx, id)` / `ContainerUnpause(ctx, id)`
- [x] Add `ContainerRestart(ctx, id, timeout)`
- [x] Add `ContainerRename(ctx, id, newName)`
- [x] Add `ContainerTop(ctx, id, args)`
- [x] Add `ContainerStats(ctx, id, stream)` - returns io.ReadCloser
- [x] Add `ContainerStatsOneShot(ctx, id)` - returns StatsResponseReader
- [x] Add `ContainerUpdate(ctx, id, config)`
- [x] Add `CopyToContainer(ctx, id, path, content, opts)` - tar handling
- [x] Add `CopyFromContainer(ctx, id, path)` - returns tar reader
- [x] Add `ContainerStatPath(ctx, id, path)` - stat path in container
- [x] Add tests for new methods (Kill, Pause/Unpause, Restart, Rename, Top)
- [x] Add error types in `errors.go` for all new operations
- [x] All tests passing: `go test ./...`

### Task 3.2: Create Management Command Structure

**Status**: COMPLETED (2026-01-15)
**Files**: `pkg/cmd/container/`, `pkg/cmd/image/`, `pkg/cmd/volume/`, `pkg/cmd/network/`

- [x] Create `pkg/cmd/container/container.go` - parent command
- [x] Create `pkg/cmd/image/image.go` - parent command
- [x] Create `pkg/cmd/volume/volume.go` - parent command
- [x] Create `pkg/cmd/network/network.go` - parent command
- [x] Update `pkg/cmd/root/root.go` to register management commands
- [x] Write tests: `container_test.go`, `image_test.go`, `volume_test.go`, `network_test.go`
- [x] Update `pkg/cmd/root/root_test.go` to verify management commands
- [x] All tests passing: `go test ./...`

### Task 3.3: Implement Container Commands

**Status**: IN PROGRESS (2026-01-15)
**Reference**: `~/.claude/plans/curried-floating-pizza.md`

#### 3.3.1: Basic Container Commands (COMPLETED)

- [x] `container list` (with aliases ls, ps)
- [x] `container remove` (with alias rm)
- [x] `container start`
- [x] `container stop`
- [x] `container logs`
- [x] `container inspect`
- [x] `container kill`
- [x] `container pause`
- [x] `container unpause`
- [x] Tests for all completed commands

#### 3.3.2: Simple Container Commands (Session A.1 - ~20 min)

- [x] `container restart`
- [x] `container rename`
- [x] `container wait`

#### 3.3.3: Inspection Container Commands (Session A.2 - ~25 min) - ✅ COMPLETED

- [x] `container top`
- [x] `container stats`
- [x] `container update`

#### 3.3.4: Interactive Container Commands (Session A.3 - ~30 min) - ✅ COMPLETED

- [x] `container exec`
- [x] `container attach`
- [x] `container cp`

#### 3.3.5: Advanced Container Commands (Session F - deferred)

- [ ] `container create`
- [ ] `container run` (Docker-style)

### Task 3.4: Volume Commands (Session B - ~30 min)

- [ ] `volume list` (alias: ls)
- [ ] `volume inspect`
- [ ] `volume create`
- [ ] `volume remove` (alias: rm)
- [ ] `volume prune` (scaffold with TODO for whail method)

### Task 3.5: Network Commands (Session C - ~30 min)

- [ ] `network list` (alias: ls)
- [ ] `network inspect`
- [ ] `network create`
- [ ] `network remove` (alias: rm)
- [ ] `network prune` (scaffold with TODO for whail method)

### Task 3.6: Image Commands (Session D - ~30 min)

- [ ] `image list` (aliases: ls, images)
- [ ] `image inspect` (scaffold with TODO for whail method)
- [ ] `image remove` (aliases: rm, rmi)
- [ ] `image build` (delegate to top-level)
- [ ] `image prune` (scaffold with TODO for whail method)

### Task 3.7: Missing whail Methods (Session E - ~30 min)

- [ ] `VolumesPrune` in pkg/whail/volume.go
- [ ] `NetworksPrune` in pkg/whail/network.go
- [ ] `ImagesPrune` in pkg/whail/image.go
- [ ] `ImageInspect` in pkg/whail/image.go

### Task 3.8: Documentation Update (Session G - ~30 min)

- [ ] Update CLI-VERBS.md with all new commands
- [ ] Update ARCHITECTURE.md with command taxonomy
- [ ] Update README.md CLI commands table
- [ ] Update this memory (architecture_migration_tasks)

### Task 3.9: Full Test Suite

- [ ] `go test ./...`
- [ ] `go test ./pkg/cmd/... -tags=integration -v -timeout 10m`

---

## Phase 4: Remove Legacy Code

**Status**: NOT STARTED
**Estimated Sessions**: 1

### Task 4.1: Remove internal/engine

- [ ] Delete `internal/engine/` directory
- [ ] Update all remaining imports
- [ ] Remove `Engine()` from factory (keep only `Client()`)

### Task 4.2: Cleanup

- [ ] `go mod tidy`
- [ ] `go vet ./...`
- [ ] `go fmt ./...`

### Task 4.3: Final test run

- [ ] `go test ./...`
- [ ] `go test ./pkg/cmd/... -tags=integration -v -timeout 10m`

---

## Phase 5: Documentation

**Status**: NOT STARTED
**Estimated Sessions**: 1

### Task 5.1: Update CLAUDE.md

- [ ] Update Repository Structure
- [ ] Update Key Concepts table
- [ ] Update code examples

### Task 5.2: Update ARCHITECTURE.md

- [ ] Document `pkg/whail` API
- [ ] Document `internal/docker` usage
- [ ] Update diagrams

### Task 5.3: Update DESIGN.md

- [ ] Mark implementation complete
- [ ] Add implementation notes

### Task 5.4: Update README.md

- [ ] If any user-facing changes

### Task 5.5: Update Serena memories

- [ ] Update `project_overview`
- [ ] Update `common_patterns`
- [ ] Archive `architecture_migration_tasks` or mark complete

---

## Session Log

Track each session's progress here:

### Session 1 (2026-01-13)

- **Duration**: ~1 hour
- **Work Done**:
  - Created migration plan
  - Established design philosophy (whail=heavy, docker=thin)
  - User decisions: pkg/whail name, Docker SDK-like API, defer swarm
  - Created task list (this memory)
- **Next**: Start Task 1.1 (create pkg/whail/engine.go)
- **Blockers**: None

### Session 2 (2026-01-14)

- **Duration**: ~45 minutes
- **Work Done**:
  - Created `pkg/whail` foundation
  - Created all core files: engine.go, container.go, volume.go, network.go, image.go, labels.go, errors.go
  - Created unit tests: labels_test.go, errors_test.go
  - Package is standalone (no clawker-specific imports)
- **Next**: Add integration tests
- **Blockers**: None

### Session 3 (2026-01-14)

- **Duration**: ~20 minutes
- **Work Done**:
  - COMPLETED Phase 1: Added comprehensive integration tests
  - Consolidated tests in `pkg/whail/engine_test.go` (55 tests total)
  - Added tests for: ContainerStop, ContainerListByLabels, FindManagedContainerByName, IsContainerManaged
  - Added tests for: VolumeListByLabels, IsVolumeManaged
  - Added tests for: IsNetworkManaged
  - Added tests for: ImageBuild with label injection
  - All tests passing: `go test ./...` and `go test ./pkg/whail/...`
- **Next**: Start Phase 2 (create `internal/docker` layer)
- **Blockers**: None

### Session 4 (2026-01-14)

- **Duration**: ~30 minutes
- **Work Done**:
  - COMPLETED Phase 2: Created `internal/docker` layer (thin)
  - Created `labels.go` with clawker label constants and filter functions
  - Created `names.go` with container/volume naming functions
  - Created `client.go` wrapping whail.Engine with clawker config
  - Created unit tests for labels and names
  - Created integration tests for Client
  - Decided to SKIP agent.go (not needed - commands compose operations directly)
  - All tests passing: `go test ./...`
- **Next**: Start Phase 3 (update commands to use `internal/docker`)
- **Blockers**: None

### Session 5 (2026-01-15)

- **Duration**: ~15 minutes
- **Work Done**:
  - COMPLETED Task 3.1: Updated factory with Client() method
    - Added `clientOnce`, `client`, `clientErr` fields
    - Added `Client(ctx)` method with lazy initialization
    - Added `CloseClient()` method
    - Marked `CloseEngine()` as deprecated
  - Started Task 3.2 but hit design issue
    - Initial approach was adding wrapper methods to docker.Client
    - User feedback: don't wrap unless modifying behavior
    - Need to replan Phase 3 approach
- **Next**: Decide on approach for Phase 3 before proceeding
- **Blockers**: Need decision on where ContainerConfig/FindOrCreate logic should live

### Session 6 (2026-01-15)

- **Duration**: ~30 minutes
- **Work Done**:
  - COMPLETED Task 3.1 (Phase 3.1): Added all missing whail methods
    - Added container methods: `ContainerKill`, `ContainerPause`, `ContainerUnpause`, `ContainerRestart`, `ContainerRename`, `ContainerTop`, `ContainerStats`, `ContainerStatsOneShot`, `ContainerUpdate`
    - Created `pkg/whail/copy.go` with: `CopyToContainer`, `CopyFromContainer`, `ContainerStatPath`
    - Added 11 new error types in `pkg/whail/errors.go`
    - Added integration tests for: Kill, Pause/Unpause, Restart, Rename, Top
    - All tests passing: `go test ./...`
- **Next**: Task 3.2 - Create management command structure
- **Blockers**: None

### Session 7 (2026-01-15)

- **Duration**: ~20 minutes
- **Work Done**:
  - COMPLETED Task 3.1.1: PR Review Fixes
  - Ran comprehensive PR review using pr-review-toolkit agents (code-reviewer, pr-test-analyzer, silent-failure-hunter, type-design-analyzer)
  - Fixed critical issue: `ContainerWait` now wraps SDK channel errors
  - Fixed important issues:
    - Removed duplicate `generateCopyContainerName` from `copy_test.go`
    - Added `TestContainerWait` with channel semantics testing
    - Added `TestContainerAttach` with managed/unmanaged verification
    - `IsContainerManaged` now wraps non-NotFound errors with `ErrContainerInspectFailed`
  - All whail tests passing: `go test ./pkg/whail/...`
  - Integration tests: 2 pre-existing failures unrelated to changes (TestRm_UnusedFlag_NoUnused, TestRun_BuildsImage)
- **Next**: Task 3.2 - Create management command structure
- **Blockers**: None
- **Key Learnings**:
  - Channel-based methods like `ContainerWait` need goroutines to wrap SDK errors
  - Test helper functions should not be duplicated across test files in same package
  - `IsContainerManaged` silently returns `(false, nil)` for not-found - document this behavior

### Session 8 (2026-01-15)

- **Duration**: ~20 minutes
- **Work Done**:
  - COMPLETED Task 3.2: Create Management Command Structure
  - Created 4 parent command files for Docker CLI mimicry:
    - `pkg/cmd/container/container.go`
    - `pkg/cmd/image/image.go`
    - `pkg/cmd/volume/volume.go`
    - `pkg/cmd/network/network.go`
  - Updated `pkg/cmd/root/root.go` with imports and command registration
  - Created tests for each parent command verifying:
    - Command basics (Use, Short, Long, Example)
    - No RunE (parent commands)
    - No subcommands yet (Task 3.3 will add them)
  - Updated `pkg/cmd/root/root_test.go` to include management commands
  - All tests passing: `go test ./...`
  - CLI shows management commands in "Additional help topics" (expected until subcommands added)
- **Next**: Task 3.3 - Implement Container Commands (container ls, container rm, etc.)
- **Blockers**: None
- **Key Learnings**:
  - Cobra shows parent commands without subcommands under "Additional help topics"
  - Once subcommands are added, they move to "Available Commands"

### Session 9 (2026-01-15)

- **Duration**: ~45 minutes
- **Work Done**:
  - STARTED Task 3.3: Implement Container Commands
  - Created 9 new container subcommands using `internal/docker` client:
    - `pkg/cmd/container/list.go` - List containers (aliases: ls, ps)
    - `pkg/cmd/container/remove.go` - Remove containers (alias: rm)
    - `pkg/cmd/container/start.go` - Start stopped containers
    - `pkg/cmd/container/stop.go` - Stop running containers
    - `pkg/cmd/container/logs.go` - Fetch container logs
    - `pkg/cmd/container/inspect.go` - Display detailed container info (JSON)
    - `pkg/cmd/container/kill.go` - Kill containers with signal
    - `pkg/cmd/container/pause.go` - Pause/unpause containers
  - Created comprehensive tests for all commands
  - Updated `container.go` parent command to register all subcommands
  - All tests passing: `go test ./...`
  - CLI shows "Available Commands" with all 9 subcommands
- **Next**: Continue Task 3.3 - Implement remaining container commands (create, run, exec, attach, restart, cp, top, stats, etc.)
- **Blockers**: None
- **Key Learnings**:
  - Commands use positional args for container names (Docker-like)
  - Helper function `splitArgs` shared across test files in same package
  - Commands use `internal/docker.Client` instead of legacy `internal/engine`

### Session 10 (2026-01-15)

- **Duration**: ~45 minutes
- **Work Done**:
  - REFOCUSED Phase 3 migration plan
  - Created comprehensive plan at `~/.claude/plans/curried-floating-pizza.md`
  - Key design decisions documented:
    1. Two parallel command interfaces (project commands vs management commands) - keep both separate
    2. Architecture constraint: all SDK calls through pkg/whail only
    3. Canonical commands with Cobra aliases for shortcuts
  - Broke remaining work into 9 manageable sessions:
    - A.1: Container restart, rename, wait (20 min)
    - A.2: Container top, stats, update (25 min)
    - A.3: Container exec, attach, cp (30 min)
    - B: Volume commands (30 min)
    - C: Network commands (30 min)
    - D: Image commands (30 min)
    - E: Missing whail methods (30 min)
    - F: Container create/run (45 min)
    - G: Documentation update (30 min)
  - Updated this memory with new task structure (3.3.1-3.9)
- **Next**: Session A.1 - Implement container restart, rename, wait
- **Blockers**: None
- **Key Learnings**:
  - Top-level project commands (run, stop, logs) are NOT shortcuts to management commands
  - They have different semantics (project-based with --agent vs container-name-based)
  - Never bypass whail - scaffold with TODO if method missing

### Session 11 (2026-01-15)

- **Duration**: ~25 minutes
- **Work Done**:
  - COMPLETED Session A.2: Inspection Container Commands
  - Created 3 new container subcommands:
    - `pkg/cmd/container/top/top.go` - Display running processes (ContainerTop)
    - `pkg/cmd/container/stats/stats.go` - Display resource usage statistics (ContainerStats/StatsOneShot)
    - `pkg/cmd/container/update/update.go` - Update container resource constraints (ContainerUpdate)
  - All commands include:
    - Comprehensive flag parsing
    - Unit tests for flag handling and command properties
    - Proper error handling through cmdutil.HandleError
  - Stats command features:
    - One-shot mode (--no-stream) and streaming mode
    - CPU %, memory usage, network I/O, block I/O, PIDs display
    - Support for multiple containers
  - Update command features:
    - --cpus, --memory, --memory-reservation, --memory-swap
    - --cpu-shares, --cpuset-cpus, --cpuset-mems
    - --pids-limit, --blkio-weight
    - Human-readable memory size parsing (512m, 1g, etc.)
  - Updated `container.go` parent to register all 3 new commands
  - Updated `container_test.go` to expect 15 subcommands
  - All tests passing: `go test ./...`
  - CLI shows all 15 container subcommands
- **Next**: Session A.3 - Container exec, attach, cp
- **Blockers**: None
- **Key Learnings**:
  - Stats streaming requires goroutines for concurrent container stat collection
  - Memory size parsing needs case-insensitive suffix handling
  - Cobra interprets args starting with `-` as flags; use `--` separator or avoid such test inputs

### Session 12 (2026-01-15)

- **Duration**: ~35 minutes
- **Work Done**:
  - COMPLETED Session A.3: Interactive Container Commands
  - Created 3 new container subcommands with tests:
    - `pkg/cmd/container/exec/exec.go` - Execute command in container (TTY, stdin handling)
    - `pkg/cmd/container/attach/attach.go` - Attach to running container (TTY, signal handling)
    - `pkg/cmd/container/cp/cp.go` - Copy files to/from container (tar archive handling)
  - All commands use `internal/docker.Client` and `internal/term.PTYHandler` for TTY
  - Fixed `-d` shorthand conflict (exec's `--detach` no longer has shorthand, conflicts with global `--debug`)
  - Updated `container.go` to register all 3 new commands (now 18 total subcommands)
  - Updated `container_test.go` expectedSubcommands to include attach, cp, exec
  - All tests passing: `go test ./...`
  - CLI shows all 18 container subcommands in help
- **Next**: Session B - Volume commands (list, inspect, create, remove, prune)
- **Blockers**: None
- **Key Learnings**:
  - Global flags like `-d` (--debug) conflict with command-specific flags; avoid reusing shorthands
  - exec command must check container is running before creating exec instance
  - cp command uses tar archives for file transfer; handle both copy directions
  - attach command detects container TTY from ContainerInspect

---

## Notes

- **Always run tests** after each task: `go test ./...`
- **Context running low?** Update this memory before ending session
- **Integration tests**: `go test ./pkg/cmd/... -tags=integration -v -timeout 10m`
- **Plan file**: `~/.claude/plans/curried-floating-pizza.md`
- **Architecture constraint**: All Docker SDK calls must go through `pkg/whail`
- **Session order**: ~~A.1~~ → ~~A.2~~ → ~~A.3~~ → B → C → D → G → E → F
