# Project Package

Project domain layer for project registration (root identity), path resolution, and worktree orchestration.

## Design Requirement

Project commands (`internal/cmd/project/*`) are the primary user interface for working with `ProjectManager`. The command layer should delegate all domain logic (health checks, status enrichment, worktree state) to the project manager — never perform ad-hoc `os.Stat` or directory checks in command code.

## Boundary

- `internal/config` owns config/path primitives (`Write`, env/path resolution, `ConfigDir`/`DataDir`/`StateDir`) and config-derived projections such as `cfg.EgressRules()`. The dependency runs one way: this package never imports `internal/config`; the caller (CLI factory) resolves the walk-up anchor via `Registry.CurrentRoot` and passes it to config, and config-owned values (e.g. the clawker.yaml `name:` override) are passed in as primitives by the caller.
- `internal/project` owns project CRUD semantics, project-root resolution (`Registry.ResolveRoot`/`CurrentRoot`), worktree lifecycle orchestration, and runtime health enrichment (`ProjectState`, `ProjectStatus`). Project-root resolution reads the registry (`ProjectRegistry` schema), so it is project-domain — not a storage-leaf or config-path concern.
- Callers should consume `ProjectManager`/`Project` interfaces instead of mutating registry data directly.

## Visibility Rules

- Public: interfaces and DTO types (`ProjectManager`, `Project`, `ProjectRecord`, `WorktreeRecord`, `WorktreeState`, `WorktreeStatus`, `ProjectState`, `ProjectStatus`, `PruneStaleResult`, `GitManagerFactory`, error sentinels), plus the `Registry` facade (`NewRegistry`, `WithRegistryDir`, `ResolveRoot`, `CurrentRoot`).
- `Registry` mutation methods (`register`, `update`, `removeByRoot`, worktree ops) are unexported — callers outside this package mutate registry state through `ProjectManager` only.
- Private implementation: `projectManager`, `projectHandle`, `worktreeService`, `flatWorktreeDirProvider`.

## Key Files

| File | Purpose |
|---|---|
| `manager.go` | Public interfaces, constructor, project handle behavior, `ListWorktrees` on both manager and handle |
| `registry.go` | Exported `Registry` facade over `storage.Store[ProjectRegistry]` — `NewRegistry` is the sole constructor of registry storage |
| `resolve.go` | `Registry.ResolveRoot`/`CurrentRoot` project-root resolution + `resolveRootPath` normalization |
| `registry_schema.go` | `ProjectRegistry`/`ProjectEntry`/`WorktreeEntry` schema types + `Fields()` (`storage.Schema`) |
| `worktree_service.go` | Internal git + registry orchestration for worktrees, `flatWorktreeDirProvider` |
| `project_test.go` | Full lifecycle tests: registration, worktree add/remove/prune, duplicate rejection |

## Public API

### Constructors

```go
func NewRegistry(opts ...RegistryOption) (*Registry, error)
func WithRegistryDir(dir string) RegistryOption  // test injection: registry file in dir instead of the data dir
func NewProjectManager(log *logger.Logger, gitFactory GitManagerFactory, nameOverride string, reg *Registry) (ProjectManager, error)
```

`NewRegistry` builds the registry facade through the storage layer (merge + lock; default placement in the resolved data dir). It is constructed once per process by the CLI factory and injected everywhere (`f.ProjectRegistry`); nothing else constructs registry storage. The store snapshots the registry file at construction — sharing the one instance is what keeps resolution and CRUD coherent.

`NewProjectManager` requires a non-nil `*Registry`. `GitManagerFactory` is `func(projectRoot string) (*git.GitManager, error)`. Pass `nil` for production default (`git.NewGitManager`). `nameOverride` is the config-owned project name (the clawker.yaml `name:` value), resolved by the caller and passed as a primitive so this package never imports `internal/config`; the manager applies it in `CurrentProject`.

### `ProjectManager`

```go
type ProjectManager interface {
    Register(ctx context.Context, name string, repoPath string) (Project, error)
    Update(ctx context.Context, entry ProjectEntry) (Project, error)
    List(ctx context.Context) ([]ProjectEntry, error)
    ListProjects(ctx context.Context) ([]ProjectState, error)
    Remove(ctx context.Context, root string) error
    Get(ctx context.Context, root string) (Project, error)
    ResolvePath(ctx context.Context, cwd string) (Project, error)
    CurrentProject(ctx context.Context) (Project, error)
    ListWorktrees(ctx context.Context) ([]WorktreeState, error)
}
```

- `List` sorts by root then name and returns deep-copied entries (cloned `Worktrees` maps) — callers never alias live registry state.
- `ResolvePath` normalizes both sides with the shared `resolveRootPath` helper (`Abs` + `EvalSymlinks`, cleaned-path fallback for nonexistent paths), so symlinked and real paths match interchangeably.
- `CurrentProject` tries the injected registry's `CurrentRoot()`, then falls back to `os.Getwd()` only on the benign `ErrNotInProject`; real registry/storage failures propagate wrapped.
- `ListProjects` returns enriched `ProjectState` views with runtime health checks (directory status, worktree state).
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

type ProjectState struct {
    Name      string
    Root      string
    Worktrees []WorktreeState
    Status    ProjectStatus
    StatusErr error          // non-nil when Status is ProjectInaccessible
}

type ProjectStatus string
// ProjectOK, ProjectMissing, ProjectInaccessible
```

## Error Sentinels

`ErrProjectNotFound`, `ErrProjectExists`, `ErrWorktreeNotFound`, `ErrWorktreeExists`, `ErrProjectHandleNotInitialized`, `ErrNotInProjectPath`, `ErrProjectNotRegistered`, `ErrNotInProject`. Use `errors.Is` at command boundaries.

## Project-Root Resolution (`resolve.go`)

Registry-backed project-root resolution as methods on the injected `Registry` facade. The schema-agnostic `storage` leaf holds no project-domain knowledge; the resolver is injected into walk-up from here.

```go
func (r *Registry) ResolveRoot(cwd string) (string, error)  // deepest registered root that is an ancestor of cwd
func (r *Registry) CurrentRoot() (string, error)            // os.Getwd() → ResolveRoot
```

`ResolveRoot` reads the registry snapshot held by the facade (loaded through the storage layer — the canonical merge/lock path, never a raw file read). cwd is cleaned internally; cwd and each registered root are compared via the shared `resolveRootPath` helper (`Abs` + `EvalSymlinks`, cleaned-path fallback for nonexistent paths — also used by the registry facade, `ResolvePath`, and the worktree service), so a root registered through a symlink matches its real path and vice versa. The returned root is always expressed in cwd's own path form — a string-ancestor of the caller's cwd, valid as a walk-up anchor even when `os.Getwd` reports a logical symlinked path. It returns `ErrNotInProject` when cwd is not within any registered project root — including when a depth-changing symlink leaves the logical cwd with no project ancestor in its own path form (a resolved-space anchor would break config walk-up); `CurrentRoot` propagates the same distinction, and `NewRegistry` surfaces storage failures at construction so they are never mistaken for "not in a project". The CLI factory resolves the root via `f.ProjectRegistry().CurrentRoot()` (and `internal/testenv` via `env.Registry(t)`) and passes it to `config.NewConfig(config.WithProjectRoot(root))` to bound clawker.yaml walk-up at the project root.

## Registry Facade (`registry.go`)

Exported `Registry` facade over `storage.Store[ProjectRegistry]` for registry persistence (`consts.RegistryFile` in the data dir). Mutation ops are unexported (`register`, `update`, `removeByRoot`, `projectByRoot`, `registerWorktree`, `unregisterWorktree`) — consumed in-package by `ProjectManager`.

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

Flat UUID-based naming under the worktrees root (`consts.WorktreesSubdir()`): `<repoName>-<projectName>-<sha256(uuid)[:12]>`. Registry (`ProjectEntry.Worktrees[branch].Path`) is the source of truth for path lookups.

`flatWorktreeDirProvider` implements `git.WorktreeDirProvider`: reuses known path from registry for existing entries, generates UUID-based path for new ones. `newFlatWorktreeDirProvider` ensures the worktrees root exists and returns an error when creation fails — there is no un-ensured fallback path.

### Prune

`PruneStaleWorktrees` marks entries prunable when: directory missing, git metadata missing, or branch deleted. Locked worktrees (`.git/worktrees/<slug>/locked` exists) are skipped even if stale, reported via `PruneStaleResult.Locked`. Supports dry-run and partial-failure reporting via `PruneStaleResult`.

## Test Doubles (`mocks/`)

Import as `projectmocks "github.com/schmitthub/clawker/internal/project/mocks"`.

- `NewMockProjectManager()` — panic-safe `*ProjectManagerMock` with no-op defaults.
- `NewMockProject(name, repoPath)` — `*ProjectMock` with read accessors and no-op mutations.
- `NewTestProjectManager(t, gitFactory)` — real `ProjectManager` backed by `testenv.New(t, testenv.WithProjectManager(gitFactory))`.

## Dependencies

`internal/consts`, `internal/git`, `internal/logger`, `internal/storage`, `internal/text`, `github.com/google/uuid`
