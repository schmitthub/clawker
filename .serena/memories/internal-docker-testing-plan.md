# Phase 2: internal/docker/ Testing Plan

Detailed sub-plan for the testing initiative Phase 2. Tests `internal/docker/` methods using `whailtest.FakeAPIClient` injected through `whail.NewFromExisting`.

**Branch:** `a/docker-internal-testing`
**Parent memory:** `testing-initiative-master-plan`
**PRD Reference:** `.claude/prds/cli_testing_adaptation/`

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Pure function tests | `pending` | — |
| Task 2: Build output parsing tests | `pending` | — |
| Task 3: Client methods with faked engine | `pending` | — |
| Task 4: Volume methods with faked engine | `pending` | — |

---

## Current Test Inventory

| File | Coverage | Notes |
|------|----------|-------|
| `labels_test.go` | 100% | Label generation, merging |
| `names_test.go` | 100% | Container/volume naming conventions |
| `opts_test.go` | 100% | Container options building |
| `client_integration_test.go` | Partial | Integration tests (requires Docker) |
| `client.go` | **Partial** | 11 methods/functions, many untested at unit level |
| `volume.go` | **ZERO** | 6 functions/methods, no unit tests |

## Target Functions/Methods

### client.go — Untested

| Symbol | Type | Mock Needs |
|--------|------|------------|
| `parseContainers` | Function | None (pure) |
| `isNotFoundError` | Function | None (pure) |
| `(*Client).IsMonitoringActive` | Method | FakeAPIClient (ContainerListFn — bypasses whail) |
| `(*Client).ImageExists` | Method | FakeAPIClient (ImageInspectFn — bypasses whail) |
| `(*Client).ListContainers` | Method | FakeAPIClient (ContainerListFn — through whail) |
| `(*Client).ListContainersByProject` | Method | FakeAPIClient (ContainerListFn — through whail) |
| `(*Client).FindContainerByAgent` | Method | FakeAPIClient (ContainerListFn — through whail) |
| `(*Client).BuildImage` | Method | FakeAPIClient (ImageBuildFn) |
| `(*Client).processBuildOutput` | Method | io.Reader only (no Docker) |
| `(*Client).processBuildOutputQuiet` | Method | io.Reader only (no Docker) |
| `(*Client).RemoveContainerWithVolumes` | Method | FakeAPIClient (multi-method: ContainerInspect, ContainerRemove, VolumeList, VolumeRemove) |

### volume.go — Untested (ALL)

| Symbol | Type | Mock Needs |
|--------|------|------------|
| `(*Client).EnsureVolume` | Method | FakeAPIClient (VolumeInspect, VolumeCreate) |
| `(*Client).CopyToVolume` | Method | FakeAPIClient (ContainerCreate, CopyToContainer, ContainerRemove) + tar creation |
| `createTarArchive` | Function | None (pure — filesystem + io) |
| `shouldIgnore` | Function | None (pure) |
| `matchPattern` | Function | None (pure) |
| `LoadIgnorePatterns` | Function | None (pure — filesystem read) |

---

## Test Helper Pattern

All tests requiring a Docker client use this pattern:

```go
// newTestClient creates a docker.Client backed by a FakeAPIClient for unit testing.
// Configure fake.XxxFn fields before calling client methods.
func newTestClient(t *testing.T) (*Client, *whailtest.FakeAPIClient) {
    t.Helper()
    fake := whailtest.NewFakeAPIClient()
    engine := whail.NewFromExisting(fake,
        whail.WithLabelPrefix("com.clawker"),
        whail.WithManagedLabel("managed"),
    )
    client := &Client{Engine: engine}
    return client, fake
}
```

**Key insight:** `docker.Client` embeds `*whail.Engine`. Some methods call the moby API directly via `c.Engine.Client()` (bypassing whail jail), while others call whail methods (which go through jail logic). Both ultimately hit the same `FakeAPIClient` Fn fields.

- **APIClient-direct:** `IsMonitoringActive`, `ImageExists` — call `c.Engine.Client().ContainerList(...)` / `c.Engine.Client().ImageInspect(...)` directly
- **Whail-delegating:** `ListContainers`, `FindContainerByAgent`, `BuildImage`, `RemoveContainerWithVolumes`, `EnsureVolume`, `CopyToVolume` — call whail methods which apply jail logic (labels, filters)

---

## Task 1: Pure Function Tests

**Creates:** `internal/docker/client_test.go` (new unit test file, no build tag)
**Mock needs:** None — all pure functions

### Test Table

| Test | Function | Subtests |
|------|----------|----------|
| `TestParseContainers` | `parseContainers` | empty list; single container; multiple containers; missing labels graceful |
| `TestIsNotFoundError` | `isNotFoundError` | nil error; non-not-found error; errdefs.NotFoundError; wrapped not-found |
| `TestShouldIgnore` | `shouldIgnore` | empty patterns; exact match; glob match; no match; `.git` default |
| `TestMatchPattern` | `matchPattern` | exact match; wildcard; directory glob; no match; case sensitivity |
| `TestLoadIgnorePatterns` | `LoadIgnorePatterns` | file not found (returns defaults); valid file; comments stripped; empty lines stripped |
| `TestCreateTarArchive` | `createTarArchive` | single file; multiple files; respects ignore patterns; empty directory |

### Implementation Notes

- `parseContainers` takes `[]container.Summary` and returns `[]Container` — need to construct input with labels `com.clawker.project`, `com.clawker.agent`
- `isNotFoundError` uses `errdefs.IsNotFound()` from moby — check exact import
- `shouldIgnore`/`matchPattern`/`LoadIgnorePatterns` are in `volume.go`
- `createTarArchive` needs a temp directory with test files — use `t.TempDir()`

### Acceptance Criteria

```bash
go test ./internal/docker/ -v -run "TestParseContainers|TestIsNotFoundError|TestShouldIgnore|TestMatchPattern|TestLoadIgnorePatterns|TestCreateTarArchive" -count=1
go test ./internal/docker/... -count=1
```

### Wrap Up
1. Update Progress Tracker: Task 1 → `complete`
2. Append key learnings
3. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the docker testing initiative. Read Serena memory `internal-docker-testing-plan` — Task 1 is complete. Begin Task 2: Build output parsing tests."

---

## Task 2: Build Output Parsing Tests

**Modifies:** `internal/docker/client_test.go`
**Mock needs:** `io.Reader` only — no Docker client needed

### Test Table

| Test | Method | Subtests |
|------|--------|----------|
| `TestProcessBuildOutput` | `processBuildOutput` | success stream; error in stream; mixed stream+error; empty stream |
| `TestProcessBuildOutputQuiet` | `processBuildOutputQuiet` | success (no output); error in stream; returns error message |

### Implementation Notes

- Both methods read from `io.Reader` containing JSON-encoded `buildEvent` structs (one per line)
- `processBuildOutput` writes stream output to `c.Engine`... actually check — it may write to an `io.Writer` parameter or use the client's IOStreams
- `processBuildOutputQuiet` suppresses output, only returns errors
- Create test input as `bytes.NewReader` with JSON lines:
  ```go
  input := `{"stream":"Step 1/3 : FROM node:20\n"}` + "\n" +
           `{"stream":"Step 2/3 : RUN echo hello\n"}` + "\n"
  ```
- For error cases:
  ```go
  input := `{"error":"build failed","errorDetail":{"message":"build failed"}}` + "\n"
  ```

### Acceptance Criteria

```bash
go test ./internal/docker/ -v -run "TestProcessBuildOutput" -count=1
go test ./internal/docker/... -count=1
```

### Wrap Up
1. Update Progress Tracker: Task 2 → `complete`
2. Append key learnings (exact processBuildOutput signature, how it handles io.Writer)
3. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the docker testing initiative. Read Serena memory `internal-docker-testing-plan` — Tasks 1-2 are complete. Begin Task 3: Client methods with faked engine."

---

## Task 3: Client Methods with Faked Engine

**Modifies:** `internal/docker/client_test.go`
**Mock needs:** `whailtest.FakeAPIClient` via `newTestClient()` helper

### Test Table

| Test | Method | Fn Fields | Subtests |
|------|--------|-----------|----------|
| `TestImageExists` | `ImageExists` | `ImageInspectFn` | exists; not found; other error |
| `TestIsMonitoringActive` | `IsMonitoringActive` | `ContainerListFn` | active (containers found); inactive (empty list); error returns false |
| `TestListContainers` | `ListContainers` | `ContainerListFn` | empty; with containers; filters applied correctly |
| `TestListContainersByProject` | `ListContainersByProject` | `ContainerListFn` | project filter applied; empty result |
| `TestFindContainerByAgent` | `FindContainerByAgent` | `ContainerListFn` | found; not found; multiple (returns first) |
| `TestBuildImage` | `BuildImage` | `ImageBuildFn` | success; build error; labels applied |
| `TestRemoveContainerWithVolumes` | `RemoveContainerWithVolumes` | `ContainerInspectFn`, `ContainerRemoveFn`, `VolumeListFn`, `VolumeRemoveFn` | removes container + volumes; container not found; no volumes |

### Implementation Notes

- `ImageExists` calls `c.Engine.Client().ImageInspect(ctx, ref)` directly — bypasses whail jail
- `IsMonitoringActive` calls `c.Engine.Client().ContainerList(ctx, opts)` directly — check exact filter it uses
- `ListContainers` goes through whail — whail injects managed label filter. The FakeAPIClient `ContainerListFn` receives the post-injection filters
- `FindContainerByAgent` adds agent label filter on top of managed filter
- `BuildImage` needs `ImageBuildFn` to return an `io.ReadCloser` with JSON build events
- `RemoveContainerWithVolumes` is complex: inspects container for mounts, removes container, lists volumes matching mount names, removes each volume

### Acceptance Criteria

```bash
go test ./internal/docker/ -v -run "TestImageExists|TestIsMonitoringActive|TestListContainers|TestFindContainerByAgent|TestBuildImage|TestRemoveContainerWithVolumes" -count=1
go test ./internal/docker/... -count=1
```

### Wrap Up
1. Update Progress Tracker: Task 3 → `complete`
2. Append key learnings
3. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the docker testing initiative. Read Serena memory `internal-docker-testing-plan` — Tasks 1-3 are complete. Begin Task 4: Volume methods with faked engine."

---

## Task 4: Volume Methods with Faked Engine

**Modifies:** `internal/docker/volume_test.go` (new file)
**Mock needs:** `whailtest.FakeAPIClient` via `newTestClient()` helper

### Test Table

| Test | Method | Fn Fields | Subtests |
|------|--------|-----------|----------|
| `TestEnsureVolume` | `EnsureVolume` | `VolumeInspectFn`, `VolumeCreateFn` | volume exists (no create); volume not found (creates); create error |
| `TestCopyToVolume` | `CopyToVolume` | `ContainerCreateFn`, `CopyToContainerFn`, `ContainerRemoveFn` | success; creates temp container with volume mount; copies tar; removes container; copy error cleans up |

### Implementation Notes

- `EnsureVolume` checks if volume exists via `VolumeInspect`, creates if not found
- `CopyToVolume` is the most complex method:
  1. Calls `LoadIgnorePatterns` to get ignore list
  2. Calls `createTarArchive` to create tar of source directory
  3. Creates a temporary container with the target volume mounted
  4. Copies the tar archive into the container
  5. Removes the temporary container
- For testing `CopyToVolume`, create a real temp directory with files, but fake the Docker operations
- The container create/copy/remove all go through whail, so FakeAPIClient Fn fields are used
- Need to set up `ContainerCreateFn` to return a valid container ID
- `CopyToContainerFn` should verify the tar content is correct (input-spy pattern)
- `ContainerRemoveFn` should be called even on copy failure (cleanup)

### Acceptance Criteria

```bash
go test ./internal/docker/ -v -run "TestEnsureVolume|TestCopyToVolume" -count=1
go test ./internal/docker/... -count=1
go test ./... -count=1  # full suite, no regressions
```

### Wrap Up
1. Update Progress Tracker: Task 4 → `complete`
2. Append key learnings
3. Update `internal/docker/CLAUDE.md` with new test file references
4. Update `testing-initiative-master-plan` memory: Phase 2 → `COMPLETE`
5. Run final verification:
   ```bash
   go test ./internal/docker/... -v -count=1  # all docker tests
   go test ./... -count=1                       # full suite
   ```
6. **STOP.** Inform the user Phase 2 is complete. Suggest Phase 3 (dockertest package) as next step.

---

## Key Learnings

(Agents append here as they complete tasks)

---

## GoMock Assessment

**Status:** Keep for now — DO NOT modify existing GoMock-based tests in Phase 2.

- ~60+ command tests in `internal/cmd/*/` use GoMock-generated mocks
- GoMock deprecation is Phase 4 scope
- New tests in Phase 2 exclusively use the function-field pattern
- The `mock_docker/` package (if it exists) is not touched in this phase

---

## Agent Rules

1. Read `CLAUDE.md`, `.claude/rules/testing.md`, `internal/docker/CLAUDE.md` before starting
2. Use Serena tools for code exploration — read symbol bodies only when needed
3. Test file uses `package docker` (internal test package) — can access unexported functions
4. `newTestClient()` helper goes in a `_test.go` file (shared across test files via same package)
5. Never import `moby/moby/client` directly — use types through `pkg/whail` or `pkg/whail/whailtest`
6. All new tests must compile and pass: `go test ./internal/docker/... -count=1`
7. Follow existing test patterns in `labels_test.go`, `names_test.go`, `opts_test.go`

---

## CRITICAL: Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append key learnings to the Key Learnings section
4. Present the handoff prompt from the task's Wrap Up section to the user
5. Wait for the user to start a new conversation with the handoff prompt
