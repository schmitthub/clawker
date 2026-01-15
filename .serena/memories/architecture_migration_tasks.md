# Architecture Migration Task List

**Last Updated**: 2026-01-15
**Current Phase**: Phase 3 - Docker CLI Mimicry (REPLANNED)
**Plan File**: `~/.claude/plans/rosy-churning-puffin.md`

## IMPORTANT: Phase 3 Pivot

Phase 3 has been **pivoted** to implement Docker CLI command mimicry. Key changes:

- Commands will mirror Docker CLI structure (`clawker container run`, `clawker container ls`, etc.)
- Both top-level shortcuts AND management commands (like Docker)
- `clawker run` will behave like `docker run` (always creates new container)
- Keep `--agent` flag separate from `--name` for clawker-specific naming

**Reference Documents**:

- `.claude/docs/docker-cli-map.md` - Docker CLI → SDK mapping (1,740 lines)
- `.claude/docs/docker_top_level_commands.md` - Commands to implement

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

**Order**: Start with simplest, build up
**Reference**: `docker-cli-map.md` Container Commands section

- [ ] `container ls` (refactor from `list`)
- [ ] `container rm` (refactor from `remove`)
- [ ] `container start` / `container stop` (refactor)
- [ ] `container logs` (refactor from `logs`)
- [ ] `container create` (new)
- [ ] `container run` (refactor - Docker-style, no FindOrCreate)
- [ ] `container exec` (new)
- [ ] `container attach` (new)
- [ ] `container inspect` (new)
- [ ] `container kill` / `pause` / `unpause` (new)
- [ ] `container restart` (refactor)
- [ ] `container cp` (new)
- [ ] `container top` / `stats` / `wait` / `port` / `rename` / `update` (new)
- [ ] Add tests for new commands

### Task 3.4: Implement Image Commands

**Reference**: `docker-cli-map.md` Image Commands section

- [ ] `image ls` (alias: `images`)
- [ ] `image build` (refactor from `build`)
- [ ] `image rm` (alias: `rmi`)
- [ ] `image inspect` (new)
- [ ] `image pull` (new)
- [ ] Add tests for new commands

### Task 3.5: Implement Top-Level Shortcuts

- [ ] Create shortcuts in `pkg/cmd/root/root.go`
- [ ] Each shortcut delegates to management command
- [ ] Maintain backward compatibility aliases
- [ ] Add tests for new commands

### Task 3.6: Update internal/docker Layer

- [ ] Update `internal/docker/client.go` to use new whail methods
- [ ] Add agent resolution helpers
- [ ] Remove internal/engine dependencies
- [ ] Add tests for new functionality

### Task 3.7: Full Test Suite

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

---

## Notes

- **Always run tests** after each task: `go test ./...`
- **Context running low?** Update this memory before ending session
- **Integration tests**: `go test ./pkg/cmd/... -tags=integration -v -timeout 10m`
- **Plan file**: `~/.claude/plans/warm-humming-ocean.md`
