# Config Facade Refactoring Progress

## End Goal
Convert `config.Config` from a lazy-loading gateway pattern to an eager-loading facade pattern where:
- `NewConfig()` takes no arguments (uses `os.Getwd()` internally)
- WorkDir cannot be overridden
- `Project`, `Settings`, `Resolution`, `ProjectInfo` are public struct fields (not methods)
- Tests use `os.Chdir()` pattern or `NewConfigForTest(project, settings)`

## Status: COMPLETE ✓ (PR Review Fixes Applied)

### PR Review Fixes (2026-02-03)

All 9 issues from the PR review have been addressed:

1. **Issue #1: Encapsulation Violation** ✓
   - Added `UpdateProject(key, func(*ProjectEntry) error) error` to RegistryLoader
   - Updated `setWorktreeSlug` and `deleteWorktreeSlug` to use new method

2. **Issue #2: YAML Parse Errors Silently Use Defaults** ✓
   - `loadProject()` now distinguishes "config not found" from "config invalid"
   - Invalid YAML triggers `os.Exit(1)` with clear error message

3. **Issue #3: Thread Safety Race Condition** ✓
   - Added `worktreeMu sync.RWMutex` field to Project struct
   - Worktree read operations use RLock, write operations use Lock
   - `getWorktrees()` returns defensive copy of map

4. **Issue #4: CLAUDE.md References Non-Existent ProjectInfo** ✓
   - Removed all ProjectInfo references
   - Updated Key Files table: `project_info.go` → `project_runtime.go`
   - Replaced "ProjectInfo" section with "Project Runtime Context" section

5. **Issue #5: Missing Test Coverage for Worktree Methods** ✓
   - Created `internal/config/project_runtime_test.go`
   - Tests for all worktree methods and edge cases

6. **Issue #6: os.Getwd() Failure Only Warns** ✓
   - Changed to `os.Exit(1)` with clear error message
   - Removed unused `loadDefaults()` method

7. **Issue #7: NewConfigForTest Incomplete Runtime Context** ✓
   - Documented limitation in function comment
   - Tests needing full context should use NewConfig() with os.Chdir()

8. **Issue #8: Document Schema/Runtime Mixing** ✓
   - Added doc comment to Project struct explaining design

9. **Issue #9: Store Registry Init Error** ✓
   - Added `registryInitErr` field to Config
   - Added `RegistryInitErr()` method for diagnostics

### Verification

- `go build ./...` — PASSING
- `make test` — 2878 tests passed, 6 skipped

### Completed Steps ✓

1. **Rewrote internal/config/config.go** - Core facade type
   - `NewConfig()` with no arguments, calls `os.Getwd()` internally
   - Public fields: `Project`, `Settings`, `Resolution`, `ProjectInfo`, `Registry`
   - `NewConfigForTest(project *Project, settings *Settings) *Config` - 2 arguments only
   - `SettingsLoader()` and `ProjectLoader()` return single values (no error)

2. **Updated internal/cmd/factory/default.go**
   - `configFunc()` calls `NewConfig()` without arguments

3. **Updated internal/project/register.go**
   - Changed signature to take `*config.RegistryLoader` directly (not closure)

4. **Rewrote internal/config/config_test.go**
   - Uses `testChdir(t, dir)` pattern for tests
   - All config facade tests passing

5. **Updated ~20+ command files**
   - Changed `.Project()` method calls to `.Project` field access
   - Changed `.Settings()`, `.Resolution()`, `.ProjectInfo()` similarly
   - Changed `SettingsLoader()` callers to remove error handling

6. **Fixed test files with old 3-argument signature**
   - internal/docker/image_resolve_test.go - fixed
   - internal/cmd/container/run/run_test.go - fixed
   - test/internals/image_resolver_test.go - fixed
   - Removed unused `tmpDir` variable in run_test.go

7. **Fixed internal/config/debug_test.go**
   - Added `t.Skip()` for developer-only debug test

8. **Updated internal/config/CLAUDE.md**
   - Documentation reflects new facade pattern

### Verification Status

- **Full build**: `go build ./...` - PASSING
- **Unit tests**: `make test` - 2885 tests passed, 6 skipped

### Final Fixes (2026-02-03)

1. Fixed `internal/cmd/container/run/run_test.go` lines 814 and 914 — removed workDir argument from `NewConfigForTest` calls
2. Removed unused `tmpDir` variable in `run_test.go` line 807
3. Removed `--workdir` flag test assertion from `internal/cmd/root/root_test.go`
4. Updated `internal/cmd/root/CLAUDE.md` to remove `--workdir` flag documentation
5. Fixed macOS symlink issue in `internal/config/config_test.go` — added `filepath.EvalSymlinks` for `/var` vs `/private/var`

### Key Pattern Changes

**Old (gateway)**:
```go
cfg, err := cfgGateway.Project()
settings, err := cfgGateway.Settings()
```

**New (facade)**:
```go
cfg := cfgGateway.Project      // direct field access, no error
settings := cfgGateway.Settings
```

**Test pattern (old)**: `config.NewConfigForTest(workDir, project, settings)`
**Test pattern (new)**: `config.NewConfigForTest(project, settings)` - only 2 args

### Files Modified

Core:
- internal/config/config.go (complete rewrite)
- internal/config/config_test.go (complete rewrite)
- internal/config/debug_test.go (skip added)
- internal/config/CLAUDE.md (updated)
- internal/cmd/factory/default.go
- internal/project/register.go

Test files fixed:
- internal/docker/image_resolve_test.go
- internal/cmd/container/run/run_test.go
- test/internals/image_resolver_test.go
- test/harness/factory.go

Command files (changed method to field access):
- Multiple files in internal/cmd/container/*, internal/cmd/project/*, etc.

---

**IMPORTANT**: Before proceeding with any remaining work, check with the user. If all work is complete and verified, ask the user if they want to delete this memory.
