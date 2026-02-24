# Project Package

Project domain layer for project registration (root identity), path resolution, and worktree orchestration.

## Boundary

- `internal/config` owns config/path primitives (`GetProjectRoot`, `GetProjectIgnoreFile`, `Write`, env/path resolution).
- `internal/project` owns project CRUD semantics, project resolution, and worktree lifecycle orchestration.
- Callers should consume `ProjectManager`/`Project` interfaces instead of mutating registry data directly.

## Visibility Rules

- Public: interfaces and DTO types (`ProjectManager`, `Project`, `ProjectRecord`, `WorktreeRecord`, `WorktreeState`, `WorktreeStatus`, `PruneStaleResult`, `GitManagerFactory`, error sentinels).
- Public helper: `NewWorktreeDirProvider(cfg, projectRoot)` — creates a `git.WorktreeDirProvider` for external callers (e.g. `container/shared`).
- Private implementation: `projectManager`, `projectHandle`, `projectRegistry`, `worktreeService`, `flatWorktreeDirProvider`.

## Key Files

| File | Purpose |
|---|---|
| `manager.go` | Public interfaces, constructor, project handle behavior, `ListWorktrees` on both manager and handle |
| `registry.go` | Internal registry facade over `config.Config` read/set/write |
| `worktree_service.go` | Internal git + registry orchestration for worktrees, `flatWorktreeDirProvider`, `NewWorktreeDirProvider` |
| `project_test.go` | Full lifecycle tests: registration, worktree add/remove/prune, duplicate rejection |

## Public API

### Constructor

```go
func NewProjectManager(cfg config.Config, gitFactory GitManagerFactory) ProjectManager
```

`GitManagerFactory` is `func(projectRoot string) (*git.GitManager, error)`. Pass `nil` for production default (`git.NewGitManager`).

### `ProjectManager`

```go
type ProjectManager interface {
    Register(ctx context.Context, name string, repoPath string) (Project, error)
    Update(ctx context.Context, entry config.ProjectEntry) (Project, error)
    List(ctx context.Context) ([]config.ProjectEntry, error)
    Remove(ctx context.Context, root string) error
    Get(ctx context.Context, root string) (Project, error)
    ResolvePath(ctx context.Context, cwd string) (Project, error)
    CurrentProject(ctx context.Context) (Project, error)
    ListWorktrees(ctx context.Context) ([]WorktreeState, error)
}
```

- `List` sorts by root then name. `ResolvePath` normalizes with `Abs` + `EvalSymlinks` fallback.
- `CurrentProject` tries `cfg.GetProjectRoot()`, then falls back to `os.Getwd()`.
- `ListWorktrees` aggregates across all registered projects.

### `Project`

```go
type Project interface {
    Name() string
    RepoPath() string
    Record() (ProjectRecord, error)
    CreateWorktree(ctx context.Context, branch, base string) (string, error)
    AddWorktree(ctx context.Context, branch, base string) (WorktreeState, error)
    RemoveWorktree(ctx context.Context, branch string, deleteBranch bool) error
    PruneStaleWorktrees(ctx context.Context, dryRun bool) (*PruneStaleResult, error)
    ListWorktrees(ctx context.Context) ([]WorktreeState, error)
    GetWorktree(ctx context.Context, branch string) (WorktreeState, error)
}
```

- `AddWorktree` rejects duplicates with `ErrWorktreeExists`. Returns enriched `WorktreeState`.
- `RemoveWorktree(deleteBranch=true)`: worktree always removed; `ErrBranchNotFound` swallowed, other branch errors wrapped.
- `ListWorktrees` enriches registry data with git-level detail (HEAD, detached state, inspect errors) and performs multi-layer health checks: directory existence, `.git` file presence, git metadata existence, branch existence, lock file presence.

## Data Types

```go
type ProjectRecord struct {
    Name      string
    Root      string
    Worktrees map[string]WorktreeRecord
}

type WorktreeRecord struct { Path, Branch string }

type WorktreeState struct {
    Project          string
    Branch, Path     string
    Head             string         // short commit hash
    IsDetached       bool
    ExistsInRegistry bool
    ExistsInGit      bool
    Status           WorktreeStatus
    IsLocked         bool           // worktree is locked against pruning (.git/worktrees/<slug>/locked)
    InspectError     error          // non-nil indicates degraded health check (permissions, git errors)
}

type WorktreeStatus string
// WorktreeHealthy, WorktreeRegistryOnly, WorktreeGitOnly, WorktreeBroken,
// WorktreeDotGitMissing, WorktreeGitMetadataMissing
```

## Error Sentinels

`ErrProjectNotFound`, `ErrProjectExists`, `ErrWorktreeNotFound`, `ErrWorktreeExists`, `ErrProjectHandleNotInitialized`, `ErrNotInProjectPath`, `ErrProjectNotRegistered`. Use `errors.Is` at command boundaries.

## Registry Facade (`registry.go`)

Internal facade over `config.Config` for registry persistence. Decodes both list-shaped and legacy map-shaped registries. Ops: `Register`, `Update`, `RemoveByRoot`, `ProjectByRoot`, `registerWorktree`, `unregisterWorktree`.

## Worktree Service (`worktree_service.go`)

### Root Resolution

All worktree service methods accept `projectRoot` from the calling `projectHandle` (which gets it from `record.Root`). This ensures deterministic path matching against the registry — the service never re-resolves the root from the filesystem via `os.Getwd()`. The `findProjectByRoot` helper resolves symlinks on both the input and registry entries for robust matching (e.g., macOS `/var` → `/private/var`).

### Internal API

```go
CreateWorktree(_ context.Context, projectRoot, branch, base string) (string, error)
RemoveWorktree(_ context.Context, projectRoot, branch string, deleteBranch bool) error
PruneStaleWorktrees(_ context.Context, projectRoot string, dryRun bool) (*PruneStaleResult, error)
```

### Directory Naming

Flat UUID-based naming under `cfg.WorktreesSubdir()`: `<repoName>-<projectName>-<sha256(uuid)[:12]>`. Registry (`ProjectEntry.Worktrees[branch].Path`) is the source of truth for path lookups.

`flatWorktreeDirProvider` implements `git.WorktreeDirProvider`: reuses known path from registry for existing entries, generates UUID-based path for new ones.

### Public Helper

```go
func NewWorktreeDirProvider(cfg config.Config, projectRoot string) git.WorktreeDirProvider
```

For external callers needing a `WorktreeDirProvider` without the full project service.

### Prune

`PruneStaleWorktrees` marks entries prunable when: directory missing, git metadata missing, or branch deleted. Locked worktrees (`.git/worktrees/<slug>/locked` exists) are skipped even if stale, reported via `PruneStaleResult.Locked`. Supports dry-run and partial-failure reporting via `PruneStaleResult`.

## Test Doubles (`mocks/`)

Import as `projectmocks "github.com/schmitthub/clawker/internal/project/mocks"`.

- `NewMockProjectManager()` — panic-safe `*ProjectManagerMock` with no-op defaults.
- `NewMockProject(name, repoPath)` — `*ProjectMock` with read accessors and no-op mutations.
- `NewTestProjectManager(t, gitFactory)` — real `ProjectManager` backed by `configmocks.NewIsolatedTestConfig(t)`.

## Dependencies

`internal/config`, `internal/git`, `internal/text`, `github.com/google/uuid`
