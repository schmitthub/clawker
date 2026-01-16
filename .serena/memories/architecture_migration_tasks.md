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
   - Top-level project commands CAN BE shortcuts they are documented in @./claude

**Docker Shortcuts**
| Shortcut | Canonical Command | Notes |
|----------|-------------------|-------|
| `docker ps` | `docker container list` | Also: `docker container ls`, `docker container ps` |
| `docker run` | `docker container run` | Compound: create + start |
| `docker exec` | `docker container exec` | |
| `docker attach` | `docker container attach` | |
| `docker cp` | `docker container cp` | |
| `docker create` | `docker container create` | |
| `docker kill` | `docker container kill` | |
| `docker logs` | `docker container logs` | |
| `docker pause` | `docker container pause` | |
| `docker port` | `docker container port` | |
| `docker rename` | `docker container rename` | |
| `docker restart` | `docker container restart` | |
| `docker rm` | `docker container remove` | Also: `docker container rm` |
| `docker start` | `docker container start` | |
| `docker stats` | `docker container stats` | |
| `docker stop` | `docker container stop` | |
| `docker top` | `docker container top` | |
| `docker unpause` | `docker container unpause` | |
| `docker update` | `docker container update` | |
| `docker wait` | `docker container wait` | |
| `docker build` | `docker image build` | Also: `docker buildx build` |
| `docker images` | `docker image list` | Also: `docker image ls` |
| `docker rmi` | `docker image remove` | Also: `docker image rm` |


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

### Task 3.4: Volume Commands (Session B - ~30 min) - ✅ COMPLETED

- [x] `volume list` (alias: ls)
- [x] `volume inspect`
- [x] `volume create`
- [x] `volume remove` (alias: rm)
- [x] `volume prune` (scaffold with TODO for whail method)

### Task 3.5: Network Commands (Session C - ~30 min) - ✅ COMPLETED (2026-01-16)

- [x] `network list` (alias: ls)
- [x] `network inspect`
- [x] `network create`
- [x] `network remove` (alias: rm)
- [x] `network prune` (uses list+remove workaround like volume prune)

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

### Lessons Learned

After each session if you've learned anything add it to this list, avoid verbosity.

**Key Learnings**:
  - Channel-based methods like `ContainerWait` need goroutines to wrap SDK errors
  - Test helper functions should not be duplicated across test files in same package
  - `IsContainerManaged` silently returns `(false, nil)` for not-found - document this behavior
  - Cobra shows parent commands without subcommands under "Additional help topics"
  - Once subcommands are added, they move to "Available Commands"
  - Commands use positional args for container names (Docker-like)
  - Helper function `splitArgs` shared across test files in same package
  - Commands use `internal/docker.Client` instead of legacy `internal/engine`
  - Top-level project commands (run, stop, logs) are NOT shortcuts to management commands
  - They have different semantics (project-based with --agent vs container-name-based)
  - Never bypass whail - scaffold with TODO if method missing
  - Stats streaming requires goroutines for concurrent container stat collection
  - Memory size parsing needs case-insensitive suffix handling
  - Cobra interprets args starting with `-` as flags; use `--` separator or avoid such test inputs
  - Global flags like `-d` (--debug) conflict with command-specific flags; avoid reusing shorthands
  - exec command must check container is running before creating exec instance
  - cp command uses tar archives for file transfer; handle both copy directions
  - attach command detects container TTY from ContainerInspect
  - Subcommands go in their own subpackages (volume/list/list.go not volume/list.go)
  - shlex.Split strips quotes, so test expected values shouldn't include quotes
  - prune workaround: list+remove individual volumes instead of waiting for VolumesPrune
  - Global flag `-d/--debug` reserves `-d` shorthand; don't reuse it in subcommands

Summarize subtasks and tasks into short summaries after they are complete to keep this file footprint small

---

## Notes

- **Always run tests** after each task: `go test ./...`
- **Context running low?** Update this memory before ending session
- **Integration tests**: `go test ./pkg/cmd/... -tags=integration -v -timeout 10m`
- **Plan file**: `~/.claude/plans/curried-floating-pizza.md`
- **Architecture constraint**: All Docker SDK calls must go through `pkg/whail`
- **Session order**: ~~A.1~~ → ~~A.2~~ → ~~A.3~~ → ~~B~~ → ~~C~~ → D → G → E → F
