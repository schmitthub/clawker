# Test Infrastructure Fixes & Project Package Tests

## End Goal
Fix test infrastructure around `NewIsolatedTestConfig` and wire up proper test stubs + tests for `internal/project/` package.

## Background Context
The config mock system has three tiers:
1. **`NewBlankConfig()`** — read-only `*ConfigMock` with nil Func fields for Set/Write/path helpers. Good for consumers that only read config values.
2. **`NewFromString(yaml)`** — same as above but seeded from YAML string.
3. **`NewIsolatedTestConfig(t)`** — returns a real `config.Config` (not a mock) backed by temp directories. Supports Set/Write and all path helper methods. Use for tests needing mutation/persistence.

## Completed Work

### 1. Fixed socketbridge test panic (DONE)
- **Problem**: `TestManagerIsRunning` panicked because `NewTestManager` used `configmocks.NewBlankConfig()` which doesn't wire path helpers like `BridgePIDFilePath`, `BridgesSubdir`, `LogsSubdir`.
- **Fix**: Changed `socketbridge/mocks.NewTestManager` to use `NewIsolatedTestConfig(t)` instead. Signature changed from `NewTestManager(t, dir) *Manager` to `NewTestManager(t) (*Manager, string)` where string is the pidsDir.
- **Files**: `internal/socketbridge/mocks/stubs.go`, `internal/socketbridge/manager_test.go`
- All socketbridge tests passing.

### 2. Fixed `NewIsolatedTestConfig` missing directory creation (DONE)
- **Problem**: `NewIsolatedTestConfig` set env vars pointing to `<tempdir>/config`, `<tempdir>/data`, `<tempdir>/state` but never created those directories. Config's file-locking mechanism needs the config dir to exist.
- **Why socketbridge didn't fail**: `BridgesSubdir()` internally calls `subdirPath()` which does its own `os.MkdirAll`. Registry writes go directly to `ConfigDir()/projects.yaml` via file lock — no auto-mkdir.
- **Fix**: Added `os.MkdirAll` for all three dirs in `NewIsolatedTestConfig`.
- **File**: `internal/config/mocks/stubs.go`

### 3. Created project mock stubs (DONE)
- **`NewMockProjectManager()`** — safe no-op defaults for all `ProjectManagerMock` funcs. Get/ResolvePath/CurrentProject return `ErrProjectNotFound`.
- **`NewMockProject(name, repoPath)`** — wires read accessors (Name, RepoPath, Record). Mutation methods return zero values.
- **`NewTestProjectManager(t)`** — real `ProjectManager` backed by `NewIsolatedTestConfig` for registry persistence tests.
- **File**: `internal/project/mocks/stubs.go`

### 4. Created project manager tests (DONE)
- Tests for: Register, List, Get, Remove, Update, ResolvePath, Record
- Covers: happy paths, error cases (duplicate root, unknown root, unregistered project), persistence verification, sibling survival on removal
- All 14 test cases passing.
- **File**: `internal/project/manager_test.go`

### 5. Worktree refactor: flat UUID naming, duplicate rejection, ListWorktrees (DONE)
- Worktree dirs changed from nested `<sha1(projectRoot)[:12]>/<slugified-branch>` to flat `<repoName>-<projectName>-<sha1(uuid)[:12]>` using `google/uuid`.
- `AddWorktree` now rejects duplicates with `ErrWorktreeExists`.
- `RemoveWorktree` accepts `deleteBranch bool` — project layer handles branch deletion.
- `ProjectManager.ListWorktrees(ctx)` aggregates across all projects.
- `Project.ListWorktrees(ctx)` enriched with git-level detail (HEAD, detached, inspect errors).
- `WorktreeState.Project` field added.
- `NewWorktreeDirProvider(cfg, projectRoot)` public helper for external callers.
- Mock stubs updated: `NewMockProjectManager` includes `ListWorktreesFunc`, `NewMockProject` includes `RemoveWorktreeFunc(deleteBranch)`.
- `NewTestProjectManager(t, gitFactory)` accepts `GitManagerFactory` for worktree operation tests.
- All 14 project tests + all worktree command tests passing.

## Remaining TODOs (not started)
- [ ] Add registry edge case tests (e.g. legacy map format decoding, symlink resolution)
- [ ] Add worktree CRUD tests on Project (CreateWorktree, AddWorktree, RemoveWorktree, ListWorktrees, GetWorktree, PruneStaleWorktrees) — these require git repo setup
- [ ] Add CurrentProject tests (requires setting cwd to a registered project root)
- [ ] Review if any other packages use `NewBlankConfig()` where they should use `NewIsolatedTestConfig` (potential latent panics like socketbridge had)

## Key Files
- `internal/config/mocks/stubs.go` — NewIsolatedTestConfig (fixed)
- `internal/socketbridge/mocks/stubs.go` — NewTestManager (fixed)
- `internal/socketbridge/manager_test.go` — updated call sites
- `internal/project/mocks/stubs.go` — new stubs
- `internal/project/manager_test.go` — new tests
- `internal/project/export_test.go` — existing test exports (RegistryForTest, NewProjectHandleForTest)

## Lessons Learned
- `NewBlankConfig()` only wires read-only methods. Any code path hitting Set/Write/path helpers will panic on the mock.
- `subdirPath()` in config does its own `MkdirAll`, masking missing parent dirs. Direct file operations (like registry lock files) don't.
- Always create directories in test infrastructure, not in individual consumer stubs.

---
**IMPORTANT**: Always check with the user before proceeding with the next todo item. If all work is done, ask the user if they want to delete this memory.
