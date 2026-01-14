# Architecture Migration Task List

**Last Updated**: 2026-01-13
**Current Phase**: PLANNING (Not Started)
**Plan File**: `~/.claude/plans/warm-humming-ocean.md`

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

**Status**: NOT STARTED
**Estimated Sessions**: 1-2

### Task 2.1: Create client.go

- [ ] Create `internal/docker/` directory
- [ ] Create `internal/docker/client.go` with:
  - [ ] `Client` struct wrapping `*whail.Engine`
  - [ ] `NewClient(ctx, cfg)` that configures whail with clawker labels
  - [ ] `Close()` method
  - [ ] Expose `Engine()` for direct access when needed

### Task 2.2: Create labels.go

- [ ] Create `internal/docker/labels.go` with:
  - [ ] Clawker label key constants (`LabelProject`, `LabelAgent`, etc.)
  - [ ] Move constants from `internal/engine/labels.go`
  - [ ] `ContainerLabels(project, agent, version, image, workdir)` helper
  - [ ] `VolumeLabels(project, agent, purpose)` helper

### Task 2.3: Create names.go

- [ ] Create `internal/docker/names.go`:
  - [ ] Move from `internal/engine/names.go`
  - [ ] `ContainerName(project, agent)`
  - [ ] `VolumeName(project, agent, purpose)`
  - [ ] `ParseContainerName(name)`
  - [ ] `GenerateRandomName()`
  - [ ] `ImageTag(project)`

### Task 2.4: Create agent.go (thin wrappers)

- [ ] Create `internal/docker/agent.go` with:
  - [ ] `RunAgent(ctx, agent, opts)` - thin, composes Engine calls
  - [ ] `StopAgent(ctx, agent, opts)` - thin
  - [ ] `RemoveAgent(ctx, agent, opts)` - thin
  - [ ] `ListAgents(ctx, opts)` - thin
  - [ ] `RestartAgent(ctx, agent, opts)` - thin

### Task 2.5: Run tests and verify

- [ ] Write minimal tests for thin wrappers
- [ ] `go test ./internal/docker/...`
- [ ] Verify it uses `pkg/whail` correctly

---

## Phase 3: Update Commands

**Status**: NOT STARTED
**Estimated Sessions**: 2-3

### Task 3.1: Update factory

- [ ] Update `pkg/cmdutil/factory.go`:
  - [ ] Add `Client() (*docker.Client, error)` method
  - [ ] Keep `Engine()` temporarily for backward compat
  - [ ] Update lazy initialization

### Task 3.2: Update run command

- [ ] Update `pkg/cmd/run/run.go`:
  - [ ] Replace `engine.NewEngine` with `docker.NewClient`
  - [ ] Use new label/name helpers
  - [ ] Run integration tests

### Task 3.3: Update stop command

- [ ] Update `pkg/cmd/stop/stop.go`
- [ ] Run integration tests

### Task 3.4: Update list command

- [ ] Update `pkg/cmd/list/list.go`
- [ ] Run integration tests

### Task 3.5: Update remove command

- [ ] Update `pkg/cmd/remove/remove.go`
- [ ] Update `pkg/cmd/prune/prune.go`
- [ ] Run integration tests

### Task 3.6: Update logs command

- [ ] Update `pkg/cmd/logs/logs.go`

### Task 3.7: Update restart command

- [ ] Update `pkg/cmd/restart/restart.go`

### Task 3.8: Update build command

- [ ] Update `pkg/cmd/build/build.go`

### Task 3.9: Update workspace strategy

- [ ] Update `internal/workspace/strategy.go` engine references

### Task 3.10: Full test suite

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

---

## Notes

- **Always run tests** after each task: `go test ./...`
- **Context running low?** Update this memory before ending session
- **Integration tests**: `go test ./pkg/cmd/... -tags=integration -v -timeout 10m`
- **Plan file**: `~/.claude/plans/warm-humming-ocean.md`
