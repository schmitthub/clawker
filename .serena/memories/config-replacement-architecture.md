# Config Replacement: Viper → yaml.v3

> **Status:** Storage engine implemented. Config/project migration next.
> **Last Updated:** 2026-02-22
> **Branch:** `refactor/configapocalypse`
> **Docs:** `.claude/docs/DESIGN.md` §2.4, `.claude/docs/ARCHITECTURE.md` (triad diagram), `internal/storage/CLAUDE.md`

## Problem

Viper is designed for single-file configs. Clawker needs multi-file layered config with typed accessors, per-field merge strategies, scoped writes, and clean separation. The namespace refactor (prefixing keys with scope) was a workaround — it goes away entirely.

## Stack

- **yaml.v3 only.** No Viper, no koanf, no intermediary config library.
- `internal/storage` provides `Store[T]` — the generic engine.
- `internal/config` and `internal/project` compose it with their schemas.

## Current State

### Completed

- [x] Architecture decisions finalized
- [x] `internal/storage` package implemented (8 files, 28 tests passing)
- [x] Node tree architecture (solves `omitempty` problem)
- [x] Discovery: walk-up + explicit paths + dual placement
- [x] Load + migrations (precondition-based, idempotent)
- [x] Merge with provenance tracking and struct tag strategies
- [x] Write: provenance-based routing + explicit filename targeting
- [x] Atomic I/O (temp+fsync+rename) + optional flock
- [x] XDG resolution (CLAWKER_*_DIR > XDG_*_HOME > defaults)
- [x] Documentation: `internal/storage/CLAUDE.md`, ARCHITECTURE.md, DESIGN.md updated
- [x] Serena memories updated

### Next: Config Migration

Migrate `internal/config` to compose `Store[T]`:

```go
// Target architecture
type configImpl struct {
    project  *storage.Store[ConfigFile]
    settings *storage.Store[SettingsFile]
}

func NewConfig() (Config, error) {
    projectStore, _ := storage.NewStore[ConfigFile](
        storage.WithFilenames("clawker.yaml", "clawker.local.yaml"),
        storage.WithDefaults(DefaultConfigYAML),
        storage.WithWalkUp(),
        storage.WithConfigDir(),
        storage.WithMigrations(configMigrations...),
    )
    settingsStore, _ := storage.NewStore[SettingsFile](
        storage.WithFilenames("settings.yaml"),
        storage.WithDefaults(DefaultSettingsYAML),
        storage.WithConfigDir(),
    )
    return &configImpl{project: projectStore, settings: settingsStore}, nil
}
```

### Next: Project Migration

```go
type projectManagerImpl struct {
    registry *storage.Store[Registry]
}

func NewProjectManager() (ProjectManager, error) {
    store, _ := storage.NewStore[Registry](
        storage.WithFilenames("registry.yaml"),
        storage.WithDataDir(),
        storage.WithMigrations(registryMigrations...),
        storage.WithLock(),
    )
    return &projectManagerImpl{registry: store}, nil
}
```

### Callers That Need Updating

| Caller | Current | New |
|--------|---------|-----|
| `internal/project/registry.go` | `cfg.Set("registry.projects", ...)` | `pm.Set(func(r *Registry) { ... })` |
| `internal/cmd/init/init.go` | `cfg.Set("settings.default_image", ...)` | `cfg.Set(func(c *ConfigFile) { ... })` |
| All callers | `cfg.Get("project.build.image")` | `cfg.Project().Build.Image` |
| All callers | `cfg.Get("settings.logging.*")` | `cfg.Settings().Logging.*` |

## TODO

- [ ] Migrate `internal/config` to compose `Store[ConfigFile]` + `Store[SettingsFile]`
- [ ] Migrate `internal/project` to compose `Store[Registry]`
- [ ] Update consumer mock APIs (`config/mocks`, `project/mocks`)
- [ ] Update `internal/config/CLAUDE.md` with new API
- [ ] Remove Viper dependency
- [ ] Remove namespace refactor artifacts
- [ ] Migrate callers to typed accessors
