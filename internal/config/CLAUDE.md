# Config Package

Configuration loading, validation, project registry, and resolver.

## Key Files

| File | Purpose |
|------|---------|
| `schema.go` | `Config` struct and nested types (`BuildConfig`, `SecurityConfig`, etc.) |
| `loader.go` | `Loader` with functional options: `WithUserDefaults`, `WithProjectRoot`, `WithProjectKey` |
| `settings.go` | `Settings` struct (user-level: `default_image`, `logging`) |
| `settings_loader.go` | `SettingsLoader` — loads/saves `settings.yaml`, merges project-level overrides |
| `registry.go` | `ProjectRegistry`, `ProjectEntry`, `RegistryLoader` — persistent slug-to-path map |
| `resolver.go` | `Resolution`, `Resolver` — resolves working directory to registered project |
| `validator.go` | Config validation rules |
| `defaults.go` | Default config values |

## Registry (`registry.go`)

Persistent project registry at `~/.local/clawker/projects.yaml`.

```go
type ProjectEntry struct {
    Name string `yaml:"name"`
    Root string `yaml:"root"`
}

type ProjectRegistry struct {
    Projects map[string]ProjectEntry `yaml:"projects"`
}
```

**Key functions:**
- `NewRegistryLoader(path)` — creates loader for the registry file
- `Slugify(name)` — converts project name to URL-safe slug
- `UniqueSlug(name, registry)` — generates unique slug with numeric suffix if needed
- `(*RegistryLoader).Register(key, entry)` / `Unregister(key)` — add/remove projects
- `(*ProjectRegistry).Lookup(path)` — find project by longest-prefix path match
- `(*ProjectRegistry).LookupByKey(key)` — find project by slug key

## Resolver (`resolver.go`)

Resolves working directory to a registered project.

```go
type Resolution struct {
    ProjectKey   string        // Registry slug (e.g., "my-app")
    ProjectEntry *ProjectEntry // Name and root path
    WorkDir      string        // Resolved working directory
}
```

- `NewResolver(registry)` — creates resolver from registry
- `(*Resolver).Resolve(workDir)` — returns `*Resolution` (nil if no match)
- `(*Resolution).Found()` — true if resolution matched a project
- `(*Resolution).ProjectRoot()` — returns the project root path

## Loader (`loader.go`)

Loads `clawker.yaml` with functional options:

- `WithUserDefaults(dir)` — merge user defaults from settings directory
- `WithProjectRoot(path)` — explicit project root (skips discovery)
- `WithProjectKey(key)` — inject `Config.Project` from registry

**Load order:** read project config → merge user defaults → inject project key.

`Config.Project` is `yaml:"-"` — never read from YAML, always injected by the loader from the registry.

## Settings (`settings_loader.go`)

- `NewSettingsLoader(opts...)` — creates loader for `settings.yaml`
- `WithProjectSettingsRoot(path)` — enables project-level settings merging
- Settings no longer contain a `Projects` list — that moved to the registry

## Schema Notes

- `Config.Project` has tag `yaml:"-" mapstructure:"-"` — computed, not persisted
- `RalphConfig` is a pointer (`*RalphConfig`) — nil when not configured
- `FirewallConfig.Enable` defaults to `true` via `defaults.go`
