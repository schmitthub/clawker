# Project Package

Project domain layer for project registration (root identity), path resolution, and worktree orchestration.

## Recent Changes (Current State)

- Project identity is now **root-path based** (`ProjectEntry.Root`), not key/slug based.
- Public constructor is `NewProjectManager(cfg, gitFactory)`; `gitFactory` is optional (nil = production `git.NewGitManager`).
- Registry persistence now uses `projects` as list semantics with compatibility decoding for legacy map-shaped data.
- Registration is non-idempotent by root: registering the same root again returns `ErrProjectExists`.
- Worktree orchestration moved behind package-private `worktreeService`, exposed through `Project` methods.
- Worktree directories use **flat UUID-based naming**: `<repoName>-<projectName>-<sha1(uuid)[:12]>` under `cfg.WorktreesSubdir()`.
- `AddWorktree` rejects duplicates with `ErrWorktreeExists` when a branch is already registered.
- `RemoveWorktree` accepts `deleteBranch bool` — project layer handles branch deletion with safety (swallows `ErrBranchNotFound`, returns other errors wrapped).
- `ProjectManager.ListWorktrees(ctx)` aggregates worktrees across all registered projects.
- `Project.ListWorktrees(ctx)` returns enriched worktree state with git-level detail (HEAD, detached state, inspect errors).
- `WorktreeState.Project` field identifies which project a worktree belongs to.

## Boundary

- `internal/config` owns config/path primitives (`GetProjectRoot`, `GetProjectIgnoreFile`, `Write`, env/path resolution).
- `internal/project` owns project CRUD semantics, project resolution, and worktree lifecycle orchestration.
- Callers should consume `ProjectManager`/`Project` interfaces instead of mutating registry data directly.

## Visibility Rules

- Public: interfaces and DTO-ish types (`ProjectManager`, `Project`, `ProjectRecord`, `WorktreeRecord`, `WorktreeState`, `WorktreeStatus`, `PruneStaleResult`, `GitManagerFactory`, error sentinels).
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

Behavior notes:

- `List` sorts by root then name.
- `ResolvePath` and registry lookup normalize with `Abs` + `EvalSymlinks` fallback.
- `CurrentProject` first attempts `cfg.GetProjectRoot()`, then falls back to `os.Getwd()`.
- `ListWorktrees` iterates all registered projects, calling `proj.ListWorktrees()` on each, and aggregates results.

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

`CreateWorktree` and `AddWorktree` share creation flow; `AddWorktree` returns a caller-facing `WorktreeState`.

`RemoveWorktree` with `deleteBranch=true`: worktree is always removed even if branch deletion fails. `ErrBranchNotFound` is silently swallowed; other branch errors are returned wrapped with `"worktree removed but deleting branch: %w"`.

`ListWorktrees` enriches registry data with git-level detail: HEAD commit (short hash), detached state, and inspect errors via `gitMgr.ListWorktrees()`.

## Data Types

```go
type ProjectRecord struct {
    Name      string
    Root      string
    Worktrees map[string]WorktreeRecord
}

type WorktreeRecord struct {
    Path   string
    Branch string
}

type WorktreeState struct {
    Project          string         // project name (set by ListWorktrees, AddWorktree)
    Branch           string
    Path             string
    Head             string         // short commit hash from git
    IsDetached       bool
    ExistsInRegistry bool
    ExistsInGit      bool
    Status           WorktreeStatus // healthy, registry_only, git_only, broken
    InspectError     error
}

type WorktreeStatus string
const (
    WorktreeHealthy      WorktreeStatus = "healthy"
    WorktreeRegistryOnly WorktreeStatus = "registry_only"
    WorktreeGitOnly      WorktreeStatus = "git_only"
    WorktreeBroken       WorktreeStatus = "broken"
)
```

## Error Sentinels

- `ErrProjectNotFound`
- `ErrProjectExists`
- `ErrWorktreeNotFound`
- `ErrWorktreeExists` — duplicate branch registration rejected by `AddWorktree`
- `ErrProjectHandleNotInitialized`
- `ErrNotInProjectPath`
- `ErrProjectNotRegistered`

Use `errors.Is` at command boundaries.

## Registry Facade (`registry.go`)

Core responsibilities:

- Read project list via `cfg.Get("projects")`.
- Decode both:
  - list-shaped registry (`[]any`)
  - legacy map-shaped registry (`map[string]any`)
- Stage updates with `cfg.Set("projects", raw)`.
- Persist with `cfg.Write(WriteOptions{Scope: ScopeRegistry})`.
- Fallback persistence path when registry path is unconfigured: `ConfigDir()/cfg.ProjectRegistryFileName()`.

Internal ops:

- `Register(displayName, rootDir)`
- `Update(entry)`
- `RemoveByRoot(root)`
- `ProjectByRoot(root)`
- `registerWorktree(projectRoot, branch, path)`
- `unregisterWorktree(projectRoot, branch)`

## Worktree Service (`worktree_service.go`)

### Worktree Directory Naming

Worktree directories use **flat UUID-based naming** under `cfg.WorktreesSubdir()`:

```text
<WorktreesSubdir>/<repoName>-<projectName>-<sha1(uuid.New())[:12]>
```

- `repoName` = `filepath.Base(projectRoot)`, slugified
- `projectName` = `entry.Name` (falls back to `repoName` if empty), slugified
- UUID is freshly generated per worktree creation via `google/uuid`
- Registry (`ProjectEntry.Worktrees[branch].Path`) is the source of truth for path lookups

`flatWorktreeDirProvider` implements `git.WorktreeDirProvider`:
- `GetOrCreateWorktreeDir`: reuses known path from registry, generates UUID-based path for new entries
- `GetWorktreeDir`: lookup from registry-backed `knownPaths` map
- `DeleteWorktreeDir`: lookup + `os.RemoveAll`

### Public helper

```go
func NewWorktreeDirProvider(cfg config.Config, projectRoot string) git.WorktreeDirProvider
```

For external callers (e.g. `container/shared`) that need a `WorktreeDirProvider` without going through the full project service. Internally looks up the project in the registry to populate known paths.

### Core flow for add/remove

1. Resolve project root from config.
2. Find project in registry (reject if unregistered).
3. Check for duplicate branch registration (`AddWorktree` only).
4. Build `gitManager` for project root.
5. Use `flatWorktreeDirProvider` with known paths from registry.
6. Apply git operation (`SetupWorktree` / `RemoveWorktree`).
7. Stage and persist registry mutation.
8. Optional branch deletion (`RemoveWorktree` with `deleteBranch=true`).

`PruneStaleWorktrees` marks entries prunable when any of:

- worktree directory is missing

- git worktree metadata is missing
- branch is deleted

It supports dry-run and partial-failure reporting through `PruneStaleResult`.

## Test Coverage (What is actively validated)

- Registration and duplicate-root conflict behavior.
- Root-based remove/get/list semantics.
- Nil-handle guards.
- Worktree add/remove registry mutation behavior.
- Duplicate worktree rejection (`ErrWorktreeExists`).
- In-memory git manager behavior for realistic worktree lifecycle tests.
- Stale prune path and failure accounting via `PruneStaleResult`.
- Full lifecycle: register → add worktree → list → remove worktree (with/without branch deletion) → remove project.

## Test Doubles for Dependents (`mocks/`)

`internal/project/mocks/` provides pre-wired doubles for downstream packages that depend on `ProjectManager`/`Project`. Import as:

```go
projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
```

### Available Stubs

- `NewMockProjectManager()` — panic-safe `*ProjectManagerMock` with no-op defaults for all methods including `ListWorktrees`.
- `NewMockProject(name, repoPath)` — `*ProjectMock` with read accessors and no-op mutation methods.
- `NewTestProjectManager(t, gitFactory)` — real `ProjectManager` backed by `configmocks.NewIsolatedTestConfig(t)`.

### Minimal Usage

```go
// 1) Pure mock
mgr := projectmocks.NewMockProjectManager()
mgr.GetFunc = func(_ context.Context, root string) (project.Project, error) {
    return projectmocks.NewMockProject("demo", root), nil
}

// 2) Real manager with isolated config
mgr := projectmocks.NewTestProjectManager(t, nil)
_, err := mgr.Register(context.Background(), "Demo", t.TempDir())
```

## Dependencies

- `internal/config`
- `internal/git`
- `internal/text`
- `github.com/google/uuid`
