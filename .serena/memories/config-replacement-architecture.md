# Config Replacement: Viper → storage.Store[T]

> **Status:** COMPLETE — All migration tasks finished, Viper fully removed.
> **Last Updated:** 2026-02-22
> **Branch:** `refactor/configapocalypse`
> **Docs:** `.claude/docs/DESIGN.md` §2.4, `.claude/docs/ARCHITECTURE.md` (triad diagram), `internal/storage/CLAUDE.md`, `internal/config/CLAUDE.md`

## Problem

Viper is designed for single-file configs. Clawker needs multi-file layered config with typed accessors, per-field merge strategies, scoped writes, and clean separation. The namespace refactor (prefixing keys with scope) was a workaround — it goes away entirely.

## Stack

- **yaml.v3 only.** No Viper, no koanf, no intermediary config library.
- `internal/storage` provides `Store[T]` — the generic engine.
- `internal/config` and `internal/project` compose it with their schemas.

## Final Architecture

```go
type configImpl struct {
    project  *storage.Store[Project]
    settings *storage.Store[Settings]
}
```

- `Store[Project]` — project config (`clawker.yaml`, `clawker.local.yaml`), walk-up + config dir discovery.
- `Store[Settings]` — user settings (`settings.yaml`), config dir only.
- `Store[ProjectRegistry]` — owned by `internal/project`, data dir with flock.

### Typed Mutation API

```go
cfg.SetProject(func(p *Project) { p.Name = "foo" })
cfg.SetSettings(func(s *Settings) { s.DefaultImage = "node:20" })
cfg.WriteProject()   // persists to disk
cfg.WriteSettings()  // persists to disk
```

### Constructors

| Constructor | Defaults | File Discovery | Use Case |
|------------|----------|----------------|----------|
| `NewConfig()` | Yes (YAML constants) | Yes (walk-up + config dir) | Production |
| `NewBlankConfig()` | Yes | No | Test base (read-only mock) |
| `NewFromString(project, settings)` | **No** | No | Precise test control |
| `ValidateProjectYAML(data)` | N/A | N/A | Strict validation (`config check`) |

## Completed Tasks

- [x] `internal/storage` package implemented (8 files, 28 tests)
- [x] Remove `mapstructure` tags from schema.go
- [x] Create defaults YAML string constants (`defaultProjectYAML`, `defaultSettingsYAML`)
- [x] Export `ResolveProjectRoot()` from storage
- [x] Rewrite `configImpl` and `Config` interface (two-store composition)
- [x] Extract registry to `internal/project` as `Store[ProjectRegistry]`
- [x] Update all consumers (init, image, config check) and regenerate mocks
- [x] Rewrite config tests (25 tests covering constructors, defaults, validation, mutation, persistence)
- [x] Remove Viper, fsnotify, mapstructure dependencies from go.mod
- [x] Delete dead code: `dirty.go`, `load.go`, `write.go`, namespace helpers
- [x] Update documentation: CLAUDE.md files, ARCHITECTURE.md, DESIGN.md
- [x] Full build passes, all tests pass

## Key Decisions

1. **No env var overrides** — `CLAWKER_*` env vars only affect directory resolution, not config values
2. **`Watch()` dropped** — zero callers, dead code
3. **`Logging() map[string]any` dropped** — redundant with typed `LoggingConfig()`
4. **`ValidateProjectYAML()`** — uses `yaml.Decoder` with `KnownFields(true)` for strict validation
5. **`NewFromString` has NO defaults** — mirrors storage's raw `NewFromString` semantics
6. **Registry in project** — `internal/project` owns `Store[ProjectRegistry]`, not config
