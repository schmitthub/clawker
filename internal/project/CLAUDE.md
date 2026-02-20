# Project Package

Project domain layer for project registration (root identity), path resolution, and worktree orchestration.

## Recent Changes (Current State)

- Project identity is now **root-path based** (`ProjectEntry.Root`), not key/slug based.
- Public constructor is `NewProjectManager(cfg)`; stale references to `NewService` are invalid in this package.
- Registry persistence now uses `projects` as list semantics with compatibility decoding for legacy map-shaped data.
- Registration is non-idempotent by root: registering the same root again returns `ErrProjectExists`.
- Worktree orchestration moved behind package-private `worktreeService`, exposed through `Project` methods.
- Worktree directories are namespaced under `cfg.WorktreesSubdir()` using a stable hash of resolved project root.

## Boundary

- `internal/config` owns config/path primitives (`GetProjectRoot`, `GetProjectIgnoreFile`, `Write`, env/path resolution).
- `internal/project` owns project CRUD semantics, project resolution, and worktree lifecycle orchestration.
- Callers should consume `ProjectManager`/`Project` interfaces instead of mutating registry data directly.

## Visibility Rules

- Public: interfaces and DTO-ish types (`ProjectManager`, `Project`, `ProjectRecord`, `WorktreeRecord`, `WorktreeState`, `PruneStaleResult`, error sentinels).
- Private implementation: `projectManager`, `projectHandle`, `projectRegistry`, `worktreeService`.

## Key Files

| File | Purpose |
|---|---|
| `manager.go` | Public interfaces, constructor, project handle behavior |
| `registry.go` | Internal registry facade over `config.Config` read/set/write |
| `worktree_service.go` | Internal git + registry orchestration for worktrees |
| `register_test.go` | Registration and root-based removal behavior |
| `service_test.go` | Manager sorting/not-found and handle guard behavior |
| `worktree_service_test.go` | Worktree add/remove/prune integration behavior |

## Public API

### Constructor

```go
func NewProjectManager(cfg config.Config) ProjectManager
```

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
}
```

Behavior notes:

- `List` sorts by root then name.
- `ResolvePath` and registry lookup normalize with `Abs` + `EvalSymlinks` fallback.
- `CurrentProject` first attempts `cfg.GetProjectRoot()`, then falls back to `os.Getwd()`.

### `Project`

```go
type Project interface {
    Name() string
    RepoPath() string
    Record() (ProjectRecord, error)

    CreateWorktree(ctx context.Context, branch, base string) (string, error)
    AddWorktree(ctx context.Context, branch, base string) (WorktreeState, error)
    RemoveWorktree(ctx context.Context, branch string) error
    PruneStaleWorktrees(ctx context.Context, dryRun bool) (*PruneStaleResult, error)
    ListWorktrees(ctx context.Context) ([]WorktreeState, error)
    GetWorktree(ctx context.Context, branch string) (WorktreeState, error)
}
```

`CreateWorktree` and `AddWorktree` currently share creation flow; `AddWorktree` returns a caller-facing `WorktreeState`.

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
    Branch           string
    Path             string
    Head             string
    IsDetached       bool
    ExistsInRegistry bool
    ExistsInGit      bool
    Status           WorktreeStatus
    InspectError     error
}
```

Current `ListWorktrees` state is record-backed and conservative (`ExistsInRegistry`/`ExistsInGit` default true in current implementation path).

## Error Sentinels

- `ErrProjectNotFound`
- `ErrProjectExists`
- `ErrWorktreeNotFound`
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
- Fallback persistence path when registry path is unconfigured: `ConfigDir()/projects.yaml`.

Internal ops:

- `Register(displayName, rootDir)`
- `Update(entry)`
- `RemoveByRoot(root)`
- `ProjectByRoot(root)`
- `registerWorktree(projectRoot, branch, path)`
- `unregisterWorktree(projectRoot, branch)`

## Worktree Service (`worktree_service.go`)

Core flow for add/remove:

1. Resolve project root from config.
2. Verify root is registered.
3. Build `gitManager` for project root.
4. Use `configDirWorktreeProvider` rooted at hashed namespace under `cfg.WorktreesSubdir()`.
5. Apply git operation (`SetupWorktree` / `RemoveWorktree`).
6. Stage and persist registry mutation.

`PruneStaleWorktrees` marks entries prunable when:

- worktree directory is missing
- and git worktree metadata is missing

It supports dry-run and partial-failure reporting through `PruneStaleResult`.

## Test Coverage (What is actively validated)

- Registration and duplicate-root conflict behavior.
- Root-based remove/get/list semantics.
- Nil-handle guards.
- Worktree add/remove registry mutation behavior.
- In-memory git manager behavior for realistic worktree lifecycle tests.
- Stale prune path and failure accounting via `PruneStaleResult`.

## Test Doubles for Dependents (`stubs.go`)

`internal/project/stubs.go` provides pre-wired doubles for downstream packages that depend on `ProjectManager`/`Project`.

### Scenario Mapping

- **Pure mock (no config/git reads or writes)**
    - Use `NewProjectManagerMock()`
    - Returns a panic-safe `*ProjectManagerMock` with overridable function fields.
- **Read-only config + in-memory git**
    - Use `NewReadOnlyTestManager(t, yaml)`
    - Config is `config.NewFromString(yaml)` (read-only semantics), git is `gittest.NewInMemoryGitManager`.
    - `Register` / `Update` / `Remove` return `ErrReadOnlyTestManager`.
- **Isolated writable config + in-memory git**
    - Use `NewIsolatedTestManager(t)`
    - Config is `config.NewIsolatedTestConfig(t)` (safe file read/write in temp dirs), git is `gittest.NewInMemoryGitManager`.
    - Includes `ReadConfigFiles` callback for assertions over persisted settings/project/registry files.

### Minimal Usage

```go
// 1) Pure mock
mgr := project.NewProjectManagerMock()
mgr.GetFunc = func(_ context.Context, root string) (project.Project, error) {
        return project.NewProjectMockFromRecord(project.ProjectRecord{Name: "demo", Root: root}), nil
}

// 2) Read-only config + in-memory git
h := project.NewReadOnlyTestManager(t, `projects: [{name: Demo, root: /tmp/demo}]`)
projects, err := h.Manager.List(context.Background())

// 3) Isolated writable config + in-memory git
h := project.NewIsolatedTestManager(t)
_, err := h.Manager.Register(context.Background(), "Demo", t.TempDir())
```

## Dependencies

- `internal/config`
- `internal/git`
- `internal/iostreams`
- `internal/text`
