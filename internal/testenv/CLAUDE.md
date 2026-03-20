# internal/testenv — Unified Test Environment

## Purpose

Provides isolated, progressively-configured test environments for any test that needs XDG directory isolation. Eliminates duplicated directory setup across `config/mocks`, `project/mocks`, and `test/e2e/harness`.

## Package: `testenv`

Import: `"github.com/schmitthub/clawker/internal/testenv"`

## Types

| Type | Purpose |
|------|---------|
| `IsolatedDirs` | Holds resolved paths: `Base`, `Config`, `Data`, `State`, `Cache` |
| `Env` | Unified environment with dirs + optional config/project manager |
| `Option` | Functional option: `func(t *testing.T, e *Env)` |
| `ConfigFile` | Enum identifying a config file type and its canonical location |

## Constructor

```go
func New(t *testing.T, opts ...Option) *Env
```

1. Creates temp directory (symlink-resolved for macOS `/var` → `/private/var`)
2. Creates four XDG subdirs: `config/`, `data/`, `state/`, `cache/`
3. Sets `CLAWKER_CONFIG_DIR`, `CLAWKER_DATA_DIR`, `CLAWKER_STATE_DIR`, `CLAWKER_CACHE_DIR` (restored on cleanup via `t.Setenv`)
4. Applies options in order

## Options

| Option | Effect | Accessor |
|--------|--------|----------|
| *(none)* | Dirs only | `env.Dirs` |
| `WithConfig()` | Creates real `config.Config` backed by temp dirs | `env.Config()` |
| `WithProjectManager(gitFactory)` | Creates real `project.ProjectManager` (implies `WithConfig`) | `env.ProjectManager()` |

Pass `nil` for `gitFactory` if worktree operations are not needed.

## ConfigFile Constants

| Constant | Target |
|----------|--------|
| `ProjectConfig` | `.clawker.yaml` in caller-provided project dir |
| `ProjectConfigLocal` | `.clawker.local.yaml` in caller-provided project dir |
| `Settings` | `settings.yaml` in config dir |
| `EgressRules` | `egress-rules.yaml` in state dir |
| `ProjectRegistry` | `projects.yaml` in data dir |

## Accessors

- `env.Config()` — panics if `WithConfig()` was not applied
- `env.ProjectManager()` — panics if `WithProjectManager()` was not applied
- `env.Dirs` — always available (struct field, not method)
- `env.WriteYAML(t, file, dir, content)` — writes YAML content to the canonical location for the given `ConfigFile`. For project configs (`ProjectConfig`, `ProjectConfigLocal`), `dir` is the project directory; for others, `dir` is ignored and the appropriate XDG directory is used

## Usage Patterns

```go
// Storage / resolver tests — dirs only
env := testenv.New(t)
// env.Dirs.Config, env.Dirs.Data, etc.

// Config mutation tests
env := testenv.New(t, testenv.WithConfig())
cfg := env.Config()
cfg.SetProject(func(p *config.Project) { p.Build.Image = "alpine" })
cfg.WriteProject()

// Project registration round-trips
env := testenv.New(t, testenv.WithProjectManager(nil))
pm := env.ProjectManager()
pm.Register(ctx, "myapp", "/path/to/repo")

// With git manager for worktree tests
env := testenv.New(t, testenv.WithProjectManager(gittest.NewInMemoryFactory()))
```

## Delegation Pattern

Higher-level helpers delegate to testenv rather than duplicating dir setup:

- `config/mocks.NewIsolatedTestConfig(t)` → `testenv.New(t, testenv.WithConfig())`
- `project/mocks.NewTestProjectManager(t, gf)` → `testenv.New(t, testenv.WithProjectManager(gf))`
- `test/e2e/harness.NewIsolatedFS()` → `testenv.New(h.T)` + project dir + chdir

## Import Position

Leaf-adjacent foundation package. Imports: `config`, `logger`, `project` (all foundation/middle). Imported by: `config/mocks`, `project/mocks`, `test/e2e/harness`, any test needing isolated XDG dirs.

## Key Design Decisions

1. **Progressive options** — callers request only what they need; no over-allocation
2. **Panic on unconfigured access** — `Config()` and `ProjectManager()` panic with clear messages if the option was not applied, catching test setup bugs immediately
3. **Symlink resolution** — `filepath.EvalSymlinks` on temp dir base prevents macOS `/var` vs `/private/var` mismatches
4. **t.Setenv for env vars** — automatically restored on test cleanup; no manual unset needed
5. **Idempotent WithConfig** — `WithProjectManager` calls `WithConfig` internally; duplicate calls are no-ops
