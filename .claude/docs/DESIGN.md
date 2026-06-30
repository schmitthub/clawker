# Clawker Design Document

Clawker is a Go CLI tool that wraps the Claude Code agent in secure, reproducible Docker containers.

## Related Docs

- `.claude/docs/ARCHITECTURE.md` — package layering and dependency boundaries.
- `internal/storage/CLAUDE.md` — storage package API, node tree architecture, merge/write internals.
- `internal/config/CLAUDE.md` — config package contracts, persistence model, and test helpers.

## 0a. Critical Invariant: CP Crashing Is a Security Incident

**This precedes everything else in this document. Internalize it before reading on.**

CP crashing is not an availability problem — it is a security boundary integrity problem. The reason:

- eBPF firewall programs are CP-managed but **kernel-pinned** under `/sys/fs/bpf`. They survive CP container death.
- Clean lifecycle (`firewall.Stack.Stop()` → `ebpfMgr.FlushAll()`) only runs on the `AgentWatcher` drain-to-zero path. **Panics, `log.Fatal`, unrecovered goroutines all skip it.**
- After a CP crash: agent containers keep running, eBPF keeps filtering against the rules loaded at crash time, but **CP is no longer there to observe, update rules, expire bypasses, or dispatch containment**.
- The user has no idea. Their agents look healthy. Their mental model — "CP has them covered" — is silently false. They are now exposed to prompt injection, exfiltration, and lateral-movement attempts CP would otherwise see and contain.

**Operational consequences when CP is dead but eBPF is still attached:**

- `clawker firewall add <domain>` updates the file but reload requires CP → silently dropped.
- An in-flight `clawker firewall bypass <duration>` loses its expiry timer → the bypass is permanent until manual intervention.
- No CP→clawkerd Session → no observation, no command dispatch, no containment.
- `clawker controlplane status` reports CP down, but the user has to know to look. Stack traces land on `os.Stderr` → `docker logs <cp>`, NOT in the rotating `ControlPlaneLogFile` operators are wired to grep.

**Therefore — design rules for any code reachable from `cmd/clawkercp/main.go` after `SetReady`:**

1. **No `panic()`. No `log.Fatal()`. No `os.Exit()`.** Constructors return `(nil, error)`. main.go logs and degrades.
2. **Long-lived goroutines must `recover()`.** Heartbeats, watchers, RPC handlers — one bad event must not silently strand eBPF.
3. **Subsystem failures degrade, never escalate.** Broken Executor → `executor = nil`; broken dialer → `dialer = nil`. Everything else stays up. The patterns in `cmd/clawkercp/main.go` — `wireExecutor` (executor; emits `event=agent_executor_unavailable`) and the `agent.New(...)` block that degrades on error to `event=agent_dialer_unavailable` — are canonical.
4. **Every degraded path emits a structured log line** (`event=<subsystem>_unavailable`) with component, error, and blast-radius fields. Operators will not see panic stacks; the structured log is the only surface.
5. **The only acceptable hard-exits** are pre-`SetReady` startup gates (no agents running yet, eBPF not load-bearing) and the orchestrator's intentional drain-to-zero clean exit.

If you're tempted to panic in CP code, you are about to turn a logic bug into a silent firewall failure. See `CLAUDE.md` (project root) and `controlplane/CLAUDE.md` for the full statement and templates.

## 0. Common Confusion: CP ≠ Firewall

The Control Plane (CP) and the firewall are NOT the same thing. LLM sessions repeatedly conflate them — see project-root `CLAUDE.md` for the full callout. Summary:

- **CP** = unconditional auth + gRPC infrastructure (AdminService on AdminPort, AgentService listener on AgentPort, sqlite-persisted agent registry, CP→clawkerd `agent.Dialer` outbound dialer, pub/sub event topics, mTLS, owns clawker-net). Always running whenever any clawker container exists. Boots via `manager.EnsureRunning`. There is no "disable CP" flag.
- **Firewall** = one optional subsystem CP manages (Envoy + CoreDNS + eBPF egress enforcement). Toggled by `firewall.enable` in `settings.yaml` (global). Per-project rules are separate (`security.firewall.add_domains` / `rules` in `clawker.yaml`).
- Disabling firewall does NOT disable CP, clawker-net, mTLS, agent registry, agent.Dialer→clawkerd Session, ListAgents, or any non-firewall AdminService RPC.
- **CP owns firewall**, not vice versa. Older framings that put firewall above or alongside CP are stale.

Do not gate non-firewall behavior on `firewall.enable` (settings.yaml).

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

**Project Resolution**: project-root resolution lives in `internal/project` as methods on the exported `Registry` facade (`ResolveRoot`/`CurrentRoot`), reading the registry. The CLI factory constructs one `Registry` per process (`f.ProjectRegistry`) and every consumer shares it. Callers resolve the root and pass it to `config.NewConfig(config.WithProjectRoot(root))`, which bounds the project-config walk-up merge (see §2.4) at that directory. `config` receives the root as a plain path; it does not resolve it. The `Config` interface exposes typed accessors — all file paths and constants are private to the package.

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

1. **Register**: `clawker project init` or `clawker project register` adds a slug→path entry to the registry file (`consts.RegistryFile`, data dir; owned by `internal/project`)
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
    Logging      LoggingConfig        `yaml:"logging,omitempty"`
    Monitoring   MonitoringConfig     `yaml:"monitoring,omitempty"`
    HostProxy    HostProxyConfig      `yaml:"host_proxy,omitempty"`
    Firewall     FirewallSettings     `yaml:"firewall,omitempty"`
    ControlPlane ControlPlaneSettings `yaml:"control_plane,omitempty"`
    Docker       DockerSettings       `yaml:"docker,omitempty"`
}

// Project defaults — tiered via walk-up (global → project → local)
type Project struct {
    Name      string          `yaml:"name,omitempty"`
    Build     BuildConfig     `yaml:"build"`
    Agent     AgentConfig     `yaml:"agent"`
    Workspace WorkspaceConfig `yaml:"workspace"`
    Security  SecurityConfig  `yaml:"security"`
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
    // Store accessors (preferred — direct access to the underlying Store[T])
    ProjectStore() *storage.Store[Project]   // → project config store
    SettingsStore() *storage.Store[Settings] // → settings store

    // Schema accessors
    Settings() *Settings         // → ~/.config/clawker/settings.yaml
    Project() *Project           // → merged walk-up result

    // Path helpers, constants, labels (~40 methods)
    ConfigDirEnvVar() string
    Domain() string
    LabelDomain() string
    // ...
}
```

`cfg.ProjectStore().Set(path, value)` / `cfg.ProjectStore().Write()` and `cfg.SettingsStore().Set(path, value)` / `cfg.SettingsStore().Write()` are the mutation API — thin wrappers on `Store[T].Set` / `Store[T].Write` (with `Remove(path)` for clears).

**Usage:**
- `cfg.Project().Build.Image` — from merged config walk-up
- `cfg.Settings().Logging.MaxSizeMB` — from settings.yaml
- `cfg.MonitoringConfig()` — deprecated convenience accessor (prefer `cfg.SettingsStore().Read().Monitoring`)
- `cfg.ConfigDirEnvVar()` — constants via interface methods

**No collision risk:** If both schemas grow a `Build` section, `cfg.Settings().Build` vs `cfg.Project().Build`.

**Path-based mutation.** `cfg.ProjectStore().Set(path, value)` / `Remove(path)` and `cfg.SettingsStore().Set(path, value)` mutate by dotted path; `Read()` returns the typed snapshot and `Get(path, &dest)` decodes one field. There is no closure mutator.

#### Node-Native Architecture

Every layer and the merged tree are `yaml.Node` trees, so comments ride from load through merge to write. The typed struct `*T` is an immutable snapshot decoded from the merged node and published via `atomic.Pointer` (lock-free `Read`).

```
Load:   file/string → layer node ─→ merge nodes → decode → immutable *T

Set:    encode value → graft into merged node at path → mark dirty → re-decode

Write:  dirty paths → route by provenance → graft into target layer node → per-file atomic write
```

**Why node-native:** `yaml.Marshal` respects `omitempty` tags, silently dropping fields set to zero values (e.g., `false`, `0`, `""`). Grafting the encoded value straight into the node tree avoids this — the value handed to `Set` lands as-is, so explicit zero values survive and per-field comments are preserved across a merge mutation.

#### Two-Phase Load

1. **Phase 1 (lenient):** YAML → layer node → run precondition migrations against each layer's own node → re-save any layer a migration changed
2. **Phase 2 (typed):** Merged node → typed struct snapshot via `decode`. Only known keys read, unknowns silently ignored. Struct defaults fill missing keys.

Unknown fields silently ignored — matches Claude Code and Serena. No `KnownFields(true)`. Typos are the user's problem.

Node merge preserves unknown keys not in the struct schema, so raw YAML content the struct doesn't model survives round-trips.

#### Merge Strategy

**Walk-up merge order for ConfigFile:**

```
WithDefaultsFromStruct[T]() → ~/.config/clawker/clawker.yaml → walk-up configs → env vars → CLI flags
```

**Configuration precedence** (highest to lowest):

1. CLI flags
2. Environment variables (hardcoded shortlist)
3. `.clawker.local.yaml` or `.clawker/clawker.local.yaml` (personal overrides)
4. `.clawker.yaml` or `.clawker/clawker.yaml` (project config, committed)
5. `~/.config/clawker/clawker.yaml` (global defaults)
6. `WithDefaultsFromStruct[T]()` — struct-tag-driven defaults, base layer

**Defaults from struct tags:** Default values are declared via `default:"value"` struct tags on schema types (`Project`, `Settings`). `storage.GenerateDefaultsYAML[T]()` reads these tags and produces a YAML string used as the base merge layer. `clawker project init` scaffolds config via language presets (`config.Presets()`) combined with `storage.WithDefaultsFromStruct[Project]()` — preset YAML provides language-specific fields, schema defaults fill the rest. One source of truth — no hand-written YAML template constants, no imperative `SetDefaults()`.

At each walk-up level, dir form (`.clawker/`) wins over flat form (`.clawker.yaml`) — they are mutually exclusive per directory.

Higher precedence wins silently (no warnings on override).

**Per-field merge via struct tags:**

| Tag | Behavior | Applies To | Used By |
|-----|----------|------------|---------|
| `merge:"union"` | Additive, deduped | Slices, maps | `security.firewall.add_domains`, `security.firewall.rules`, `build.instructions.labels` |
| `merge:"overwrite"` | Last-wins (explicit) | Slices, maps | (none currently — all overwrite fields use implicit default) |
| (none) | Last-wins | Scalars, slices, maps | All scalar fields, all untagged slices, `env` |

**Maps** are schema-aware: `tagRegistry` carries `FieldKind` so `mergeNodes` distinguishes `map[string]string` fields (opaque values) from struct nesting. Untagged maps default to last-wins (highest-priority layer's map replaces entirely). Tagged `merge:"union"` maps do key-by-key merge across layers.

Untagged slices default to overwrite at runtime (safe fallback). A reflection test in CI asserts every `[]T` field has an explicit `merge` tag — missing tag = test failure. Go can't enforce struct tags at compile time; test + CI gate is the standard approach.

**SettingsFile:** Loaded separately, not merged with ConfigFile.

**Env var overrides:** Removed. The old Viper-based `CLAWKER_*` env var binding has been eliminated. Env vars only affect directory resolution (`CLAWKER_CONFIG_DIR`, `CLAWKER_DATA_DIR`, `CLAWKER_STATE_DIR`).

#### Migrations

Precondition-based idempotent functions (Claude Code + Serena pattern):

```go
func migrateOldBuildKey(s *storage.Store[Project]) (bool, error) {
    // Check: does the old data shape exist in this layer?
    var v any
    had, err := s.Get("old_key", &v)
    if err != nil {
        return false, err
    }
    if !had {
        return false, nil // already current or never had old shape
    }
    // Transform: old shape → new shape, then drop the legacy key.
    if err := s.Set("new_key", v); err != nil {
        return false, err
    }
    if _, err := s.Remove("old_key"); err != nil {
        return false, err
    }
    return true, nil // signal: re-save needed
}
```

- Each migration checks if the old data shape exists via the store's `Has`/`Get`
- If found: transform → re-save → done
- If not found: skip (already current or never applied)
- No version field, no migration chain, no ordering constraints
- Runs during Phase 1 of load (against each layer's own node, before struct validation)
- Idempotent by construction — safe for concurrent processes

#### Write Model

All writes go through `Set(path, value)` + `Write()` on the store obtained from the `Config` interface:

```go
cfg.ProjectStore().Set("build.image", "ubuntu:24.04")
cfg.ProjectStore().Write(storage.ToPath(localPath))

cfg.SettingsStore().Set("logging.max_size_mb", 100)
cfg.SettingsStore().Write()
```

| Call | Target File | Writer | When |
|------|-------------|--------|------|
| `cfg.SettingsStore().Write()` | `~/.config/clawker/settings.yaml` | `clawker project init` (bootstrap), settings commands | Settings mutation |
| `cfg.ProjectStore().Write()` | Auto-routed by provenance | Programmatic | Project config updates |
| `cfg.ProjectStore().Write(ToPath(p))` | Explicit absolute path | User/programmatic | Personal overrides |
| `pm.Write()` | `~/.local/share/clawker/registry.yaml` | `internal/project` | Runtime CRUD |

Write semantics: `Set(path, value)` grafts the encoded value into the in-memory node tree and marks the path dirty. `Write()` persists dirty fields — routes each by provenance, grafts into a clone of the target layer's node, encodes, and atomically writes per file (temp+fsync+rename).

Settings files do NOT need locking — per-machine, no concurrent writers. Registry uses flock (owned by `internal/project`).

#### Package Ownership

| Package | Owns | Imports |
|---------|------|---------|
| `internal/storage` | Node tree engine (node-native merge, provenance), path-based Set/Remove, atomic write (temp+rename), flock, YAML read/write | Leaf — only internal import is `internal/consts` (stdlib-only) |
| `internal/config` | `settings.yaml` + `clawker.yaml` walk-up. One `Config` interface. Two schemas. | `storage`, `logger` |
| `internal/project` | `registry.yaml`. Project domain: registration, resolution, worktree lifecycle. | `storage`, `consts`, `git`, `logger`, `text` |

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

Factory is a pure struct with closure/value fields — no methods. 3 eager (set directly), rest lazy (closures):

**Eager**: `Version` (string), `IOStreams` (`*iostreams.IOStreams`), `TUI` (`*tui.TUI`)
**Lazy**: `Config` (`func() (config.Config, error)`), `Client` (`func(ctx) (*docker.Client, error)`), `Logger` (`func() (*logger.Logger, error)`), `ProjectManager` (`func() (project.ProjectManager, error)`), `GitManager` (`func() (*git.GitManager, error)`), `HostProxy` (`func() hostproxy.Service`), `SocketBridge` (`func() socketbridge.SocketBridgeManager`), `Prompter` (`func() *prompter.Prompter`), `AdminClient` (`func(ctx) (adminv1.AdminServiceClient, error)`), `ControlPlane` (`func() manager.Manager`), `HttpClient` (`func() *http.Client`)

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
- Heavy command helpers live in dedicated packages: `internal/bundler/` (build utilities), `internal/project/` (registration), `internal/docker/` (container naming, image resolution — `ResolveImageWithSource`, `ResolveImage`)

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
    Engine *whail.Engine
    cfg    config.Config
    log    *logger.Logger
    // ...
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

- `settings edit` - Edit user settings

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

### 7.2 Firewall — Envoy + Custom CoreDNS + eBPF Architecture

#### Design Rationale

**Domain-based egress over IP allowlists**: IP-based firewall rules are fragile (CDN IPs rotate, cloud providers share ranges) and coarse-grained (an IP range may host both trusted and untrusted content). Domain-based rules let project configs express intent (`api.anthropic.com`, `registry.npmjs.org`) rather than infrastructure details. CoreDNS enforces deny-by-default at the DNS layer — agents cannot even resolve unlisted hosts.

**Shared infrastructure with eBPF routing**: One Envoy+CoreDNS+eBPF manager trio per host serves all agent containers. eBPF cgroup programs attached from outside each container steer traffic to Envoy (TCP) and CoreDNS (DNS). No per-container capabilities or firewall scripts needed — agent containers run fully unprivileged.

**Path rules and MITM inspection**: For domains requiring API-level control (e.g., allow `GET /v1/models` but block arbitrary uploads), Envoy terminates TLS with per-domain MITM certificates (ECDSA P256 CA). Domains without path rules use TLS passthrough with zero inspection overhead. This gives fine-grained control without penalizing simple allow/deny use cases.

**Hot-reload semantics**: Rule changes regenerate `envoy.yaml` and `Corefile` on disk AND atomically replace the global BPF `route_map` via gRPC `AdminService.SyncRoutes` RPC to the CP. Envoy picks up config via container restart; CoreDNS via its reload plugin (2s poll). No agent container restarts required — all running containers immediately see the updated rules. The CP owns `Manager.Load()` lifetime in-process, so the pinned `dns_cache` map persists across rule reloads by construction (old hot-reload pinning bug from `ebpfExec("init")` era is gone).

**CP-driven agent start — two plans (init, boot)**: clawkerd boots as PID 1, reads its mTLS bootstrap material, and serves the `:7700` listener. CP's `agent.Dialer` establishes the Session (Hello → HelloAck) and then runs two static plans over the Session bidi-stream, each gated on a flag in `HelloAck` so they're one-shot per condition (`controlplane/agent/{init_steps,boot_steps}.go`):

- **Init plan** — one-time per container, runs only when `HelloAck.Initialized == false`. Steps in order: `docker-socket`, `config`, `git`, `git-credentials`, `ssh`, `post_init` (the `agent.post_init` hook), then the terminal `Command_AgentInitialized`. clawkerd handles `AgentInitialized` by writing a writable-layer marker file (`consts.AgentInitializedMarkerPath`); a later Hello then reports `Initialized == true` and the init plan is skipped.
- **Boot plan** — runs on every start, only when `HelloAck.CmdRunning == false`. Steps in order: `pre_run` (the `agent.pre_run` hook), `docker-socket`, then the terminal `Command_AgentReady`. clawkerd handles `AgentReady` by forking the user CMD (default `claude`) with kernel-side privilege drop via `SysProcAttr.Credential`. `AgentReady` is no-op success on a reconnect where clawkerd already spawned the CMD.

So `post_init` is an init step (one-time) and `pre_run` is a boot step (every start). On a `docker start` after stop, init is skipped (marker present) but boot re-runs and re-forks the CMD.

**PID-1 privilege model**: eBPF programs are attached from outside the container by the eBPF manager. Agent containers require no elevated capabilities — they run fully unprivileged. clawkerd runs as root for log writes, bootstrap reads, and `Wait4(-1)` orphan drain; the user CMD is the privilege-dropped child, never the supervisor itself (kernel runs `setgroups → setgid → setuid` between fork and exec).

#### Implementation

The firewall uses an **Envoy proxy + custom CoreDNS + eBPF manager** trio running as managed Docker containers. eBPF cgroup programs perform all traffic routing. The CoreDNS image is a custom build (`clawker-coredns:latest`) of `cmd/coredns-clawker` embedding `internal/dnsbpf` — not stock `coredns/coredns`. See `.claude/docs/ARCHITECTURE.md` and `controlplane/firewall/CLAUDE.md` for the as-built design.

**Why this architecture:**
- **DNS deny-by-default**: CoreDNS returns NXDOMAIN for unlisted domains — agents can't even resolve blocked hosts. Upstream: Cloudflare malware-blocking (`1.1.1.2`, `1.0.0.2`).
- **Real-time dns_cache via dnsbpf plugin**: Every successful A-record response goes through the `dnsbpf` CoreDNS plugin, which writes `IP → {domain_hash, TTL}` into the pinned BPF `dns_cache` map. This replaced the previous startup-time DNS seed approach that was vulnerable to DNS round-robin. NXDOMAIN responses are never written to the cache.
- **TLS inspection**: Envoy terminates TLS with per-domain MITM certificates (ECDSA P256 CA), enabling path-level filtering. Passthrough mode for domains without path rules.
- **Live hot reload**: Rule changes regenerate `envoy.yaml` and `Corefile` AND atomically replace the global BPF `route_map` via `syncRoutes`. All running agent containers immediately see the new rules — no container restarts.
- **Global BPF route_map**: `route_key` is `{domain_hash, dst_port}` (no `cgroup_id`). Container enforcement is gated on presence in `container_map` — all enforced containers share the same routes. Enables live rule sync and eliminates 1×N route duplication.
- **Dual-stack IPv6 handling**: `cgroup/connect4` handles AF_INET sockets. `cgroup/connect6` routes IPv4-mapped IPv6 destinations (`::ffff:x.x.x.x`) with the same logic as connect4 — this closed a prior security bug where dual-stack sockets (SSH, curl, Node.js) bypassed the firewall entirely. `cgroup/sendmsg6` and `cgroup/recvmsg6` similarly handle UDP DNS via dual-stack sockets so `nslookup`-style resolvers can't bypass CoreDNS. Native IPv6 is denied (documented limitation).
- **Stale pin recovery**: `ebpf.Manager.Load()` detects pinned maps whose key/value sizes changed (e.g., after the `route_key` schema change) and removes them before loading new programs. Prevents startup failures after BPF struct changes.
- **Agent container capabilities**: Agent containers need zero Linux capabilities. The eBPF manager container handles BPF program loading/attachment from outside. The custom CoreDNS container requires `CAP_BPF + CAP_SYS_ADMIN` (plus a `/sys/fs/bpf` bind mount) so the `dnsbpf` plugin can open the pinned `dns_cache` map and write entries.

**Firewall container lifecycle**: The firewall runs as Docker containers managed by `Stack.EnsureRunning()`, called from CP startup. `Stack` manages container lifecycle and runs dual health check loops (Envoy HTTP + CoreDNS HTTP, 5s interval). The CP's agent watcher drives the `drain-to-zero` path that calls `Stack.Stop()` when no clawker containers are running.

**Network design**: All firewall containers and agent containers share a `clawker-net` Docker bridge network. Envoy gets `.2`, CoreDNS `.3`, and the eBPF manager `.4` (static IPs computed from the network gateway). eBPF `cgroup/connect4`/`connect6` programs rewrite agent container `connect()` calls to route traffic to Envoy/CoreDNS IPs.

**Startup ordering is security-critical**: `EnsureRunning` starts the eBPF container and runs `init` BEFORE Envoy and CoreDNS, because the `dnsbpf` plugin opens the pinned `dns_cache` map on CoreDNS startup and fails to boot if it doesn't exist. The `regenerateAndRestart` path preserves this invariant — it re-runs init and `syncRoutes` before restarting Envoy/CoreDNS.

**Embedded binaries**: The eBPF manager binary is embedded via `controlplane/manager/embed_ebpf.go` (`go:embed`); the custom CoreDNS binary via `controlplane/firewall/embed_coredns.go`. Each package builds its image on demand from the embedded binary using an inline Dockerfile SHA-pinned to `alpine:3.21`.

**Rule merge strategy**: System-required rules (Claude API, Docker registry) are always present. Project rules from `.clawker.yaml` (`add_domains`, `rules`) merge additively — project rules never replace system rules. Dedup key: `destination:protocol:port`. The rules store uses `storage.Store[EgressRulesFile]` with file-level locking.

**Certificate PKI**: A persistent ECDSA P256 CA is generated on first run. Per-domain certificates are generated for domains requiring MITM inspection (path rules). The CA cert is injected into agent containers at creation time via `containerfs`. `clawker firewall rotate-ca` regenerates everything.

**Bypass escape hatch**: `clawker firewall bypass` sets an eBPF bypass flag for instant unrestricted egress, auto-clearing after a configurable timeout. No rule flushing or re-application needed — just a BPF map update.

**PID-1 privilege model**: Agent containers run fully unprivileged. clawkerd is the container's `ENTRYPOINT` and runs as root only to host the mTLS listener, write log files, and reap reparented orphans; the user CMD it forks runs as the unprivileged `claude` user (kernel-side `setgroups → setgid → setuid` between fork and exec via `SysProcAttr.Credential`). eBPF programs attach from outside — no in-container firewall scripts or capabilities.

#### Per-decision telemetry / eBPF egress event stream

Every BPF decision point (`connect4`/`connect6`, `sendmsg4`/`sendmsg6`, `recvmsg6`) calls `submit_event` after it reaches a verdict. The kernel side is fully bounded:

- **Rate limiting** is enforced per-`cgroup_id` via a token-bucket in `ratelimit_state` (`BPF_MAP_TYPE_LRU_HASH` so dead cgroups self-evict). Tunables (`RATELIMIT_BURST=64`, `RATELIMIT_REFILL_NS=100ms`, `RATELIMIT_TOKENS_PER=64`) live as `#define`s in `bpf/common.h`. Records dropped by the limiter increment `ratelimit_drops` (per-cgroup) and never touch the ringbuf.
- **Kernel-fault drops** (ringbuf full or `bpf_ringbuf_reserve == NULL`) increment `events_drops` (PERCPU_ARRAY at key 0).
- The ringbuf itself (`events_ringbuf`) is single-producer-per-decision-point, single-consumer, 256 KiB sized for 32-byte `egress_event` records.

The userspace consumer is `netlogger.Service` (`controlplane/firewall/ebpf/netlogger/`):

1. **Drain**: A reader goroutine pulls records out of `events_ringbuf` via `cilium/ebpf/ringbuf` and forwards them through a bounded queue (default 8192) with drop-newest back-pressure (`QueueDropped` counter).
2. **Enrichment**: A processor goroutine decodes each record into a typed `Event` and resolves `cgroup_id → {container_id, agent, project}` via `LabelCache`. The cache is hydrated by `ebpf.EBPFContainerEnrolled` pub/sub events published by `firewall.Handler.FirewallEnable` and evicted by `dockerevents.DockerEvent` with `Action ∈ {die, destroy}`. `domain_hash → domain` resolution goes through `ReverseDNSMap`, rebuilt every refresh tick from the firewall rule set.
3. **Emit**: An `otelSink` shapes the enriched record into an OTLP log record (scope `clawker.netlogger`, event name `ebpf.egress`, `service.name=ebpf-egress`) and pushes it through a `*sdklog.LoggerProvider` over mTLS gRPC to the trusted-infra OTLP receiver on `OtelInfraPort`. Records land in the `clawker-ebpf-egress` OpenSearch index. Strict directive: every `Event` field maps to an attribute on every emitted record — empty strings and zero numbers ship verbatim, never dropped.

Collector-unavailable posture is binary per-CP-lifetime:

- **Startup preflight**: `controlplane.NewOtelLoggerProvider` performs a one-shot TLS dial against the OTLP endpoint. Failure returns an error to CP main; main emits `event=netlogger_unavailable` with the failing `step` field, leaves `netloggerSvc=nil`, and CP runs degraded with no per-decision telemetry. Firewall enforcement is untouched.
- **Runtime circuit breaker**: `netlogger.NewCircuitExporter` wraps the OTLP exporter inside the BatchProcessor. After 3 consecutive `Export` failures the breaker permanently trips, drops records on the floor, and emits a single `event=netlogger_collector_lost` line. No background reconnect — operators recover by restarting CP.

Drain ordering (in `cmd/clawkercp/main.go`'s drain callback) places `netloggerSvc.Stop(stopCtx)` BEFORE `ebpfMgr.FlushAll()` so the BatchProcessor flushes in-flight OTLP batches before the BPF maps the reader holds are torn down.

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

- **OpenTelemetry Collector** - Two OTLP receivers on distinct trust lanes: the untrusted `otlp` receiver (agent containers — Claude Code + clawker-cli telemetry) and the mTLS-gated `otlp/infra` receiver on `OtelInfraPort` (trusted CP-side emitters — CP zerolog bridge, Envoy access logs, CoreDNS otel plugin, netlogger). Routes logs into the six OpenSearch indices and exposes a Prometheus scrape endpoint for metrics. The two-lane split is load-bearing for netlogger's trust model — infra emitters carry per-handshake leaf certs minted by `otelcerts.Service` and never cross into the untrusted agent lane. A `traces` pipeline is configured but idle — agents don't emit spans today.
- **OpenSearch** - Logs only, split into six indices: `claude-code` (Claude Code OTLP push, untrusted port), `clawker-cli` (host CLI OTLP push, untrusted port), `clawkercp` (mTLS-gated CP push), `clawker-envoy` (Envoy access logs, mTLS-gated), `clawker-coredns` (CoreDNS query logs, mTLS-gated), and `clawker-ebpf-egress` (eBPF per-decision egress events from netlogger, `service.name=ebpf-egress`, mTLS-gated). Cross-index queries: `clawkercp,claude-code,clawker-cli,clawker-envoy,clawker-coredns,clawker-ebpf-egress`.
- **OpenSearch Dashboards** - UI for log exploration (Discover)
- **Prometheus** - Metrics storage + UI; also accepts direct OTLP push for callers willing to lose `/api/v1/metadata` coverage

Container images are built with OTEL environment variables pointing to the collector. The stack is preconfigured by a one-shot `clawker-opensearch-bootstrap` compose service that runs after OpenSearch reports `service_healthy` and before `otel-collector` / `prometheus` start (`service_completed_successfully` gate): component + index templates with explicit per-source field mappings (including `clawker-ebpf-egress` for netlogger records), per-index ingest pipelines on the trusted-infra OTLP lane (the `envelope-normalize` mirror that surfaces `resource.attributes.*` for OSD Explore default columns), a default ISM retention policy auto-attached via `ism_template.index_patterns`, and Dashboards index-pattern saved objects for all six indices. Source assets live in `internal/monitor/templates/opensearch-bootstrap/` and are re-applied every `monitor up`. Curated dashboards / visualizations / alerts are NOT yet shipped — adding them is a matter of dropping the Dashboards-exported NDJSON into `opensearch-bootstrap/saved-objects/clawker.ndjson` and shipping a new release.

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

Developer diagnostics go to zerolog; the caller owns all terminal output. Steps:
1. **workspace** — `workspace.SetupMounts()` (internally calls `EnsureConfigVolumes()`)
2. **config** (skipped if volume cached) — `containerfs.PrepareClaudeConfig()` + `containerfs.PrepareCredentials()` → `docker.CopyToVolume()`
3. **environment** — `shared.ResolveAgentEnv()` merges env_file/from_env/env → runtime env vars (warnings surfaced to the caller on the result)
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

1. Run `make test` - all unit tests must pass
2. Run `go test ./test/e2e/... -v -timeout 10m` - Docker-backed integration tests (requires Docker)

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
