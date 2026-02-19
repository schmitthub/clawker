# Config Docs Sync — Post-Interface Expansion

> **Status:** Complete
> **Branch:** `refactor/configapocalypse`
> **Parent:** `configapocalypse-prd`, `configapocalypse-migration-inventory`
> **Last updated:** 2026-02-19

## End Goal

Update ALL claude docs, migration files, and memories to reflect the expanded Config interface which now includes concurrency-safe low-level `Get`, `Set`, `Write`, and `Watch` methods — plus an upcoming internal file mapper that routes writes to the correct underlying file (settings.yaml, clawker.yaml, projects.yaml) transparently.

## Context: What Changed in Config Interface

The `Config` interface in `internal/config/config.go` was expanded from read-only schema accessors to include low-level mutation with ownership-aware file routing:

```go
type Config interface {
    // Schema accessors
    Logging() map[string]any
    Project() *Project
    Settings() Settings
    LoggingConfig() LoggingConfig
    MonitoringConfig() MonitoringConfig

    // Low-level concurrency-safe primitives
    Get(key string) (any, error)             // RLock — returns KeyNotFoundError if unset
    Set(key string, value any) error         // Lock — validates ownership, updates viper, marks dirty
    Write(opts WriteOptions) error           // Lock — ownership-aware scoped persistence
    Watch(onChange func(fsnotify.Event)) error // Lock — watches config file for changes

    // Constants (private, exposed via methods)
    Domain() string
    LabelDomain() string
    ConfigDirEnvVar() string
    MonitorSubdir() string
    BuildSubdir() string
    DockerfilesSubdir() string
    ClawkerNetwork() string
    LogsSubdir() string
    BridgesSubdir() string
    ShareSubdir() string
    RequiredFirewallDomains() []string
    GetProjectRoot() (string, error)
}

type ConfigScope string
const ScopeSettings ConfigScope = "settings"
const ScopeProject  ConfigScope = "project"
const ScopeRegistry ConfigScope = "registry"

type WriteOptions struct {
    Path  string       // explicit file override
    Safe  bool         // create-only mode
    Scope ConfigScope  // constrain to scope
    Key   string       // single key persistence
}
```

Implementation details:
- `configImpl` has `sync.RWMutex` field (`mu`) — ALL methods are thread-safe
- `keyOwnership` map routes root keys to scopes (e.g., `"default_image"` → `ScopeSettings` → `settings.yaml`)
- `Set()` validates key ownership, updates viper in-memory, marks dirty node tree
- `Write()` dispatches: Key → single dirty key, Scope → dirty roots for scope, neither → all scopes to owning files
- `dirtyNode` tree for structural path tracking (node-based, not flat set)
- `resolveTargetPath()` maps scope to file (settings.yaml, projects.yaml, project clawker.yaml)
- `Get` returns `KeyNotFoundError` when key is not set via `v.IsSet()`
- `Watch` wraps viper's `WatchConfig()` + `OnConfigChange()`

### File mapper: IMPLEMENTED
The ownership-aware file mapper is built. `Set`+`Write` route changes to the correct underlying file (settings.yaml, clawker.yaml, projects.yaml) — callers see only the unified `Config` interface and never reference specific files or paths.

## What Was Done

### Migration inventory completed [DONE]
- Full per-file inventory of every caller using old config symbols: `configapocalypse-migration-inventory` memory
- 15 gaps identified, 4-phase migration plan proposed
- ~110-120 files need changes across the codebase

### Docs update started but NOT committed [IN PROGRESS]
- Identified all docs needing updates (list below)
- First edit attempted on `internal/config/CLAUDE.md` but was interrupted before any edits landed

## TODO: Docs to Update

### Step 1: `internal/config/CLAUDE.md` [DONE]
- [x] Architecture section: RWMutex concurrency note, file mapper documented
- [x] Config Interface section: full interface with Get/Set/Write/Watch + WriteOptions + ConfigScope
- [x] Files table: updated config.go description
- [x] Usage Patterns: new patterns for Get/Set/Write (settings write-back, key lookup, scoped writes)
- [x] Pattern 4 (SettingsLoader): REWRITTEN — now shows Set+Write pattern
- [x] Low-level Mutation API section: ownership map, Write dispatch, dirty tracking
- [x] Testing Guide: all three stubs documented

### Step 2: `.claude/docs/ARCHITECTURE.md` [DONE]
- [x] internal/config section: write model, validation, testing documented
- [x] Package table: config description updated

### Step 3: `.claude/docs/DESIGN.md` [DONE]
- [x] Config persistence model paragraph added (line ~52)
- [x] Note: `config *config.Config` in Client struct example is conceptual pseudocode, not actual type

### Step 4: `CLAUDE.md` (root) [DONE]
- [x] Key Concepts table: config.Config entry updated with Set/Write/Watch, ownership routing, thread safety

### Step 5: `.claude/docs/TESTING-REFERENCE.md` [DONE]
- [x] Config testing section: all three stubs with usage examples
- [x] Factory wiring pattern with NewMockConfig()

### Step 6: Serena memories [DONE]
- [x] `configapocalypse-migration-inventory`: Gap 4 marked RESOLVED, Gap 12 marked RESOLVED, interface signature corrected
- [x] `configapocalypse-prd`: Interface expansion noted, mutation API documented, design decisions updated
- [x] `configapocalypse-docs-sync`: This memory — status updated to Complete

### Step 7: Verify no stale references [DONE]
- [x] "SettingsLoader is not yet rebuilt" in config/CLAUDE.md Pattern 4 — FIXED
- [x] "write operations need a new implementation" — FIXED
- [x] No other stale references found in .claude/ docs

## Completion Notes

The internal file mapper is implemented. `WriteOptions` gained `Scope ConfigScope` and `Key string` fields. The `keyOwnership` map routes root keys to scopes, and `resolveTargetPath()` maps scopes to files. Callers use `Set(key, value)` + `Write(WriteOptions{Key: key})` without awareness of which file gets written.

All docs and memories are now synchronized with the actual implementation. This memory can be deleted once the configapocalypse migration work is fully complete.
