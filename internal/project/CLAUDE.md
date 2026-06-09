# Project Package

Project domain layer for project registration (root identity), path resolution, and worktree orchestration.

## Design Requirement

Project commands (`internal/cmd/project/*`) are the primary user interface for working with `ProjectManager`. The command layer should delegate all domain logic (health checks, status enrichment, worktree state) to the project manager — never perform ad-hoc `os.Stat` or directory checks in command code.

## Boundary

- `internal/config` owns config/path primitives (`Write`, env/path resolution, `ConfigDir`/`DataDir`/`StateDir`) and config-derived projections such as `cfg.EgressRules()`. The dependency runs one way: this package never imports `internal/config`; config resolves its own walk-up anchor via the standalone `ResolveProjectRoot`, and config-owned values (e.g. the clawker.yaml `name:` override) are passed in as primitives by the caller.
- `internal/project` owns project CRUD semantics, project-root resolution (`ResolveProjectRoot`, `CurrentProjectRoot`, `CurrentProjectIgnoreFile`), worktree lifecycle orchestration, and runtime health enrichment (`ProjectState`, `ProjectStatus`). Project-root resolution reads the registry (`ProjectRegistry` schema), so it is project-domain — not a storage-leaf or config-path concern.
- Callers should consume `ProjectManager`/`Project` interfaces instead of mutating registry data directly.

## Visibility Rules

- Public: interfaces and DTO types (`ProjectManager`, `Project`, `ProjectRecord`, `WorktreeRecord`, `WorktreeState`, `WorktreeStatus`, `ProjectState`, `ProjectStatus`, `PruneStaleResult`, `GitManagerFactory`, error sentinels).
- Public helper: `NewWorktreeDirProvider(log, projectRoot)` — creates a `git.WorktreeDirProvider` for external callers (e.g. `container/shared`).
- Private implementation: `projectManager`, `projectHandle`, `projectRegistry`, `worktreeService`, `flatWorktreeDirProvider`.

## Key Files

| File | Purpose |
|---|---|
| `manager.go` | Public interfaces, constructor, project handle behavior, `ListWorktrees` on both manager and handle |
| `registry.go` | Internal registry facade over `storage.Store[ProjectRegistry]` read/set/write |
| `registry_schema.go` | `ProjectRegistry`/`ProjectEntry`/`WorktreeEntry` schema types + `Fields()` (`storage.Schema`) |
| `worktree_service.go` | Internal git + registry orchestration for worktrees, `flatWorktreeDirProvider`, `NewWorktreeDirProvider` |
| `project_test.go` | Full lifecycle tests: registration, worktree add/remove/prune, duplicate rejection |

## Public API

### Constructor

```go
func NewProjectManager(log *logger.Logger, gitFactory GitManagerFactory, nameOverride string) (ProjectManager, error)
```

`GitManagerFactory` is `func(projectRoot string) (*git.GitManager, error)`. Pass `nil` for production default (`git.NewGitManager`). `nameOverride` is the config-owned project name (the clawker.yaml `name:` value), resolved by the caller and passed as a primitive so this package never imports `internal/config`; the manager applies it in `CurrentProject`.

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

- `List` sorts by root then name. `ResolvePath` normalizes with `Abs` + `EvalSymlinks` fallback.
- `CurrentProject` tries `CurrentProjectRoot()`, then falls back to `os.Getwd()`.
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

Registry-backed project-root resolution. The schema-agnostic `storage` leaf holds no project-domain knowledge; the resolver is injected into walk-up from here.

```go
func ResolveProjectRoot(cwd string) (string, error)   // deepest registered root that is an ancestor of cwd
func CurrentProjectRoot() (string, error)             // os.Getwd() → ResolveProjectRoot
func CurrentProjectIgnoreFile() (string, error)       // <root>/.clawkerignore
```

`ResolveProjectRoot` reads the registry (`ProjectRegistry` schema) through the storage layer (`newRegistryStore()` → `storage.Store[ProjectRegistry]`) — the canonical merge/migrate/lock path, never a raw file read. It returns `ErrNotInProject` when cwd is not within any registered project root, and returns a storage failure wrapped so it is not mistaken for "not in a project"; `CurrentProjectRoot` propagates the same distinction. The CLI factory and `internal/testenv` resolve the root here and pass it to `config.NewConfig(config.WithProjectRoot(root))` to bound clawker.yaml walk-up at the project root.

## Registry Facade (`registry.go`)

Internal facade over `storage.Store[ProjectRegistry]` for registry persistence (`consts.RegistryFile` in the data dir). Ops: `Register`, `Update`, `RemoveByRoot`, `ProjectByRoot`, `registerWorktree`, `unregisterWorktree`.

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

Flat UUID-based naming under the worktrees root (`consts.WorktreesSubdir()`, falling back to `consts.WorktreesPath()`): `<repoName>-<projectName>-<sha256(uuid)[:12]>`. Registry (`ProjectEntry.Worktrees[branch].Path`) is the source of truth for path lookups.

`flatWorktreeDirProvider` implements `git.WorktreeDirProvider`: reuses known path from registry for existing entries, generates UUID-based path for new ones.

### Public Helper

```go
func NewWorktreeDirProvider(log *logger.Logger, projectRoot string) git.WorktreeDirProvider
```

For external callers needing a `WorktreeDirProvider` without the full project service.

### Prune

`PruneStaleWorktrees` marks entries prunable when: directory missing, git metadata missing, or branch deleted. Locked worktrees (`.git/worktrees/<slug>/locked` exists) are skipped even if stale, reported via `PruneStaleResult.Locked`. Supports dry-run and partial-failure reporting via `PruneStaleResult`.

## Test Doubles (`mocks/`)

Import as `projectmocks "github.com/schmitthub/clawker/internal/project/mocks"`.

- `NewMockProjectManager()` — panic-safe `*ProjectManagerMock` with no-op defaults.
- `NewMockProject(name, repoPath)` — `*ProjectMock` with read accessors and no-op mutations.
- `NewTestProjectManager(t, gitFactory)` — real `ProjectManager` backed by `testenv.New(t, testenv.WithProjectManager(gitFactory))`.

## Dependencies

`internal/consts`, `internal/git`, `internal/logger`, `internal/storage`, `internal/text`, `github.com/google/uuid`
