# Project Package

Project domain layer for registry CRUD, project identity resolution, and worktree lifecycle orchestration.

## Boundary

- **Path resolution** belongs to `internal/config` (`GetProjectRoot`, `GetProjectIgnoreFile`, `ConfigDir`).
- **Project CRUD and project identity/worktree orchestration** belong to `internal/project`.
- Callers should depend on project-layer services/facades rather than mutating registry data via config internals.

## Visibility Rule

- Public structs in this package are DTO/schema types only (`ProjectRecord`, `WorktreeRecord`, `WorktreeState`).
- Behavioral concrete types are private (`projectManager`, `projectHandle`, `projectRegistry`, `worktreeService`).
- External callers use `ProjectManager` and `Project` interfaces only.

## Key Files

|File|Purpose|
|---|---|
|`manager.go`|External interfaces (`ProjectManager`, `Project`) and private manager/handle implementations|
|`registry.go`|Internal registry facade backed by `config.Config` (`projects` list + `Write(ScopeRegistry)`), with legacy map-read compatibility|
|`worktree_service.go`|Internal worktree orchestration (`CreateWorktree`, `RemoveWorktree`, `PruneStaleWorktrees`, `CurrentProject`)|
|`register_test.go`|Filesystem-backed root-identity registration and remove-by-root coverage|
|`service_test.go`|Manager/project unit tests for sort order, not-found behavior, and project-handle guards|

## Public API (`manager.go`)

```go
type ProjectManager interface {
    Register(ctx context.Context, name, repoPath string) (Project, error)
    Update(ctx context.Context, entry config.ProjectEntry) (Project, error)
    List(ctx context.Context) ([]config.ProjectEntry, error)
    Remove(ctx context.Context, root string) error
    Get(ctx context.Context, root string) (Project, error)
    ResolvePath(ctx context.Context, cwd string) (Project, error)
    CurrentProject(ctx context.Context) (Project, error)
}

func NewProjectManager(cfg config.Config, logger iostreams.Logger) ProjectManager
```

`Project` is an interface exposing runtime behavior for a single registered project:

- `Name()`, `RepoPath()`, `Record()`
- `CreateWorktree()`, `AddWorktree()`, `RemoveWorktree()`, `PruneStaleWorktrees()`
- `ListWorktrees()`, `GetWorktree()`

Identity is root-based (`ProjectEntry.Root`), not key-based.

## Internal Construction

- `newRegistry(cfg)` and `newWorktreeService(cfg, logger)` are package-internal and not part of the external API.
- Callers should depend on `ProjectManager` and `Project`, not internal concrete helpers.

## Errors

- `ErrProjectNotFound`
- `ErrProjectExists`
- `ErrWorktreeNotFound`
- `ErrProjectHandleNotInitialized`
- `ErrNotInProjectPath`
- `ErrProjectNotRegistered`

## Registry Facade (`registry.go`)

Internal registry behavior:

- `Projects()` / `List()` return decoded `projects` entries as `[]config.ProjectEntry`
- `Register(displayName, rootDir)` enforces non-idempotent root registration (`ErrProjectExists` on existing root)
- `Update(entry)` mutates existing entry by root identity
- `RemoveByRoot(root)` removes an entry by root identity
- `ProjectByRoot(root)` resolves one entry by root identity
- Reads both legacy map and new list registry shapes; writes list shape
- `Save()` persists via `cfg.Write(config.WriteOptions{Scope: config.ScopeRegistry})`

## Worktree Layout

- Worktree directories are rooted at `cfg.WorktreesSubdir()`.
- Per-project worktree base dir is derived from a stable hash of resolved project root.
- Branch-level directories remain slug-based (via `text.Slugify`).

## Dependencies

Imports: `internal/config`, `internal/git`, `internal/iostreams`, `internal/text`
