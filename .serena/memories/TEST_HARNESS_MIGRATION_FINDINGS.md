# Test Harness and Config Migration Analysis

## Overview
The project is migrating from a legacy `config.Provider` interface (old test API) to a new `config.Config` interface with test stubs in `internal/config/mocks/`. This document summarizes the full scope of migration needed for test infrastructure.

## Current Architecture

### Old API (Being Removed)
- `config.NewConfigForTest(*Project, *Settings)` — factory function (location TBD, likely in old provider interface)
- Used extensively in test files to construct test configs
- Returns something that implements the old `config.Provider` interface

### New API (Replacement)
- `config.ReadFromString(yaml)` — parse YAML string, returns `config.Config` interface
- Test stubs in `internal/config/mocks/`:
  - `NewBlankConfig()` → `*ConfigMock` with defaults
  - `NewFromString(yaml)` → `*ConfigMock` with specific YAML values
  - `NewIsolatedTestConfig(t)` → file-backed `Config` for mutation tests
  - `StubWriteConfig(t)` → temp config dir isolation without full config

## Files That Need Migration

### Test Harness (test/harness/)
**File**: `/workspace/test/harness/factory.go` (lines 40, 53)
- Line 40: `cfg := config.NewConfigForTest(h.Config, nil)`
- Line 53: `Config: func() config.Provider {`
- **Migration**: Replace with `configmocks.NewFromString(h.Config)` or construct via YAML marshal
- **Impact**: This is a CRITICAL file — all integration tests use `NewTestFactory()` from here

**File**: `/workspace/test/harness/builders/config_builder.go`
- Imports `configtest` — needs to switch to `configmocks`
- Already wraps `configtest.ProjectBuilder` internally
- **Migration**: Update builder to use new mocks API, or migrate to direct YAML construction

**File**: `/workspace/test/harness/builders/config_builder_test.go`
- Tests for ConfigBuilder itself
- **Migration**: Update test helpers to use new mocks API

### Container Commands (internal/cmd/container/)
**Files**: 
- `run/run_test.go` (4 occurrences) — Lines 683, 809, 914, 930
- `create/create_test.go` (3 occurrences) — Lines 448, 601, 614
- `start/start_test.go` (2 occurrences) — Lines 228, 321

**Migration Pattern**: Replace with `configmocks.NewFromString(YAML)` or construct needed config programmatically

### Image Build Commands (internal/cmd/image/build/)
**Files**:
- `build_progress_test.go` (4 occurrences) — Lines 29, 79, 119, 154
- `build_progress_golden_test.go` (1 occurrence) — Line 35

**Migration**: Use `configmocks.NewFromString()` with minimal YAML for test configs

### Loop Commands (internal/cmd/loop/)
**Files**:
- `iterate/iterate_test.go` (1 occurrence) — Line 33
- `tasks/tasks_test.go` (1 occurrence) — Line 35

**Pattern**: Both use `config.NewConfigForTest(project, nil)` in `testFactoryWithConfig()` helper
**Migration**: Replace with `configmocks.NewFromString()` or direct config construction

### Docker/Docker Test (internal/docker/ and test/*)
**Files**:
- `dockertest/fake_client_test.go` — part of dockertest helper system
- `test/internals/image_resolver_test.go` (4 occurrences) — Lines 180, 204, 220, 267, 294
- `test/internals/docker_client_test.go` — Docker client integration tests
- `test/commands/worktree_test.go` (1 occurrence) — Line 308 (uses `NewConfigForTestWithEntry`)

**Note**: `NewConfigForTestWithEntry` is another old API variant that needs investigation

### Fawker Demo CLI
**File**: `/workspace/cmd/fawker/factory.go` (Line 73)
- Uses `config.NewConfigForTest(fawkerProject(), settings)`
- **Migration**: Replace with appropriate test config construction

## Detailed File List (234 total files import configtest or config.Provider)

### Direct `NewConfigForTest` Users (High Priority)
1. `/workspace/test/harness/factory.go` — test harness factory
2. `/workspace/test/harness/builders/config_builder.go` — builder wrapper
3. `/workspace/internal/cmd/loop/iterate/iterate_test.go` — testFactoryWithConfig
4. `/workspace/internal/cmd/loop/tasks/tasks_test.go` — testFactoryWithConfig
5. `/workspace/internal/cmd/container/run/run_test.go` — 4 direct calls
6. `/workspace/internal/cmd/container/create/create_test.go` — 3 direct calls
7. `/workspace/internal/cmd/container/start/start_test.go` — 2 direct calls
8. `/workspace/internal/cmd/image/build/build_progress_test.go` — 4 direct calls
9. `/workspace/internal/cmd/image/build/build_progress_golden_test.go` — 1 direct call
10. `/workspace/cmd/fawker/factory.go` — 1 direct call
11. `/workspace/test/internals/image_resolver_test.go` — 5 direct calls
12. `/workspace/test/commands/worktree_test.go` — uses `NewConfigForTestWithEntry` variant

### All 234 Test Files (Indirect Impact)
The grep found 234 files that import or reference config test infrastructure:
- Most unit tests use test doubles but don't directly call `NewConfigForTest`
- They import from `internal/config/mocks` or other test helpers
- **Key observation**: Once harness and builders are updated, many of these tests may work without changes

## Current Harness Patterns

### Pattern 1: TestFactory (Most Common)
```go
func testFactory(t *testing.T) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
    tio := iostreamstest.New()
    f := &cmdutil.Factory{
        IOStreams: tio.IOStreams,
        TUI:       tui.NewTUI(tio.IOStreams),
    }
    return f, tio
}
```

### Pattern 2: TestFactoryWithConfig (Needs Migration)
```go
func testFactoryWithConfig(t *testing.T) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
    tio := iostreamstest.New()
    project := config.DefaultProject()
    project.Project = "testproject"
    cfg := config.NewConfigForTest(project, nil)  // ← NEEDS MIGRATION
    f := &cmdutil.Factory{
        IOStreams: tio.IOStreams,
        TUI:       tui.NewTUI(tio.IOStreams),
        Config:    func() config.Provider { return cfg },  // ← Also returns Provider, needs Config
        Client:    ...,
    }
    return f, tio
}
```

### Pattern 3: NewTestFactory (Integration)
From `test/harness/factory.go`:
```go
func NewTestFactory(t *testing.T, h *Harness) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
    tio := iostreamstest.New()
    applyTestDefaults(h.Config)
    cfg := config.NewConfigForTest(h.Config, nil)  // ← NEEDS MIGRATION
    f := &cmdutil.Factory{
        IOStreams: tio.IOStreams,
        TUI:       tui.NewTUI(tio.IOStreams),
        Config:    func() config.Provider { return cfg },  // ← Provider needs Config
        Client:    func(ctx context.Context) (*docker.Client, error) { ... },
        ...
    }
    return f, tio
}
```

## Key Blockers to Migration

1. **`config.NewConfigForTest` doesn't exist in new API** — Need to replace with `configmocks.NewFromString()` or direct config construction
2. **`config.Provider` interface vs `config.Config` interface** — Factory fields currently return Provider, need to return Config
3. **`config.NewConfigForTestWithEntry`** variant — Need to understand what this does and provide replacement
4. **Factory.Config field type** — Currently `func() config.Provider`, needs to become `func() config.Config`
5. **Builder integration** — `test/harness/builders/config_builder.go` wraps `configtest.ProjectBuilder` which no longer exists

## Migration Strategy

### Phase 1: Update Config Package Exports
- Ensure `config.Config` interface is fully exported and usable
- Verify `configmocks` stubs are complete (they appear to be)
- Check if any adapter functions are needed between old Project structs and new API

### Phase 2: Update Test Harness (Critical Path)
1. Update `/workspace/test/harness/factory.go`:
   - Replace `NewConfigForTest(h.Config, nil)` with config construction
   - Change `Config: func() config.Provider` to `Config: func() config.Config`

2. Update `/workspace/test/harness/builders/config_builder.go`:
   - Remove dependency on `configtest.ProjectBuilder`
   - Either keep builder as-is or migrate to direct YAML construction

### Phase 3: Update Command Tests
Update each command test file that uses `config.NewConfigForTest`:
- 4 in `container/run/run_test.go`
- 3 in `container/create/create_test.go`
- 2 in `container/start/start_test.go`
- 4 in `image/build/build_progress_test.go`
- 1 in `image/build/build_progress_golden_test.go`
- 1 in `loop/iterate/iterate_test.go`
- 1 in `loop/tasks/tasks_test.go`
- 1 in `cmd/fawker/factory.go`
- 5 in `test/internals/image_resolver_test.go`

### Phase 4: Verify Integration Tests
Update integration tests that use config test infrastructure:
- `test/commands/worktree_test.go` — `NewConfigForTestWithEntry`
- Other test/* files that may depend on updated harness

## Dependencies Between Migration Tasks

```
config.Config interface (✓ ready)
    ↓
configmocks stubs (✓ ready)
    ↓
test/harness/factory.go (BLOCKER)
    ↓
All command test files (can proceed after harness)
    ↓
Integration test suite (can proceed after command tests)
```

## Notes for Implementation

1. **Backward Compatibility**: The old `config.Provider` interface is being removed entirely, so all references must be updated
2. **Test Double Selection**:
   - Use `configmocks.NewFromString()` for most tests that don't mutate config
   - Use `configmocks.NewBlankConfig()` for tests that don't care about specific values
   - Use `configmocks.NewIsolatedTestConfig(t)` for tests that mutate config
3. **YAML Formatting**: When converting `*Project` structs to YAML, use `yaml.Marshal()` or hardcode minimal YAML
4. **Environment Variables**: The new `config.Config` respects `CLAWKER_*` env vars, which old API may not have — tests may need adjustment
5. **Builder Deprecation**: `test/harness/builders/config_builder.go` should either:
   - Keep builder pattern but back it with new mocks API, OR
   - Migrate to direct YAML construction and deprecate builder entirely

## Test Coverage Impact

- **234 test files** reference config infrastructure somehow
- **12 files** directly call `config.NewConfigForTest` and need explicit migration
- **Most other files** should work fine once harness is updated (transitive dependency)
- **Estimated effort**: 4-6 hours total for careful migration + verification
