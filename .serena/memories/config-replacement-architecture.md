# Config Replacement: Viper → yaml.v3 (Architecture Complete)

> **Status:** Architecture finalized. Next: DESIGN.md, then prototype.
> **Last Updated:** 2026-02-21 23:30
> **ARCHITECTURE.md:** Already updated with all decisions below.

## Problem
Viper is designed for single-file configs. Clawker needs multi-file layered config with typed accessors, per-field merge strategies, scoped writes, and clean separation of config from mutable state. The namespace refactor (prefixing keys with scope) was a Viper workaround, not a real design need. It goes away entirely.

## Stack
- **yaml.v3 only.** No Viper, no koanf, no intermediary config library.
- No Go config library handles writes, locking, or commented YAML — those are always application-level.

## File Layout (walk-up + XDG hybrid)

Mirrors Claude Code's pattern: `~/.claude/` for config walk-up, `~/.local/share/claude/` and `~/.local/state/claude/` for data/state.

```
~/.clawker/                              ← config (walk-up root, NOT XDG)
  config.yaml                            ← ConfigFile (global project defaults)
  settings.yaml                          ← SettingsFile (host infrastructure)

.clawker/                                ← project tier (walk-up match)
  config.yaml                            ← ConfigFile (per-project, committed)
  local.yaml                             ← ConfigFile (personal overrides, gitignored)

~/.local/share/clawker/                  ← data (XDG compliant)
  registry.yaml                          ← project/worktree state (owned by internal/project)

~/.local/state/clawker/                  ← state (XDG compliant)
  logs/
  cache/
```

- **Walk-up pattern:** Loader walks from CWD to HOME, collects all `.clawker/config.yaml` files, merges global-first/closest-wins. Same pattern as `.gitignore`, `.editorconfig`, Claude Code's `.claude/`.
- **Any config file can become tiered** by placing a copy at a lower level. If per-project settings are ever needed, drop `settings.yaml` into `.clawker/` — zero architecture changes.
- **`CLAWKER_CONFIG_DIR`** overrides `~/.clawker/` (single env var). `XDG_DATA_HOME` / `XDG_STATE_HOME` honored for data/state.
- **`.clawkerignore`** lives at project root (not inside `.clawker/`). No walk-up — follows `.gitignore`/`.dockerignore` convention. Project root anchor from `internal/project`.

## Two Independent Schemas

Settings and config are **never collapsed** — different concerns, different evolution, different write patterns.

```go
// Host infrastructure — ~/.clawker/settings.yaml only
type SettingsFile struct {
    Logging    LoggingConfig    `yaml:"logging"`
    HostProxy  HostProxyConfig  `yaml:"host_proxy"`
    Monitoring MonitoringConfig `yaml:"monitoring"`
}

// Project defaults — tiered via walk-up (global → project → local)
type ConfigFile struct {
    Build     BuildConfig     `yaml:"build"`
    Workspace WorkspaceConfig `yaml:"workspace"`
    Security  SecurityConfig  `yaml:"security"`
    Agent     AgentConfig     `yaml:"agent"`
    Loop      LoopConfig      `yaml:"loop"`
}
```

- **No version field in either struct.** Struct is source of truth. Migrations check data shape, not version numbers.
- **No `project` field in config.** Project identity lives in registry only (owned by `internal/project`).
- **No `ProjectDefaults` shared embed.** The two schemas are fully independent — no coupling.

## Config Interface (single access point, namespaced)

```go
type Config interface {
    Settings() *SettingsFile    // → ~/.clawker/settings.yaml
    Project() *ConfigFile       // → merged walk-up result

    // Path helpers, constants, labels (~40 methods)
    ConfigDir() string
    Domain() string
    LabelDomain() string
    // ...

    // Scoped writes
    WriteSettings(partial SettingsFile) error
    WriteProjectConfig(partial ConfigFile) error
    WriteLocalConfig(partial ConfigFile) error
}
```

- `cfg.Project().Build().Image` — from merged config walk-up
- `cfg.Settings().Logging().FileEnabled` — from settings.yaml
- `cfg.ConfigDir()` — path helpers at top level
- No collision risk: if both schemas grow a `Build` section, `cfg.Settings().Build()` vs `cfg.Project().Build()`
- No generic `Get(key)` / `Set(key, val)`. Typed accessors only.
- One factory closure (`f.Config()`), one interface.

## Merge Strategy

**Walk-up merge for ConfigFile:**
```
hardcoded defaults → ~/.clawker/config.yaml → .clawker/config.yaml → .clawker/local.yaml → env vars
```

**Per-field merge for arrays** via struct tags:
- `merge:"union"` (additive, deduped) — for: `from_env`, `packages`, `includes`, `firewall.sources`
- `merge:"overwrite"` (project wins entirely) — for: `copy`, `root_run`, `user_run`, `inject.*`
- Scalars: always last-wins, no tag needed

**Untagged slices default to overwrite at runtime** (safe fallback). A reflection test in CI asserts every `[]T` field has an explicit `merge` tag — missing tag = test failure. Go can't enforce struct tags at compile time; test + CI gate is the standard approach.

**Env var overrides:** Hardcoded shortlist only. No generic `CLAWKER_*` → field mapping. Explicitly supported env vars, added as needed.

**SettingsFile:** Loaded separately, not merged with ConfigFile.

## Two-Phase Load

1. **Phase 1 (lenient):** YAML → `map[string]any` → run precondition migrations → re-save if anything changed
2. **Phase 2 (typed):** Map → typed struct. Only known keys read, unknowns silently ignored. Struct defaults fill missing keys.

**Unknown fields silently ignored** — matches Claude Code and Serena. No `KnownFields(true)`. Typos are user's problem.

## Migrations

**Precondition-based idempotent functions** (Claude Code + Serena pattern):
- Each migration checks if old data shape exists in the raw map
- If found: transform → re-save → done
- If not found: skip (already current or never applied)
- No version field, no migration chain, no ordering constraints
- Runs during Phase 1 of load (on the raw map, before struct validation)
- Idempotent by construction — safe for concurrent processes

## Writes

| Scope | Writes | By Whom | When |
|-------|--------|---------|------|
| `~/.clawker/settings.yaml` | Init scaffolding | `clawker init` | One-time |
| `~/.clawker/config.yaml` | Init scaffolding (e.g. `build.image`) | `clawker init` | One-time |
| `.clawker/config.yaml` | User edits by hand | User's text editor | Anytime |
| `.clawker/local.yaml` | User edits by hand | User's text editor | Anytime |
| `registry.yaml` | Programmatic CRUD | `internal/project` | Runtime |

Write pattern (Claude Code `T_()` style): read current → deep merge partial update → atomic write.
Settings files do NOT need locking. Registry uses flock.

## Package Ownership

| Package | Owns | Imports |
|---------|------|---------|
| **`internal/storage`** | Atomic write (temp+rename), flock, YAML read/write, reflection-based merge | Leaf — zero internal imports |
| **`internal/config`** | `settings.yaml` + `config.yaml` walk-up. One `Config` interface. Two schemas, two concerns. | `storage`, `logger` |
| **`internal/project`** | `registry.yaml`. Project domain: registration, resolution, worktree lifecycle. | `storage`, `config`, `iostreams`, `logger` |

- `internal/project` is a middle-tier domain package ("if I want project operations, I go here")
- Registry is `internal/project`'s persistence layer, not its identity — don't rename to `internal/registry`

## Claude Code Reference (informed our decisions)

Reverse-engineered from `/Users/andrew/.local/bin/claude` binary.

**Config architecture:** `~/.claude.json` (state DB, locked RMW) + `settings.json` files (pure preferences, same schema all tiers, no locking). Two completely separate schemas and logic paths.

**Settings write (`T_` function):** Read current → deep merge partial (undefined=delete, arrays=replace within scope) → mark internal write (suppress file watcher 5s) → atomic write.

**Migrations:** Three ad-hoc functions (`K9D`, `I9D`, `f9D`). Each: check precondition → write to target scope → delete from source → telemetry. Run every startup via Commander `preAction` hook. Idempotent, no version chain.

**Array merge:** Read-side across scopes = union with dedup. Write-side within scope = arrays replace.

**Atomic write (`Zz`):** Resolve symlinks → temp file `{target}.tmp.{pid}.{timestamp}` → writeFileSync → preserve permissions → renameSync → fallback to direct write → cleanup on error.

**File locking:** Only `~/.claude.json` (global state). Settings files unlocked.

## Serena Reference
Serena also uses precondition-based idempotent migrations. Unknown fields silently ignored. Both tools independently arrived at the same patterns.

## Callers That Need Updating

| Caller | Current | New |
|--------|---------|-----|
| `internal/project/registry.go` | `cfg.Set("registry.projects", ...)` + `cfg.Write(ScopeRegistry)` | `internal/project` owns registry directly via `internal/storage` |
| `internal/cmd/init/init.go` | `cfg.Set("settings.default_image", ...)` + `cfg.Write()` | Scaffolds `~/.clawker/config.yaml` with `build.image` |
| `internal/cmd/container/shared/image.go` | `persistDefaultImageSetting` | ELIMINATED — fallback handled by walk-up merge |
| All callers | `cfg.Get("project.build.image")` | `cfg.Project().Build().Image` |
| All callers | `cfg.Get("settings.logging.*")` | `cfg.Settings().Logging().*` |

## TODO

- [x] Research: koanf, Claude Code binary, Serena patterns
- [x] Architecture decisions (all finalized, zero open items)
- [x] Update ARCHITECTURE.md
- [ ] **Update DESIGN.md** with detailed design (interfaces, structs, load/write flows, migration patterns)
- [ ] Update `internal/config/CLAUDE.md` with new package API
- [ ] Phase 2: Prototype implementation
- [ ] Phase 3: Migrate callers, remove Viper, remove namespace refactor, update docs

---

**IMPERATIVE: Always check with the user before proceeding to the next TODO item. If all work is done, ask the user if they want to delete this memory.**
