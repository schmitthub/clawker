# Config Migration: Command Layer & Project Manager Integration

> **Status:** Phase 1 bulk sweep ~60% complete (15/25 commands done)
> **Branch:** `refactor/configapocalypse`
> **Parent memories:** `configapocalypse-prd`, `configapocalypse-migration-inventory`
> **Tracker:** `configapocalypse-command-migration-tracker` (canonical state)
> **Last updated:** 2026-02-20

## Context

Items #1-7 of the configapocalypse migration are complete (bundler, hostproxy, socketbridge, docker, workspace, containerfs, monitor). The command-layer migration is underway. **15 of ~25 Phase 1 commands are done** (factory, init, kill, and the 14-command bulk sweep). See `configapocalypse-command-migration-tracker` for canonical per-command status.

## Architecture Decisions (Confirmed with User)

### Package Boundaries
- **`config.Config`** — reads, writes, path resolution. Leaf primitives only.
- **`project.Manager`** (in progress by user) — project identity orchestration. Wraps `config.Config` + `git` + `text` for operations needing multiple dependencies: slug resolution, registry CRUD, worktree lifecycle.
- **No `configtest` subpackage** — it's gone forever. Adapt callers to the new system, never revive old patterns.

### Test Stubs (Only Two Patterns)
- `config.NewBlankConfig()` — defaults, caller doesn't care about specific values
- `config.NewFromString(yaml)` — specific values expressed as YAML string
- **Do NOT use `NewFakeConfig` with seeded viper** — user will not approve this; viper is an implementation detail

### Old API Replacements (VERIFIED)
| Old Pattern | New Pattern |
|---|---|
| `config.Provider` on Options structs | `config.Config` via `func() (config.Config, error)` (Factory already has this) |
| `opts.Config().ProjectKey()` | `cfg, err := opts.Config()` + nil-safe `cfg.Project().Project` (NO project.Manager needed) |
| `opts.Config().ProjectFound()` | `cfg.Project() != nil` |
| `opts.Config().ProjectRegistry()` | `cfg.Set("projects.*", ...)` + `cfg.Write()` (no Registry abstraction) |
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

### Factory Changes — RESOLVED
- Factory `Config` field already returns `func() (config.Config, error)` — no further changes needed
- Container commands resolve project identity via nil-safe `cfg.Project().Project` — no project.Manager dependency needed for these
- Worktree commands may still need project.Manager for worktree lifecycle orchestration (TBD)

## Migration Phases

### Phase 0: Config package + project.Manager ✅ DONE
- Config interface built, test stubs built, file mapper built
- project.Manager exists as `project.NewProjectManager(cfg)` (wired in factory)

### Phase 1: Simple Mechanical Sweep (~25 commands, ~80 test files) 🔄 ~60% DONE
**Done (15 commands):** factory, init, kill, pause, unpause, restart, rename, attach, cp, inspect, logs, stats, update, wait, stop, remove, top

**Remaining (~10 commands - see tracker memory for canonical list)**

