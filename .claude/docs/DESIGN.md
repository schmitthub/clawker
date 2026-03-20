# Clawker Design Document

Clawker is a Go CLI tool that wraps the Claude Code agent in secure, reproducible Docker containers.

## Related Docs

- `.claude/docs/ARCHITECTURE.md` — package layering and dependency boundaries.
- `internal/storage/CLAUDE.md` — storage package API, node tree architecture, merge/write internals.
- `internal/config/CLAUDE.md` — config package contracts, persistence model, and test helpers.

## 1. Philosophy: "The Padded Cell"

### Core Principle

Standard Docker gives users full control—which is dangerous for beginners and risky when running autonomous AI agents. Clawker creates a "padded cell": users interact with Docker-like commands, but operations are isolated to clawker-managed resources only.

### Threat Model

Clawker protects everything **outside** the container from what happens **inside**:

- Host filesystem protected from container writes (bind mounts controlled)
- Host network protected via firewall (outbound controlled, inbound open)
- Other Docker resources protected via label-based isolation
- The container itself is disposable—a new one can always be created

We do **not** inherit Docker's threat model. If Docker allows catastrophic commands, clawker permits them—but only against clawker-managed resources.

## 2. Core Concepts

### 2.1 Project

A clawker project is defined by a `.clawker/` directory containing configuration files. Every clawker command requires project context.

**Project Resolution**: `config.NewConfig()` performs a walk-up merge of configuration files (see §2.4) and resolves the current project from the registry via `GetProjectRoot()`. The `Config` interface exposes typed accessors — all file paths and constants are private to the package.

**Project identity** is decoupled from configuration:
- `internal/config` — configuration file I/O, walk-up loading, path helpers
- `internal/project` — project registration, CRUD, worktree lifecycle, `registry.yaml` I/O

See **§2.4 Configuration System** for the full design: file layout, schemas, load/merge/write flows, and migrations.

### 2.2 Agent

An agent is a named container instance. Agents have a many-to-many relationship with projects and images:

- One project can have multiple agents
- One agent belongs to one project
- Multiple agents can share an image
- One agent uses one image

**Naming Convention**: `clawker.<project>.<agent>`

- Example: `clawker.myapp.dev`, `clawker.backend.worker`

If no agent name provided, one is generated randomly (Docker-style adjective-noun).

### 2.3 Resource Identification

Three mechanisms identify clawker-managed resources:

| Mechanism | Purpose | Authority |
|-----------|---------|-----------|
| **Labels** | Filtering, ownership verification | Authoritative source of truth |
| **Naming** | Human readability, programmatic filtering when needed | Secondary |
| **Network** | Container-to-container communication, security isolation | Functional |

**Key Labels**:

```
dev.clawker.managed=true
dev.clawker.project=<project-name>   # omitted when project is empty
dev.clawker.agent=<agent-name>
```

**Naming Segments**:
- 3-segment: `clawker.project.agent` (e.g., `clawker.myapp.dev`) — when project is set
- 2-segment: `clawker.agent` (e.g., `clawker.dev`) — when project is empty (orphan project)

**Strict Ownership**: Clawker refuses to operate on resources without `dev.clawker.managed=true` label, even if they have the `clawker.` name prefix.

### Project Registry Lifecycle

1. **Register**: `clawker project init` or `clawker project register` adds a slug→path entry to `cfg.ProjectRegistryFileName()`
2. **Lookup**: `Factory.Config()` returns a `config.Config` — the single interface all callers receive. Project resolution uses registry + `os.Getwd()` internally
3. **Orphan projects**: If no project is resolved, resources get 2-segment names and omit the project label

### 2.4 Configuration System

Replaces Viper with `yaml.v3` only. No Go config library handles writes, locking, or commented YAML — those are always application-level. The Viper namespace refactor (prefixing keys with scope) was a workaround, not a real design need — it is eliminated entirely.

#### File Layout (Full XDG)

All user-level directories follow the XDG Base Directory Specification. Walk-up never reaches HOME — it is bounded at the registered project root.

```
~/.config/clawker/                       ← config (XDG_CONFIG_HOME)
  clawker.yaml                           ← ConfigFile (global project defaults)
  clawker.local.yaml                     ← ConfigFile (global personal overrides)
  settings.yaml                          ← SettingsFile (host infrastructure)

<any-walk-up-level>/                     ← dual placement at every level (dir wins)
  .clawker.yaml                          ← flat form (committed)
  .clawker.local.yaml                    ← flat form (personal, gitignored)
  .clawker/                              ← OR directory form (wins if .clawker/ exists)
    clawker.yaml                         ← dir form (committed)
    clawker.local.yaml                   ← dir form (personal, gitignored)

~/.local/share/clawker/                  ← data (XDG_DATA_HOME)
  registry.yaml                          ← project/worktree state (owned by internal/project)

~/.local/state/clawker/                  ← state (XDG_STATE_HOME)
  logs/
  cache/
```

**Filename-driven discovery:** The store takes an ordered list of filenames on construction (e.g., `"clawker.yaml"`, `"clawker.local.yaml"`). All filenames share the same schema. At each walk-up level, for each filename:
1. If `.clawker/` dir exists → check `.clawker/{filename}`
2. Otherwise → check `.{filename}` (flat dotfile)

Dir form and flat form are mutually exclusive per level. Both `.yaml` and `.yml` extensions accepted. First filename takes merge precedence at the same level.

**Walk-up pattern:** Bounded from CWD to registered project root. Home-level configs (`~/.config/clawker/`) are added via `WithConfig()` convenience option — never discovered via walk-up. Walk-up requires CWD to be within a registered project; if not, only home/explicit configs are loaded (sentinel error `ErrNotInProject` lets callers decide how to handle it).

**Env overrides (precedence order):**
1. `CLAWKER_CONFIG_DIR` / `CLAWKER_DATA_DIR` / `CLAWKER_STATE_DIR` — clawker-specific, highest precedence
2. `XDG_CONFIG_HOME` / `XDG_DATA_HOME` / `XDG_STATE_HOME` — standard XDG
3. Defaults: `~/.config/clawker/`, `~/.local/share/clawker/`, `~/.local/state/clawker/`

**`.clawkerignore`:** Lives at project root (not inside `.clawker/`). No walk-up — follows `.gitignore`/`.dockerignore` convention. Project root anchor from `internal/project`.

#### Two Independent Schemas

Settings and config are **never collapsed** — different concerns, different evolution, different write patterns.

```go
// Host infrastructure — ~/.config/clawker/settings.yaml only
type Settings struct {
    DefaultImage string           `yaml:"default_image,omitempty"`
    Logging      LoggingConfig    `yaml:"logging"`
    HostProxy    HostProxyConfig  `yaml:"host_proxy"`
    Monitoring   MonitoringConfig `yaml:"monitoring"`
}

// Project defaults — tiered via walk-up (global → project → local)
type Project struct {
    Version   string          `yaml:"version"`
    Name      string          `yaml:"name,omitempty"`
    Build     BuildConfig     `yaml:"build"`
    Workspace WorkspaceConfig `yaml:"workspace"`
    Security  SecurityConfig  `yaml:"security"`
    Agent     AgentConfig     `yaml:"agent"`
    Loop      *LoopConfig     `yaml:"loop,omitempty"`
}
```

**Design decisions:**
- **No version field** in either struct. Struct is source of truth. Migrations check data shape, not version numbers.
- **No `project` field** in config. Project identity lives in registry only (owned by `internal/project`).
- **No `ProjectDefaults` shared embed.** The two schemas are fully independent — no coupling.

#### Config Interface

Single access point with namespaced sub-accessors. One factory closure (`f.Config()`), one interface.

```go
type Config interface {
    // Store accessors — each delegates to a composed Store[T].Get()
    Settings() Settings         // → ~/.config/clawker/settings.yaml
    Project() *Project          // → merged walk-up result

    // Typed mutation — separate methods per store (different generic types)
    SetProject(fn func(*Project))              // in-memory mutation → tree update
    SetSettings(fn func(*Settings))            // in-memory mutation → tree update
    WriteProject(filename ...string) error     // provenance-routed atomic write
    WriteSettings(filename ...string) error    // provenance-routed atomic write

    // Path helpers, constants, labels (~40 methods)
    ConfigDir() string
    Domain() string
    LabelDomain() string
    // ...
}
```

`cfg.SetProject(fn)` / `cfg.WriteProject()` and `pm.Set(fn)` / `pm.Write()` share the same familiar API shape — thin wrappers around `Store[T].Set` / `Store[T].Write`, consistent across all things that compose `Store[T]`.

**Usage:**
- `cfg.Project().Build.Image` — from merged config walk-up
- `cfg.Settings().Logging.FileEnabled` — from settings.yaml
- `cfg.MonitoringConfig()` — typed convenience accessor
- `cfg.ConfigDirEnvVar()` — constants via interface methods

**No collision risk:** If both schemas grow a `Build` section, `cfg.Settings().Build` vs `cfg.Project().Build`.

**No generic `Get(key)` / `Set(key, val)`.** Typed mutation via `SetProject(fn)` / `SetSettings(fn)` only.

#### Node Tree Architecture

The node tree (`map[string]any`) is the merge engine and persistence layer. The typed struct `*T` is a deserialized view — the read/write API.

```
Load:   file → map[string]any ─┐
                                ├→ merge maps → deserialize → *T
        string → map[string]any ─┘

Set:    *T (mutated) → structToMap → merge into tree → mark dirty

Write:  tree → route by provenance → per-file atomic write
```

**Why not struct-based merge:** `yaml.Marshal` respects `omitempty` tags, silently dropping fields set to zero values (e.g., `false`, `0`, `""`). Map-based merge avoids this — absent keys mean "not set" (not iterated), present keys with zero values mean "explicitly set". `structToMap` uses reflection to serialize structs ignoring `omitempty` tags, so explicit clears survive.

#### Two-Phase Load

1. **Phase 1 (lenient):** YAML → `map[string]any` → run precondition migrations → re-save if anything changed
2. **Phase 2 (typed):** Merged map → typed struct via YAML round-trip. Only known keys read, unknowns silently ignored. Struct defaults fill missing keys.

Unknown fields silently ignored — matches Claude Code and Serena. No `KnownFields(true)`. Typos are the user's problem.

`structToMap` (used in `Set`) ignores `omitempty` and preserves unknown keys via `mergeIntoTree`. Raw YAML content that the struct doesn't model survives round-trips.

#### Merge Strategy

**Walk-up merge order for ConfigFile:**

```
WithDefaults(yaml) → ~/.config/clawker/clawker.yaml → walk-up configs → env vars → CLI flags
```

**Configuration precedence** (highest to lowest):

1. CLI flags
2. Environment variables (hardcoded shortlist)
3. `.clawker.local.yaml` or `.clawker/clawker.local.yaml` (personal overrides)
4. `.clawker.yaml` or `.clawker/clawker.yaml` (project config, committed)
5. `~/.config/clawker/clawker.yaml` (global defaults)
6. `WithDefaults(DefaultConfigYAML)` — YAML string template, base layer

**Defaults as YAML templates:** The same `DefaultConfigYAML` constant serves as both the base merge layer (parsed, comments ignored) and the scaffolding template written by `clawker init` (comments preserved). One source of truth — no imperative `SetDefaults()`, no struct tag defaults.

At each walk-up level, dir form (`.clawker/`) wins over flat form (`.clawker.yaml`) — they are mutually exclusive per directory.

Higher precedence wins silently (no warnings on override).

**Per-field merge for arrays** via struct tags:

| Tag | Behavior | Used By |
|-----|----------|---------|
| `merge:"union"` | Additive, deduped | `from_env`, `packages`, `includes`, `firewall.sources` |
| `merge:"overwrite"` | Project wins entirely | `copy`, `root_run`, `user_run`, `inject.*` |
| (none / scalar) | Last-wins | All scalar fields |

Untagged slices default to overwrite at runtime (safe fallback). A reflection test in CI asserts every `[]T` field has an explicit `merge` tag — missing tag = test failure. Go can't enforce struct tags at compile time; test + CI gate is the standard approach.

**SettingsFile:** Loaded separately, not merged with ConfigFile.

**Env var overrides:** Removed. The old Viper-based `CLAWKER_*` env var binding has been eliminated. Env vars only affect directory resolution (`CLAWKER_CONFIG_DIR`, `CLAWKER_DATA_DIR`, `CLAWKER_STATE_DIR`).

#### Migrations

Precondition-based idempotent functions (Claude Code + Serena pattern):

```go
func migrateOldBuildKey(raw map[string]any) bool {
    // Check: does old data shape exist?
    if _, ok := raw["old_key"]; !ok {
        return false // already current or never had old shape
    }
    // Transform: old shape → new shape
    raw["new_key"] = raw["old_key"]
    delete(raw, "old_key")
    return true // signal: re-save needed
}
```

- Each migration checks if old data shape exists in the raw map
- If found: transform → re-save → done
- If not found: skip (already current or never applied)
- No version field, no migration chain, no ordering constraints
- Runs during Phase 1 of load (on the raw map, before struct validation)
- Idempotent by construction — safe for concurrent processes

#### Write Model

All writes go through `Set*()` + `Write*()` on the composed `Store[T]`:

```go
cfg.SetProject(func(p *Project) { p.Build.Image = "ubuntu:24.04" })
cfg.WriteProject("clawker.local.yaml")

cfg.SetSettings(func(s *Settings) { s.DefaultImage = "custom:latest" })
cfg.WriteSettings()
```

| Call | Target File | Writer | When |
|------|-------------|--------|------|
| `cfg.WriteSettings()` | `~/.config/clawker/settings.yaml` | `clawker init`, image commands | Settings mutation |
| `cfg.WriteProject()` | Auto-routed by provenance | Programmatic | Project config updates |
| `cfg.WriteProject("clawker.local.yaml")` | First layer matching filename | User/programmatic | Personal overrides |
| `pm.Write()` | `~/.local/share/clawker/registry.yaml` | `internal/project` | Runtime CRUD |

Write semantics: `Set*()` mutates in-memory struct + serializes back into node tree via `structToMap`. `Write*()` persists the current tree — routes fields by provenance, read-merge-write per file with atomic I/O (temp+fsync+rename).

Settings files do NOT need locking — per-machine, no concurrent writers. Registry uses flock (owned by `internal/project`).

#### Package Ownership

| Package | Owns | Imports |
|---------|------|---------|
| `internal/storage` | Node tree engine (map-based merge, provenance), structToMap (omitempty-safe), atomic write (temp+rename), flock, YAML read/write | Leaf — zero internal imports |
| `internal/config` | `settings.yaml` + `clawker.yaml` walk-up. One `Config` interface. Two schemas. | `storage`, `logger` |
| `internal/project` | `registry.yaml`. Project domain: registration, resolution, worktree lifecycle. | `storage`, `config`, `iostreams`, `logger` |

`internal/project` is a middle-tier domain package ("if I want project operations, I go here"). Registry is its persistence layer, not its identity — don't rename to `internal/registry`.

#### Testing Infrastructure

Storage provides mechanisms — composing packages (`config/mocks`, `project/mocks`) build test doubles and harnesses for their callers.

| Mechanism | What it does | Owned by |
|-----------|-------------|----------|
| `storage.NewFromString[T](yaml)` | Separate constructor. Bypasses the entire pipeline (no discovery, no migration, no layering, no merge). Parses YAML string → node tree → `*T`. No write paths — Set+Write errors. | `storage` |
| Real `Store[T]` + `t.TempDir()` | Full store pointed at a jailed temp dir. Consumer wires its own schemas/filenames/defaults. Full node tree plumbing. | Consumer (`config/mocks`, `project/mocks`) |

Consumer mock APIs stay unchanged (`NewBlankConfig`, `NewFromString`, `NewIsolatedTestConfig`, etc.). Callers never see `Store[T]` or `NewFromString[T]` directly.

`Store[T]` itself has no mock interface — it's a concrete struct composed inside `configImpl` / `projectManagerImpl`. The consumer interfaces (`Config`, `ProjectManager`) are the mock boundary, generated via `go:generate moq`.

## 3. System Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     cmd/clawker                              │
│                   (Cobra commands)                           │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│                  internal/docker                             │
│            (Clawker-specific middleware)                     │
│         - Config-dependent (Config interface)                 │
│         - Exposes interface for commands                     │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│                   pkg/whail                                  │
│              (External Engine - Reusable docker decorator)   │
│         - Label-based selector injection                     │
│         - Whitelist of allowed operations                    │
│         - Standalone library for other projects              │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│              github.com/moby/moby                            │
│                  (Docker SDK)                                │
└─────────────────────────────────────────────────────────────┘
```

### 3.1 External Engine (`pkg/whail`)

A standalone, reusable library for building isolated Docker clients. Designed for use in other container-based AI wrapper projects. Decorates docker client to scope methods to resources with managed labels while adding utility methods for filtering, checking, option merging.

**Core Mechanism: Selector Injection**

The engine wraps Docker SDK and injects label filtering logic for some methods. :

```go
// User calls:
engine.ContainerList(ctx, opts)

// Engine transforms to:
// ContainerList lists containers matching the filter.
// The managed label filter is automatically injected.
func (e *Engine) ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error) {
 options.Filters = e.injectManagedFilter(options.Filters)
 return e.APIClient.ContainerList(ctx, options)
}

// User calls:
engine.ContainerInspect(ctx, containerID)
// ContainerInspect inspects a container.
// Only inspects managed containers.
func (e *Engine) ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error) {
 isManaged, err := e.IsContainerManaged(ctx, containerID) // added checker method
 if err != nil {
  return types.ContainerJSON{}, ErrContainerInspectFailed(containerID, err)
 }
 if !isManaged {
  return types.ContainerJSON{}, ErrContainerNotFound(containerID)
 }
 return e.APIClient.ContainerInspect(ctx, containerID)
}

// checker method for if container is managed
// IsContainerManaged checks if a container has the managed label.
func (e *Engine) IsContainerManaged(ctx context.Context, containerID string) (bool, error) {
 info, err := e.APIClient.ContainerInspect(ctx, containerID)
 if err != nil {
  if client.IsErrNotFound(err) {
   return false, nil
  }
  return false, err
 }

 val, ok := info.Config.Labels[e.managedLabelKey]
 return ok && val == e.managedLabelValue, nil
}
```

**Whitelist Approach**

`Engine` is a concrete struct (not an interface) that embeds the Docker `APIClient` and selectively overrides methods with label-injecting wrappers. Only wrapped methods enforce isolation — unwrapped SDK methods remain accessible but are not used by clawker's higher layers.

**Blocked by Design**: Clawker's `internal/docker` layer only calls Engine methods that have label enforcement. Operations that cannot apply label filters (e.g., `system prune` without filters) are not called by clawker code.

**Docker API Compatibility**: Minimum supported version defined at compile-time. No feature detection or graceful degradation—fail fast on incompatible versions.

### 3.3 Factory Dependency Injection

The CLI layer follows the GitHub CLI's Factory pattern:

```
internal/clawker/cmd.go
    │ calls factory.New()
    ▼
internal/cmd/factory/default.go  ──imports──▶  internal/cmdutil  ◀──imports──  internal/cmd/*
    │                                               ▲
    │ imports heavy deps                            │
    │ (config, docker, hostproxy,                   │ imports for Factory type
    │  iostreams, logger, prompts)                  │ + utilities
    ▼                                               │
Returns *cmdutil.Factory                      Commands consume
with all closures wired                       *cmdutil.Factory
```

Factory is a pure struct with 9 closure/value fields — no methods. 3 eager (set directly), 6 lazy (closures with `sync.Once`):

**Eager**: `Version` (string), `IOStreams` (`*iostreams.IOStreams`), `TUI` (`*tui.TUI`)
**Lazy**: `Config` (`func() (config.Config, error)`), `Client` (`func(ctx) (*docker.Client, error)`), `GitManager` (`func() (*git.GitManager, error)`), `HostProxy` (`func() hostproxy.HostProxyService`), `SocketBridge` (`func() socketbridge.SocketBridgeManager`), `Prompter` (`func() *prompter.Prompter`)

The constructor in `internal/cmd/factory/default.go` wires all closures. Commands extract closures into per-command Options structs. Run functions only accept `*Options`, never `*Factory`.

### 3.4 Dependency Placement Decision Tree

When adding a new command helper or heavy dependency:

```
"Where does my heavy dependency go?"
              │
              ▼
Can it be constructed at startup,
before any command runs?
              │
       ┌──────┴──────┐
       YES            NO (needs CLI args, runtime context)
       │              │
       ▼              ▼
  3+ commands?    Lives in: internal/<package>/
       │          Constructed in: run function
  ┌────┴────┐     Tested via: inject mock on Options
  YES       NO
  │         │
  ▼         ▼
FACTORY   OPTIONS STRUCT
FIELD     (command imports package directly)
```

Rules:
- Implementation always lives in `internal/<package>/` — never in `cmdutil/`
- `cmdutil/` contains only: Factory struct (DI container), output utilities, arg validators
- Heavy command helpers live in dedicated packages: `internal/bundler/` (build utilities), `internal/project/` (registration), `internal/docker/` (container naming). Image resolution helpers live in `internal/cmdutil/` (`ResolveImageWithSource`, `FindProjectImage`)

See also `.claude/rules/dependency-placement.md` (auto-loaded).

#### Pattern A vs Pattern B — Side-by-Side

```
                    PATTERN A                      PATTERN B
                    Factory Field                  Options Nil-Guard
                    ─────────────                  ─────────────────
  Declared in       cmdutil/factory.go             cmd/<verb>/<verb>.go
  Constructed in    cmd/factory/default.go         run function (nil-guard)
  Constructed       once, at startup               per command execution
  Depends on        config, other Factory fields   CLI args, resolved targets
  Test injection    stub closure on Factory        set field on Options directly

  Production flow   factory.New() → closure        if opts.X == nil {
                    → stored on Factory                opts.X = real.New(...)
                    → extracted to Options          }

  Test flow         f := &cmdutil.Factory{          opts := &VerbOptions{
                        SomeDep: mockFn,                SomeDep: &mock{},
                    }                               }
```

**Key rule**: Breadth of use does NOT determine the pattern. A dependency used by 40 commands still uses Pattern B if it needs runtime context (CLI args, resolved targets, user selections).

### 3.2 Internal Client (`internal/docker`)

Clawker-specific middleware that builds on the External Engine:

- Initializes External Engine with clawker's label configuration
- Receives `Config` interface for label keys, naming, and path helpers
- Handles clawker-specific logic (agent naming, volume conventions)
- Exposes high-level interface for Cobra commands

```go
type Client struct {
    engine  docker.Engine
    config  *config.Config
    project string
}

func (c *Client) RunAgent(ctx context.Context, agent string, opts RunOptions) error {
    // High-level operation combining multiple engine calls
}
```

## 4. Command Taxonomy

Commands mirror the Docker CLI structure:

### 4.1 Structure

| Pattern | Usage | Examples |
|---------|-------|----------|
| `clawker <verb>` | Container runtime operations | `run`, `stop`, `build` |
| `clawker <noun> <verb>` | Resource operations | `container ls`, `volume rm` |

### 4.2 Primary Nouns

All Docker nouns are supported:

- `container` - Container management
- `volume` - Volume management
- `network` - Network management
- `image` - Image management
- `project` - Project registry management
- `worktree` - Git worktree management

### 4.3 Primary Verbs

**Runtime Verbs** (top-level):

- `init` - Initialize project configuration
- `build` - Build container image
- `run` - Build and run agent (idempotent)
- `stop` - Stop agent(s)
- `restart` - Restart agent(s)

**Resource Verbs** (under nouns):

- `ls` / `list` - List resources
- `rm` / `remove` - Remove resources
- `inspect` - Show detailed information
- `prune` - Remove unused resources

**Configuration Verbs**:

- `config check` - Validate configuration

**Observability Verbs**:

- `monitor up` - Start monitoring stack
- `monitor down` - Stop monitoring stack
- `monitor status` - Show stack status

### 4.4 Opaque to Docker

Clawker does **not** expose Docker passthrough commands. Users cannot run arbitrary Docker commands through clawker. The interface is completely opaque to Docker internals.

## 5. State Management

### 5.1 Stateless CLI

Clawker stores **no local state**. All state lives in Docker:

- Container state (running, stopped, etc.)
- Labels (project, agent, metadata)
- Volumes (workspace, config, history)

Benefits:

- Multiple clawker instances can operate concurrently
- No state synchronization issues
- Recovery is trivial—just query Docker

### 5.2 Idempotency

Operations mirror Docker's idempotency behavior:

- `run` on existing container: Attach to existing (race condition resolution)
- `build` when image exists: Rebuild (use cache by default)
- `stop` on stopped container: No-op success
- `rm` on non-existent: Error (or success with force)

### 5.3 Orphaned Resources

Handled identically to Docker:

- Volumes from deleted containers persist
- Images with no containers persist
- `prune` commands clean up unused resources

## 6. Error Handling

### 6.1 Error Taxonomy

| Category | Source | Handling |
|----------|--------|----------|
| User errors | Bad input, missing config | Next Steps guidance |
| Docker errors | Daemon unavailable, permission denied | Pass through directly |
| Container runtime | Captured from stderr | Stream to user |
| Network errors | Timeout, DNS failure | Pass through with context |
| Internal errors | Bugs, unexpected state | Stack trace in debug mode |

### 6.2 User Error Format

```
Error: No clawker.yaml found in current directory

Next steps:
  1. Run 'clawker init' to create a configuration
  2. Or change to a directory with clawker.yaml
```

### 6.3 Exit Codes

General codes (extensible later):

- `0` - Success
- `1` - General error

Pattern follows GitHub CLI conventions.

## 7. Security Model

### 7.1 Credential Handling

Credentials are passed via environment variables:

- `ANTHROPIC_API_KEY` - Primary authentication
- `ANTHROPIC_AUTH_TOKEN` - Token-based auth
- Filtered from `.env` files (sensitive pattern matching)

Note: Environment variables are visible in `docker inspect`. This is accepted for simplicity.

**Container credential injection**: When `claude_code.use_host_auth` is true (default),
`containerfs.PrepareCredentials()` copies the host's `~/.claude/.credentials.json` into
the config volume. The `docker.CopyToVolume` two-phase chown ensures UID 1001 ownership.
This supplements environment variable passing for persistent credential storage.

### 7.2 Firewall — Envoy+CoreDNS Sidecar Architecture

The firewall uses an **Envoy proxy + CoreDNS** sidecar pair running as managed Docker containers, not per-container iptables rules.

**Why this architecture:**
- **DNS deny-by-default**: CoreDNS returns NXDOMAIN for unlisted domains — agents can't even resolve blocked hosts. Upstream: Cloudflare malware-blocking (`1.1.1.2`, `1.0.0.2`).
- **TLS inspection**: Envoy terminates TLS with per-domain MITM certificates (ECDSA P256 CA), enabling path-level filtering. Passthrough mode for domains without path rules.
- **Hot reload**: Rule changes regenerate `envoy.yaml` and `Corefile` — Envoy picks up config via restart, CoreDNS via reload plugin (2s).
- **Shared infrastructure**: One Envoy+CoreDNS pair serves all agent containers, rather than per-container iptables (which requires `CAP_NET_ADMIN` inside the container for rule management).

**Daemon isolation**: The firewall runs as a separate detached process (`EnsureDaemon()`), not as part of the CLI command. The daemon manages container lifecycle and runs dual health check loops (Envoy TCP + CoreDNS HTTP, 5s interval). A container watcher loop (30s) exits the daemon when no clawker containers are running.

**Network design**: All firewall containers and agent containers share a `clawker-net` Docker bridge network. Envoy and CoreDNS get static IPs computed from the network gateway (`.2` and `.3`). Agent containers use iptables DNAT rules (set up by `init-firewall.sh` running as root before privilege drop) to redirect DNS to CoreDNS and HTTPS to Envoy.

**Rule merge strategy**: System-required rules (Claude API, Docker registry) are always present. Project rules from `.clawker.yaml` (`add_domains`, `rules`) merge additively — project rules never replace system rules. Dedup key: `destination:protocol:port`. The rules store uses `storage.Store[EgressRulesFile]` with file-level locking.

**Certificate PKI**: A persistent ECDSA P256 CA is generated on first run. Per-domain certificates are generated for domains requiring MITM inspection (path rules). The CA cert is injected into agent containers at creation time via `containerfs`. `clawker firewall rotate-ca` regenerates everything.

**Bypass escape hatch**: `clawker firewall bypass` grants temporary unrestricted egress by flushing iptables rules, auto-re-enabling after a configurable timeout.

**Entrypoint privilege model**: Container entrypoint runs as root → `init-firewall.sh` sets up iptables DNAT rules → `gosu` drops to unprivileged `claude` user. Containers still need `NET_ADMIN` + `NET_RAW` capabilities for the DNAT setup.

### 7.3 Strict Label Ownership

Clawker **refuses** to operate on resources without proper labels:

```go
if !hasLabel(container, "dev.clawker.managed", "true") {
    return ErrNotManagedResource
}
```

This prevents accidental operations on user's non-clawker Docker resources.

## 8. Multi-Agent Operations

### 8.1 Relationships

```
┌─────────┐     ┌─────────┐     ┌─────────┐
│ Project │────<│  Agent  │>────│  Image  │
└─────────┘     └─────────┘     └─────────┘
     1               *               *
```

- One project has many agents
- Many agents can share one image
- One agent uses one image at a time

### 8.2 Race Condition Resolution

When two processes attempt to create the same agent:

1. First process creates the container
2. Second process detects existing container
3. Second process attaches to existing container

No error, no duplicate—deterministic behavior.

## 9. Observability

### 9.1 Monitoring Stack

A Docker Compose stack on `clawker-net`:

- **OpenTelemetry Collector** - Telemetry aggregation
- **Prometheus** - Metrics collection
- **Jaeger** - Distributed tracing
- **Loki** - Log aggregation
- **Grafana** - Visualization dashboards

Container images are built with OTEL environment variables pointing to the collector.

### 9.2 Verbosity Levels

| Flag | Level | Output |
|------|-------|--------|
| `-q, --quiet` | Quiet | Minimal output |
| (default) | Normal | Human-friendly |
| `-v, --verbose` | Verbose | Detailed output |
| `-D, --debug` | Debug | Developer diagnostics |

### 9.3 Progress Reporting

| Operation Type | Indicator | Package |
|----------------|-----------|---------|
| Indeterminate (short) | Goroutine spinner | `iostreams` (`StartSpinner`, `RunWithSpinner`) |
| Determinate (image pull) | Progress bar | `iostreams` (`ProgressBar`) |
| Multi-step (image build) | Tree display with per-stage child windows | `tui` (`RunProgress`) |
| Streaming (logs) | Partial screen with progress indicators | `iostreams` |

**Tree display** is the primary pattern for multi-step operations. TTY mode renders a BubbleTea sliding-window view; plain mode prints incremental text. Both driven by the same `chan ProgressStep` event channel.

### 9.4 Presentation Layer Patterns

The **4-scenario output model** determines which packages a command imports:

| Scenario | Packages | When to use |
|----------|----------|-------------|
| Static | `iostreams` + `fmt` | Print-and-done: lists, inspect, rm |
| Static-interactive | `iostreams` + `prompter` | Confirmation prompts mid-flow |
| Live-display | `iostreams` + `tui` | Continuous rendering, no user input |
| Live-interactive | `iostreams` + `tui` | Full keyboard input, navigation |

**TUI Factory noun** (`*tui.TUI`): Created eagerly by Factory. Commands capture `f.TUI` in their Options struct. Lifecycle hooks are registered later via `RegisterHooks()` — this decouples hook registration from TUI creation.

**Key types**:
- `tui.TUI` — Factory noun wrapping IOStreams; owns hooks + delegates to `RunProgress`
- `tui.RunProgress(ctx, ios, ch, cfg)` — Generic progress display (BubbleTea TTY + plain text fallback)
- `tui.ProgressStep` — Channel event: `{ID, Name, Status, LogLine, Cached, Error}`
- `tui.ProgressDisplayConfig` — Configuration with `CompletionVerb`, callback functions (`IsInternal`, `CleanName`, `ParseGroup`, `FormatDuration`, `OnLifecycle`)
- `tui.LifecycleHook` — Generic hook function type; threaded via config, nil = no-op
- `tui.HookResult` — `{Continue bool, Message string, Err error}` — controls post-hook flow

## 10. Container Lifecycle

### 10.1 State Machine

```
           ┌──────────┐
           │ Building │
           └────┬─────┘
                ▼
     ┌──────────────────┐
     │     Created      │
     └────────┬─────────┘
              ▼
     ┌──────────────────┐     ┌──────────┐
     │     Running      │────▶│  Paused  │
     └────────┬─────────┘     └──────────┘
              ▼
     ┌──────────────────┐
     │     Stopped      │
     └────────┬─────────┘
              ▼
     ┌──────────────────┐
     │     Removed      │
     └──────────────────┘
```

### 10.2 Signal Handling

All signals (Ctrl+C, etc.) are passed through to Docker. Clawker does not intercept or transform signals.

### 10.3 Restart Policies

Restart policies are passed directly to Docker:

```yaml
agent:
  restart: unless-stopped
```

### 10.4 Container Init

New containers require one-time initialization to inherit the host user's Claude Code
configuration (settings, plugins, credentials). This avoids manual re-authentication
and plugin installation on every container creation.

**Config schema** (`config.ClaudeCodeConfig` in `cfg.ProjectConfigFileName()`):
- `strategy`: `"copy"` (copy host config) or `"fresh"` (clean slate). Default: `"copy"`
- `use_host_auth`: Forward host credentials to container. Default: `true`

**Init flow** (orchestrated by `shared.CreateContainer()` in `cmd/container/shared/container.go`):

Progress streamed via events channel (`chan CreateContainerEvent`). Steps:
1. **workspace** — `workspace.SetupMounts()` + `workspace.EnsureConfigVolumes()`
2. **config** (skipped if volume cached) — `containerfs.PrepareClaudeConfig()` + `containerfs.PrepareCredentials()` → `docker.CopyToVolume()`
3. **environment** — `config.ResolveAgentEnv()` merges env_file/from_env/env → runtime env vars (warnings sent as `MessageWarning` events)
4. **container** — validate flags, `BuildConfigs()`, `docker.ContainerCreate()` + `InjectPostInitScript()` (when `agent.post_init` configured). Onboarding bypass is image-level: entrypoint seeds `~/.claude/.config.json` from staged defaults

**Key packages**: `internal/containerfs` (tar preparation, path rewriting),
`internal/workspace` (volume lifecycle), `internal/cmd/container/shared` (orchestration)

## 11. Testing Strategy

### 11.1 Integration Regression Tests

Tests run against real Docker—no mocking:

- Ensures actual Docker behavior
- Catches API compatibility issues
- Validates label filtering works correctly

**Before completing any code change:**

1. Run `go test ./...` - all unit tests must pass
2. Run `go test ./internal/cmd/...` - all integration tests must pass

### 11.2 Table-Driven Tests

Single test functions with case tables:

**engine test**

```go
func TestContainerOperations(t *testing.T) {
    test := []struct {
        name    string
        setup   func()
        action  func() error
        verify  func() error
    }{
        {"create and start", ...},
        {"stop running", ...},
        {"remove stopped", ...},
    }
    for _, tt := range test {
        t.Run(tt.name, func(t *testing.T) {
            // ...
        })
    }
}
```

**cli command test**

```go
func TestNewCmdStop(t *testing.T) {
    tests := []struct {
        name    string
        args    []string
        wantErr bool
    }{
        {name: "no args", args: []string{}, wantErr: true},
        {name: "with agent flag", args: []string{"--agent", "dev"}},
        {name: "with container name", args: []string{"clawker.myapp.dev"}},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            tio, _, _, _ := iostreams.Test()
            f := &cmdutil.Factory{IOStreams: tio}

            var gotOpts *StopOptions
            cmd := NewCmdStop(f, func(opts *StopOptions) error {
                gotOpts = opts
                return nil
            })

            cmd.SetArgs(tt.args)
            cmd.SetIn(&bytes.Buffer{})
            cmd.SetOut(&bytes.Buffer{})
            cmd.SetErr(&bytes.Buffer{})

            err := cmd.Execute()
            if tt.wantErr {
                require.Error(t, err)
                return
            }
            require.NoError(t, err)
            require.NotNil(t, gotOpts)
        })
    }
}
```

### 11.3 Config Package Testing

Use test doubles from `internal/config/mocks/` (import as `configmocks`) for all callers:

- `configmocks.NewBlankConfig()` — defaults-seeded config for simple unit tests
- `configmocks.NewFromString(projectYAML, settingsYAML)` — deterministic pre-seeded state from YAML strings (NO defaults)
- `configmocks.NewIsolatedTestConfig(t)` — full isolated config with temp directories for write tests

See `internal/config/CLAUDE.md` for detailed test helper documentation.

## 12. Dependencies

| Dependency | Purpose |
|------------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `gopkg.in/yaml.v3` | YAML configuration parsing |
| `github.com/moby/moby` | Docker SDK |
| `github.com/rs/zerolog` | Structured logging |
| `github.com/charmbracelet/bubbletea` | Terminal UI framework (TUI package only) |
| `github.com/charmbracelet/bubbles` | TUI components — spinner, viewport, key (TUI package only) |
| `github.com/charmbracelet/lipgloss` | Terminal styling (iostreams package only) |
| `github.com/go-git/go-git/v6` | Git operations (git package only) |
| `golang.org/x/term` | Terminal capabilities (term package only) |
