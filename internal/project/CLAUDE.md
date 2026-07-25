# Project Package

Project domain layer for project registration (root identity), path resolution, and worktree orchestration.

## Design Requirement

Project commands (`internal/cmd/project/*`) are the primary user interface for working with `ProjectManager`. The command layer should delegate all domain logic (health checks, status enrichment, worktree state) to the project manager — never perform ad-hoc `os.Stat` or directory checks in command code.

## Boundary

- `internal/config` owns config/path primitives (`Write`, env/path resolution, `ConfigDir`/`DataDir`/`StateDir`) and config-derived projections such as `cfg.ProjectEgressRules()`. The dependency runs one way: this package never imports `internal/config`; the caller (CLI factory) resolves the walk-up anchor via `Registry.CurrentRoot` and passes it to config, and config-owned values (e.g. the clawker.yaml `name:` override) are passed in as primitives by the caller.
- `internal/project` owns project CRUD semantics, project-root resolution (`Registry.ResolveRoot`/`CurrentRoot`), worktree lifecycle orchestration, and runtime health enrichment (`ProjectState`, `ProjectStatus`). Project-root resolution reads the registry (`ProjectRegistry` schema), so it is project-domain — not a storage-leaf or config-path concern.
- Callers should consume `ProjectManager`/`Project` interfaces instead of mutating registry data directly.

## Visibility Rules

- Public: interfaces and DTO types (`ProjectManager`, `Project`, `ProjectRecord`, `WorktreeRecord`, `WorktreeState`, `WorktreeStatus`, `ProjectState`, `ProjectStatus`, `PruneStaleResult`, `GitManagerFactory`, error sentinels), plus the `Registry` interface (`NewRegistry`, `NewRegistryFromString`, `ResolveRoot`, `CurrentRoot`, and the registry CRUD verbs).
- `Registry`'s read/write verbs (`Projects`, `ProjectByRoot`, `Register`, `Update`, `RemoveByRoot`, worktree ops) are exported domain verbs on the interface — the store-backed facade shape. Callers still route project mutations through `ProjectManager`; the raw verbs exist for the manager and for tests.
- Private implementation: `registryImpl`, `projectManager`, `projectHandle`, `worktreeService`, `flatWorktreeDirProvider`.

## Key Files

| File | Purpose |
|---|---|
| `manager.go` | Public interfaces, constructor, project handle behavior, `ListWorktrees` on both manager and handle |
| `registry.go` | `Registry` interface + `registryImpl` (embeds `*storage.Store[ProjectRegistry]`) — `NewRegistry`/`NewRegistryFromString` are the sole constructors of registry storage |
| `resolve.go` | `ResolveRoot`/`CurrentRoot` project-root resolution on `registryImpl` + `resolveRootPath` normalization |
| `registry_schema.go` | `ProjectRegistry`/`ProjectEntry`/`WorktreeEntry` schema types + `Fields()` (`storage.Schema`) |
| `migrations.go` | `RegistryMigrations()` — additive registry migration list, wired into `NewRegistry` via `storage.WithMigrations` |
| `worktree_service.go` | Internal git + registry orchestration for worktrees, `flatWorktreeDirProvider` |
| `project_test.go` | Full lifecycle tests: registration, worktree add/remove/prune, duplicate rejection |

## Public API

### Constructors

```go
func NewRegistry() (Registry, error)                           // file-backed: registry file in the resolved data dir
func NewRegistryFromString(seed string) (Registry, error)      // in-memory: seed YAML is the only layer, no discovery, no disk
func NewProjectManager(log *logger.Logger, gitFactory GitManagerFactory, nameOverride string, reg Registry) (ProjectManager, error)
```

`NewRegistry` builds the file-backed registry facade through the storage layer (merge + lock; placement in the resolved data dir). The constructor IS the load — discovery, merge and the strict schema decode run inside it, so an unreadable or malformed registry file surfaces as an error from `NewRegistry` rather than as a silent empty registry at first read. It is constructed once per process by the CLI factory and injected everywhere (`f.ProjectRegistry`); nothing else constructs registry storage.

`NewRegistryFromString` is the in-memory seam: the seed YAML is the only layer, parsed through the real schema with no directory, no discovery and no disk (it omits every path option, so it can never read or write a file). Tests inject registry content through the data layer with it — there is no directory-override option.

`NewProjectManager` requires a non-nil `Registry`. `GitManagerFactory` is `func(projectRoot string) (*git.GitManager, error)`. Pass `nil` for production default (`git.NewGitManager`). `nameOverride` is the config-owned project name (the clawker.yaml `name:` value), resolved by the caller and passed as a primitive so this package never imports `internal/config`; the manager applies it in `CurrentProject`.

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

- `List` sorts by root then name. Every registry read decodes a fresh value out of the merged tree, so the returned entries (and their `Worktrees` maps) never alias live store state; mutations reach the store only through the registry's own write path.
- `ResolvePath` normalizes both sides with the shared `resolveRootPath` helper (`Abs` + `EvalSymlinks`, cleaned-path fallback for nonexistent paths), so symlinked and real paths match interchangeably.
- `CurrentProject` tries the injected registry's `CurrentRoot()`, then falls back to `os.Getwd()` only on the benign `ErrNotInProject`; real registry/storage failures propagate wrapped.
- `ListProjects` returns enriched `ProjectState` views with runtime health checks (directory status, worktree state). Worktree enrichment failures do not change `Status` (the root is present and registered) — they land on `StatusErr`, so a degraded worktree list is reportable rather than silently empty.
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
    StatusErr error          // non-nil when Status is ProjectInaccessible, or when worktree enrichment of an OK project failed
}

type ProjectStatus string
// ProjectOK, ProjectMissing, ProjectInaccessible
```

## Error Sentinels

`ErrProjectNotFound`, `ErrProjectExists`, `ErrWorktreeNotFound`, `ErrWorktreeExists`, `ErrProjectHandleNotInitialized`, `ErrNotInProjectPath`, `ErrProjectNotRegistered`, `ErrNotInProject`. Use `errors.Is` at command boundaries.

`ErrProjectNotFound` is the package-wide umbrella for "no registry entry for this project"; `ErrProjectNotRegistered` wraps it and is the single sentinel every registry verb returns for a root-keyed lookup miss (`RemoveByRoot`, `Update`, `RegisterWorktree`, `UnregisterWorktree`, plus `worktreeService.findProjectByRoot`), always wrapped with the verb's own context. `errors.Is(err, ErrProjectNotFound)` therefore matches every not-registered condition in the package, and `errors.Is(err, ErrProjectNotRegistered)` narrows to the registry lookups. `ProjectManager.Get`/`ResolvePath` return the bare umbrella.

Validation failures on registry writes are package-private static errors prefixed `project:` (empty root, display name, worktree branch, worktree path) — they carry no format verbs and are not part of the matchable surface.

## Project-Root Resolution (`resolve.go`)

Registry-backed project-root resolution as the two exported methods on the injected `Registry` interface. The schema-agnostic `storage` leaf holds no project-domain knowledge; the resolver is injected into walk-up from here.

```go
ResolveRoot(cwd string) (string, error)  // deepest registered root that is an ancestor of cwd
CurrentRoot() (string, error)            // os.Getwd() → ResolveRoot
```

`ResolveRoot` reads the registry entries through `storage.Get[[]ProjectEntry]` on the facade's store (loaded through the storage layer — the canonical merge/lock path, never a raw file read). cwd is cleaned internally; cwd and each registered root are compared via the shared `resolveRootPath` helper (`Abs` + `EvalSymlinks`, cleaned-path fallback for nonexistent paths — also used by the registry facade, `ResolvePath`, and the worktree service), so a root registered through a symlink matches its real path and vice versa. The returned root is always expressed in cwd's own path form — a string-ancestor of the caller's cwd, valid as a walk-up anchor even when `os.Getwd` reports a logical symlinked path. It returns `ErrNotInProject` when cwd is not within any registered project root — including when a depth-changing symlink leaves the logical cwd with no project ancestor in its own path form (a resolved-space anchor would break config walk-up); `CurrentRoot` propagates the same distinction, and `NewRegistry` surfaces storage failures at construction so they are never mistaken for "not in a project". The CLI factory resolves the root via `f.ProjectRegistry().CurrentRoot()` (and `internal/testenv` via `env.Registry(t)`) and passes it to `config.NewConfig(config.WithProjectRoot(root))` to bound clawker.yaml walk-up at the project root.

## Registry (`registry.go`)

The store-backed domain facade for registry persistence (`consts.RegistryFile` in the data dir), built to `.claude/rules/store-backed-package.md`: the exported `Registry` **interface**, the unexported `registryImpl` embedding `*storage.Store[ProjectRegistry]`, and the `NewRegistry`/`NewRegistryFromString` constructor pair returning the interface.

```go
type Registry interface {
    ResolveRoot(cwd string) (string, error)
    CurrentRoot() (string, error)

    Projects() ([]ProjectEntry, error)                            // absent key = empty registry
    ProjectByRoot(root string) (ProjectEntry, bool, error)
    Register(displayName, rootDir string) (ProjectEntry, error)
    Update(entry ProjectEntry) (ProjectEntry, error)
    RemoveByRoot(root string) error
    RegisterWorktree(projectRoot, branch, path string) error
    UnregisterWorktree(projectRoot, branch string) error
}
```

### Write Front Door

Every write verb validates before it persists, so an unusable entry never reaches the file:

| Verb | Guards | Normalization |
|---|---|---|
| `Register` | empty `rootDir`, empty `displayName`, duplicate root (`ErrProjectExists`) | root stored `filepath.Abs` |
| `Update` | empty `Root`, unknown root (`ErrProjectNotRegistered`) | root stored `filepath.Abs`; each `Worktrees` value's `Branch` set from its map key |
| `RegisterWorktree` | empty `projectRoot`/`branch`/`path`, unknown root | — |
| `UnregisterWorktree` | empty `projectRoot`/`branch`, unknown root | — |
| `RemoveByRoot` | unknown root | — |

`Update`'s `Worktrees` field discriminates nil from empty: a **nil** map carries the recorded worktrees forward (the caller is updating other fields); a **non-nil empty** map wipes them. The branch normalization means `Worktrees["main"] = {Branch: "feature"}` cannot persist — the key is the worktree's identity and the value mirrors it. The caller's map is never mutated (normalization copies).

### Migrations

`RegistryMigrations()` (`migrations.go`) is the additive migration list wired into `NewRegistry` through `storage.WithMigrations`. It is currently empty; append a precondition-guarded, idempotent migration when the registry schema evolves, never edit a shipped one. `NewRegistryFromString` deliberately omits migrations — a seed is not migrated. A `TestRegistryMigrations` legacy-chain table is added the moment the first migration exists (one row per historical on-disk shape).

The verbs are exported domain methods, so `Registry` is moq-mockable like every other store-backed facade (`//go:generate moq` → `mocks/registry_mock.go`). `registryImpl` embeds the store — the engine verbs stay reachable in-package as the escape hatch — but the impl type is unexported and only ever handed out as `Registry`, so the interface (not the struct) gates what escapes. Project mutations from the command layer still go through `ProjectManager`; the registry verbs are the manager's own surface.

Every write verb owns the single schema key `projects` and both stages **and** persists in one call — `store.Set([]string{"projects"}, entries)` then `store.Write()`, each wrapped `project: …` — so no caller ever holds a staged-but-unwritten registry and there is no separate `save()` step. Reads go through `storage.Get[[]ProjectEntry](store, "projects")` with `ErrKeyNotFound` folded to the empty registry. There is no whole-struct read.

## Worktree Service (`worktree_service.go`)

### Root Resolution

All worktree service methods accept `projectRoot` from the calling `projectHandle` (which gets it from `record.Root`). This ensures deterministic path matching against the registry — the service never re-resolves the root from the filesystem via `os.Getwd()`. The `findProjectByRoot` helper resolves symlinks on both the input and registry entries for robust matching (e.g., macOS `/var` → `/private/var`).

### Internal API

```go
CreateWorktree(_ context.Context, projectRoot, branch, base string, noTrack bool) (string, error)
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
- `RegistryMock` — moq-generated double for the `Registry` interface. Prefer a real registry when the test exercises registry behavior: `testenv.Env.Registry(t)` (file-backed over the isolated data dir) or `project.NewRegistryFromString(seed)` (in-memory, no disk).

## Dependencies

`internal/consts`, `internal/git`, `internal/logger`, `internal/storage`, `internal/text`, `github.com/google/uuid`
