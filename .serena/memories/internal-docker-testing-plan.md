# Phase 2: internal/docker/ Testing Plan

Detailed sub-plan for the testing initiative Phase 2. Pure function unit tests for `internal/docker/` — no mocks, no Docker dependency.

**Branch:** `a/docker-internal-testing`
**Parent memory:** `testing-initiative-master-plan`
**PRD Reference:** `.claude/prds/cli_testing_adaptation/`

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Pure function unit tests (5 functions) | `complete` | Claude Opus 4.5 |

---

## Current Test Inventory

| File | Coverage | Notes |
|------|----------|-------|
| `labels_test.go` | 100% | Label generation, merging |
| `names_test.go` | 100% | Container/volume naming conventions |
| `opts_test.go` | 100% | Container options building |
| `client_integration_test.go` | Partial | Integration tests (requires Docker) |
| `client_test.go` | NEW | `parseContainers` (6 subtests), `isNotFoundError` (8 subtests) |
| `volume_test.go` | NEW | `matchPattern` (12 subtests), `shouldIgnore` (11 subtests), `LoadIgnorePatterns` (4 subtests) |
| `client.go` | **Partial** | Pure functions now tested; orchestration methods deferred |
| `volume.go` | **Partial** | Pure functions now tested; `EnsureVolume`/`CopyToVolume`/`createTarArchive` deferred |

## Audit Summary (Jan 30 2025)

Package likely needs refactoring — comprehensive mock-heavy tests for orchestration methods would be wasted effort.
Scope reduced to **pure function unit tests only**. No mocks, no FakeAPIClient, no Docker dependency.

### In Scope — 5 Pure Functions

| Symbol | File | Why It Matters |
|--------|------|---------------|
| `parseContainers` | client.go | Label extraction, slash stripping. Silent empty strings if label constants change. |
| `isNotFoundError` | client.go | Error classification controlling fatal vs ignorable in cleanup paths. |
| `shouldIgnore` | volume.go | Gitignore-style matching for `.clawkerignore`. Wrong files copied if broken. |
| `matchPattern` | volume.go | `**`, `*`, basename, prefix matching. Core logic for ignore filtering. |
| `LoadIgnorePatterns` | volume.go | File parsing (comments, blanks, defaults). Patterns silently disappear if broken. |

### Explicitly Deferred (refactoring expected)

| Symbol | Reason |
|--------|--------|
| `ListContainers`, `ListContainersByProject` | 5-line wrappers; mock test just re-tests `parseContainers` |
| `BuildImage`, `RemoveContainerWithVolumes` | Orchestration glue, likely to change |
| `EnsureVolume`, `CopyToVolume` | "Mock always passes" — tests verify Go's `if` statement |
| `ImageExists`, `IsMonitoringActive`, `FindContainerByAgent` | Thin wrappers; error classification covered by `isNotFoundError` |
| `processBuildOutput`, `processBuildOutputQuiet` | Worth testing but cut to keep scope minimal |
| `createTarArchive` | Worth testing but cut — filesystem + tar, higher effort |

---

## Test Approach

**No mocks, no FakeAPIClient, no Docker dependency.** All 5 functions are pure — they take inputs and return outputs with no side effects beyond filesystem reads (`LoadIgnorePatterns`).

- Test file: `internal/docker/client_test.go` (pure functions from client.go) and `internal/docker/volume_test.go` (pure functions from volume.go)
- Package: `package docker` (internal — access unexported functions)
- Pattern: table-driven subtests with `t.Run`
- Filesystem tests: use `t.TempDir()` for `LoadIgnorePatterns`

---

## Task 1: Pure Function Unit Tests

**Creates/modifies:** `internal/docker/client_test.go`, `internal/docker/volume_test.go`
**Mock needs:** None — all pure functions
**Single task — completes Phase 2.**

### Test Table

| Test | Function | File | Subtests |
|------|----------|------|----------|
| `TestParseContainers` | `parseContainers` | client_test.go | empty list; single container; multiple containers; missing labels graceful |
| `TestIsNotFoundError` | `isNotFoundError` | client_test.go | nil error; non-not-found error; errdefs.NotFoundError; wrapped not-found |
| `TestShouldIgnore` | `shouldIgnore` | volume_test.go | empty patterns; exact match; glob match; no match; `.git` default |
| `TestMatchPattern` | `matchPattern` | volume_test.go | exact match; wildcard; directory glob `**`; no match; case sensitivity; basename |
| `TestLoadIgnorePatterns` | `LoadIgnorePatterns` | volume_test.go | file not found (returns defaults); valid file; comments stripped; empty lines stripped |

### Implementation Notes

- `parseContainers` takes `[]container.Summary` → `[]Container` — construct input with labels `com.clawker.project`, `com.clawker.agent`
- `isNotFoundError` uses `errdefs.IsNotFound()` from moby — check exact import path
- `shouldIgnore`/`matchPattern`/`LoadIgnorePatterns` are in `volume.go`
- `LoadIgnorePatterns` needs `t.TempDir()` for filesystem tests
- All use `package docker` (internal) for access to unexported functions

### Acceptance Criteria

```bash
go test ./internal/docker/ -v -run "TestParseContainers|TestIsNotFoundError|TestShouldIgnore|TestMatchPattern|TestLoadIgnorePatterns" -count=1
go test ./internal/docker/... -count=1
go test ./... -count=1  # no regressions
```

### Wrap Up
1. Update Progress Tracker: Task 1 → `complete`
2. Append key learnings
3. Update `internal/docker/CLAUDE.md` with new test file references
4. Update `testing-initiative-master-plan` memory: Phase 2 → `COMPLETE`
5. **STOP.** Inform the user Phase 2 is complete. Suggest Phase 3 (dockertest package) as next step.

---

## Key Learnings

(Agents append here as they complete tasks)

### Task 1 (Jan 30 2025)
- `matchPattern` had a bug: `**/*.ext` didn't work because the `**` handler split on `**` and did literal `HasSuffix`. Fixed by using `filepath.Match` against basename when the suffix contains wildcards. Both `**/literal.ext` and `**/*.ext` now work correctly.
- `isNotFoundError` checks both `whail.DockerError` (via `errors.As`) and raw error strings — wrapped errors work correctly for both paths.
- `LoadIgnorePatterns` returns `[]string{}` (not nil) on file-not-found, which is the correct behavior for callers.
- `container.Summary.State` is a `string` type (not an enum), maps directly to `Container.Status`.
- Docker names always have leading `/` in the API response — `parseContainers` strips it correctly.
- New test files: `client_test.go` (8 subtests across 2 tests), `volume_test.go` (21 subtests across 3 tests). Total: 29 subtests.

---

## Phase 3 Completion (Jan 30 2025)

Phase 3 (`internal/docker/dockertest/`) is COMPLETE. Created:
- `fake_client.go` — `FakeClient` struct + `NewFakeClient()` + assertion wrappers
- `helpers.go` — `ContainerFixture`, `RunningContainerFixture`, `SetupContainerList`, `SetupFindContainer`, `SetupImageExists`
- `fake_client_test.go` — 16 smoke tests across 7 test functions

Key design: Uses `com.clawker` label prefix (not `com.whailtest`) so docker-layer methods exercise real label filtering through the whail jail. The `errNotFound` type in helpers.go satisfies `errdefs.IsNotFound` via `NotFound()` method interface.

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
