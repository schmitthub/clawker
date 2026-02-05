# Worktree Test Coverage Improvement Plan

## STATUS: COMPLETED

## Approach: Proper Interface Extraction

Extract interfaces for `Registry`, `ProjectHandle`, `WorktreeHandle` to enable clean DI and fully in-memory testing.

### Interfaces (in `internal/config/registry.go`)

```go
type Registry interface {
    Project(key string) ProjectHandle
    Load() (*ProjectRegistry, error)
    Save(r *ProjectRegistry) error
    Register(displayName, rootDir string) (string, error)
    Unregister(key string) (bool, error)
    UpdateProject(key string, fn func(*ProjectEntry) error) error
    Path() string
    Exists() bool
}

type ProjectHandle interface {
    Key() string
    Get() (*ProjectEntry, error)
    Root() (string, error)
    Exists() (bool, error)
    Update(fn func(*ProjectEntry) error) error
    Delete() (bool, error)
    Worktree(name string) WorktreeHandle
    ListWorktrees() ([]WorktreeHandle, error)
}

type WorktreeHandle interface {
    Name() string
    Slug() string
    Path() (string, error)
    DirExists() bool
    GitExists() bool
    Status() *WorktreeStatus
    Delete() error
}
```

### In-Memory Registry (configtest)

- `InMemoryRegistry` implements `config.Registry`
- `SetWorktreeState(project, worktree, dirExists, gitExists)` controls DirExists/GitExists
- `InMemoryRegistryBuilder` with `WithHealthyWorktree()` / `WithStaleWorktree()`

### In-Memory GitManager (gittest)

- Uses `filesystem.NewStorage(memfs.New(), cache)` for worktree support
- `InMemoryGitManager` wraps `*git.GitManager` with filesystem accessors

### Test Pattern

```go
func TestListRun_StaleWorktree(t *testing.T) {
    registry := configtest.NewInMemoryRegistryBuilder().
        WithProject("test-project", "test-project", "/fake/project").
        WithStaleWorktree("stale-branch", "stale-branch").
        Registry().
        Build()

    mgr := gittest.NewInMemoryGitManager(t, "/fake/project")

    opts := &ListOptions{
        Config: func() *config.Config {
            cfg := config.NewConfigForTest(proj, nil)
            cfg.Registry = registry
            return cfg
        },
        GitManager: func() (*git.GitManager, error) {
            return mgr.GitManager, nil
        },
    }
}
```

## Files Modified

| File | Changes |
|------|---------|
| `internal/config/registry.go` | Added `Registry`, `ProjectHandle`, `WorktreeHandle` interfaces; renamed concrete types to `projectHandleImpl`, `worktreeHandleImpl` |
| `internal/config/config.go` | Changed `Registry *RegistryLoader` to `Registry Registry` (interface type) |
| `internal/config/schema.go` | Changed `registry *RegistryLoader` to `registry Registry` |
| `internal/config/project_runtime.go` | Updated `setRuntimeContext` to accept `Registry` interface |
| `internal/project/register.go` | Changed `registryLoader *RegistryLoader` to `registryLoader Registry` |
| `internal/config/configtest/inmemory_registry.go` | **NEW** - `InMemoryRegistry`, `InMemoryRegistryBuilder` with `WithHealthyWorktree`, `WithStaleWorktree` |
| `internal/config/configtest/fake_registry.go` | Updated `Build()` return type to `config.Registry` |
| `internal/git/git.go` | Added `NewGitManagerWithRepo(repo, repoRoot)` constructor |
| `internal/git/gittest/inmemory.go` | **NEW** - `InMemoryGitManager` using memfs |
| `internal/cmd/worktree/list/list.go` | Updated to use `[]config.WorktreeHandle` (interface slice) |
| `internal/cmd/worktree/prune/prune.go` | Updated to use `[]config.WorktreeHandle` (interface slice) |
| `internal/cmd/worktree/list/list_test.go` | Added `TestListRun_HealthyWorktree`, `TestListRun_StaleWorktree`, `TestListRun_MixedWorktrees` |

## Verification

All tests pass:
- `go test ./internal/config/...` - PASS
- `go test ./internal/git/...` - PASS
- `go test ./internal/cmd/worktree/...` - PASS
- `make test` - 2977 tests PASS