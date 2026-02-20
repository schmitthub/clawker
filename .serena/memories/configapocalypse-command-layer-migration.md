# Config Migration: Command Layer & Project Manager Integration

> **Status:** Planning complete, awaiting user's `project.Manager` interface
> **Branch:** `refactor/configapocalypse`
> **Parent memories:** `configapocalypse-prd`, `configapocalypse-migration-inventory`
> **Last updated:** 2026-02-19

## Context

Items #1-7 of the configapocalypse migration are complete (bundler, hostproxy, socketbridge, docker, workspace, containerfs, monitor). The remaining work is the command-layer migration (~40 production files, ~120 test files) and test infrastructure.

## Architecture Decisions (Confirmed with User)

### Package Boundaries
- **`config.Config`** — reads, writes, path resolution. Leaf primitives only.
- **`project.Manager`** (in progress by user) — project identity orchestration. Wraps `config.Config` + `git` + `text` for operations needing multiple dependencies: slug resolution, registry CRUD, worktree lifecycle.
- **No `configtest` subpackage** — it's gone forever. Adapt callers to the new system, never revive old patterns.

### Test Stubs (Only Two Patterns)
- `config.NewBlankConfig()` — defaults, caller doesn't care about specific values
- `config.NewFromString(yaml)` — specific values expressed as YAML string
- **Do NOT use `NewFakeConfig` with seeded viper** — user will not approve this; viper is an implementation detail

### Old API Replacements
| Old Pattern | New Pattern |
|---|---|
| `config.Provider` on Options structs | `config.Config` via `func() (config.Config, error)` (Factory already has this) |
| `opts.Config().ProjectKey()` | `project.Manager` method (TBD — resolves cwd → registry slug) |
| `opts.Config().ProjectFound()` | `project.Manager.Get()` returning `ErrNotInProject` sentinel |
| `opts.Config().ProjectRegistry()` | `project.Manager.Registry()` (exists on current Service) |
| `opts.Config().ProjectCfg()` | `cfg.Project()` on `config.Config` interface |
| `opts.Config().UserSettings()` | `cfg.Settings()` on `config.Config` interface |
| `cfgGateway.SettingsLoader()` | `cfg.Set(key, val)` + `cfg.Write(WriteOptions{Key: key})` |
| `config.NewConfigForTest(nil, nil)` | `config.NewBlankConfig()` |
| `config.NewConfigForTest(proj, settings)` | `config.NewFromString(yamlString)` |
| `config.ConfigFileName` | literal `"clawker.yaml"` |
| `config.EnsureDir(path)` | `os.MkdirAll(path, 0o755)` |
| `config.DefaultSettings()` | `config.NewBlankConfig().Settings()` |
| `(*config.Config)` type assertion | Use `cfg.Project()` and `cfg.Settings()` interface methods directly |
| `config.ResolveAgentEnv()` | Relocate to `container/shared/` (domain logic, not config) |

### Factory Changes Needed
- New `Project` noun on Factory returning `project.Manager` (follows existing noun pattern: `f.Client`, `f.GitManager`, `f.HostProxy`)
- ~20 container commands need `project.Manager` for `ProjectKey()` resolution
- Worktree commands need `project.Manager` for worktree operations + project identity

## Migration Phases

### Phase 0: User completing `project.Manager` interface ⏳ IN PROGRESS (user is doing this)
- `internal/project/service.go` — Service factory (`Registry()`, `Worktrees()`)
- `internal/project/worktree_service.go` — Worktree lifecycle
- `internal/project/registry.go` — Registry facade (already built)
- Manager interface with project identity (ProjectKey/Get), registry, worktree orchestration
- Integrates `config.Config` + `git.GitManager` + `text.Slugify`

### Phase 1: Simple Mechanical Sweep (~25 commands, ~80 test files) ⬜ TODO
Commands that only need Provider→Config signature change + test stub swap + ProjectKey via Manager:
- All `container/*` (attach, cp, exec, inspect, kill, list, logs, pause, remove, rename, restart, stats, stop, top, unpause, update, wait)
- All `worktree/*` (add, list, prune, remove)
- `loop/reset`, `loop/status`
- `monitor/status`, `monitor/up`, `monitor/down`

Each command needs:
1. Options struct: `Config func() config.Provider` → `Config func() (config.Config, error)`
2. Add `project.Manager` dependency to Options (for commands using ProjectKey)
3. Run function: add `cfg, err := opts.Config()` error handling
4. Replace `cfg.ProjectCfg()` → `cfg.Project()`
5. Replace `cfg.UserSettings()` → `cfg.Settings()`
6. Replace `opts.Config().ProjectKey()` → manager method
7. Tests: swap `config.NewConfigForTest(...)` → `config.NewBlankConfig()` or `config.NewFromString(...)`

### Phase 2: Complex Commands (~10 commands) ⬜ TODO
- **`container/shared`** — relocate `ResolveAgentEnv`, replace `SettingsLoader` with `Set()`+`Write()`
- **`container/create`, `container/run`, `container/start`** — depend on shared
- **`project/init`, `project/register`** — use `project.Manager`/`project.Registry`
- **`init`** (clawker init) — SettingsLoader→Set/Write
- **`image/build`** — remove old Validator
- **`loop/iterate`, `loop/tasks`** — remove type assertion, use `cfg.Project()` directly

### Phase 3: Test Infra + Fawker ⬜ TODO
- `test/harness/` — builders, Slugify, factory
- `test/commands/`, `test/internals/`, `test/agents/`
- `cmd/fawker/`
- `internal/cmd/factory/default_test.go`

### Phase 4: Cleanup ⬜ TODO
- Update all `CLAUDE.md` files (package-level + root)
- Update serena memories
- Verify `go build ./...` and `make test` pass

## Key Files (Current State)
- `internal/config/config.go` — Config interface (already has all needed methods)
- `internal/config/stubs.go` — `NewMockConfig()`, `NewFakeConfig()`, `NewConfigFromString()`
- `internal/config/schema.go` — Schema types + `Registry` interface
- `internal/project/registry.go` — Registry facade wrapping config.Config
- `internal/project/service.go` — Service factory (new)
- `internal/project/worktree_service.go` — Worktree lifecycle (new)
- `internal/project/projecttest/registry.go` — MockRegistrar, RegistrarFunc
- `internal/cmdutil/factory.go` — Factory struct (Config field already migrated)
- `internal/cmd/factory/default.go` — Factory constructor (configFunc already returns new type)

## IMPERATIVE

**Always check with the user before proceeding with the next todo item.** The user is actively building the `project.Manager` interface — do not start command-layer migration until they confirm it's ready. If all work is done, ask the user if they want to delete this memory.
