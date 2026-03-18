# Clawker Architecture

> High-level architecture overview. Use Serena for detailed method/type exploration.

## Related Docs

- `.claude/docs/DESIGN.md` — behavior and product-level rationale.
- `internal/storage/CLAUDE.md` — storage package API, node tree architecture, merge/write internals.
- `internal/config/CLAUDE.md` — config package API, write semantics, and testing details.

## System Layers

```
┌─────────────────────────────────────────────────────────────┐
│                     cmd/clawker                              │
│                   (Cobra commands)                           │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│                  internal/cmd/*                              │
│            (Command implementations)                         │
└──────────┬──────────────────────────────────┬───────────────┘
           │                                  │
┌──────────▼──────────────────┐  ┌────────────▼──────────────┐
│     internal/docker          │  │   internal/firewall        │
│  (Clawker middleware)        │  │  (Envoy+CoreDNS stack)     │
│  - Labels, naming            │  │  - Daemon lifecycle        │
│  - Container orchestration   │  │  - Config generators       │
└──────────┬──────────────────┘  │  - Certificate PKI         │
           │                     │  - Rule management          │
┌──────────▼──────────────────┐  └────────────┬──────────────┘
│        pkg/whail             │               │
│  (Docker engine library)     │◄──────────────┘
│  - Label-based isolation     │  (uses whail for containers)
└──────────┬──────────────────┘
           │
┌──────────▼──────────────────┐
│    github.com/moby/moby      │
│       (Docker SDK)           │
└─────────────────────────────┘
```

## Factory Dependency Injection (gh CLI Pattern)

Clawker follows the GitHub CLI's three-layer Factory pattern for dependency injection:

```
┌─────────────────────────────────────────────────────────────────────────┐
│  Layer 1: WIRING (internal/cmd/factory/default.go)                      │
│                                                                         │
│  factory.New(version) → *cmdutil.Factory                                │
│    • Creates IOStreams, wires sync.Once closures for all dependencies    │
│    • Imports everything: config, docker, hostproxy, iostreams, prompts   │
│    • Called ONCE at entry point (internal/clawker/cmd.go)                │
│    • Tests NEVER import this package                                    │
├─────────────────────────────────────────────────────────────────────────┤
│  Layer 2: CONTRACT (internal/cmdutil/factory.go)                        │
│                                                                         │
│  Factory struct — pure data with closure fields, no methods             │
│    • Defines WHAT dependencies exist (Client, Config, Project, GitManager, etc.) │
│    • Importable by all cmd/* packages without cycles                    │
│    • Also provides error handling, name resolution, project utilities   │
├─────────────────────────────────────────────────────────────────────────┤
│  Layer 3: CONSUMERS (internal/cmd/*)                                    │
│                                                                         │
│  NewCmdFoo(f *cmdutil.Factory) → *cobra.Command                         │
│    • Cherry-picks Factory closure fields into per-command Options struct │
│    • Run functions accept *Options only — never see Factory             │
│    • opts.Client = f.Client assigns closure, not method reference       │
└─────────────────────────────────────────────────────────────────────────┘
```

**Why this pattern:**
- **Testability**: Tests construct `&cmdutil.Factory{IOStreams: tio}` with only needed fields
- **Decoupling**: cmdutil has no construction logic; factory/ imports the heavy deps
- **Transparent**: `f.Client(ctx)` syntax is identical for methods and closure fields
- **Assignable**: `opts.Client = f.Client` works naturally for Options injection

## Key Packages

### pkg/whail - Docker Engine Library

Reusable library with label-based resource isolation. Standalone for use in other projects.

**Core behavior:**
- Injects managed label filter on list operations
- Refuses to operate on resources without managed label
- Wraps Docker SDK methods with label enforcement

### internal/docker - Clawker Middleware

Thin layer configuring whail with clawker's conventions.

**Key abstractions:**
- Labels: `dev.clawker.managed`, `dev.clawker.project`, `dev.clawker.agent`
- Names: `clawker.project.agent` (containers), `clawker.project.agent-purpose` (volumes)
- Client embeds `whail.Engine`, adding clawker-specific operations

### Configuration & Storage Triad

Three packages form the configuration subsystem. `storage` is the engine, `config` and `project` are domain wrappers.

```
┌─────────────────────────────────────────────────────────────────────────┐
│  COMMANDS (internal/cmd/*)                                               │
│                                                                         │
│  cfg, _ := f.Config()              pm, _ := f.Project()                 │
│  cfg.Project().Build.Image         pm.Register(slug, path)              │
│  cfg.Settings().Logging            pm.ListWorktrees(ctx)                │
│  cfg.SetProject(fn); cfg.WriteProject()  pm.Resolve(cwd)               │
└────────────┬────────────────────────────────────┬───────────────────────┘
             │ Config interface                   │ ProjectManager interface
             ▼                                    ▼
┌────────────────────────────┐     ┌────────────────────────────────────┐
│  internal/config            │     │  internal/project                   │
│  (thin domain wrapper)      │     │  (thin domain wrapper)              │
│                             │     │                                     │
│  configImpl {               │     │  projectManagerImpl {               │
│    *Store[Project]       │     │    *Store[ProjectRegistry]                 │
│    *Store[Settings]     │     │  }                                  │
│  }                          │     │                                     │
│                             │     │  • Project CRUD, resolution         │
│  • Config interface         │     │  • Worktree lifecycle               │
│  • Schema types             │     │  • Registry schema                  │
│  • Filenames + migrations   │     │  • Registry migrations              │
│  • Path/constant helpers    │     │                                     │
└────────────┬────────────────┘     └──────────────────┬─────────────────┘
             │ composes                                │ composes
             ▼                                         ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  internal/storage                                                        │
│  Store[T] — generic layered YAML store engine                            │
│                                                                         │
│  ┌─────────────┐  ┌──────────────┐  ┌───────────────┐  ┌─────────────┐ │
│  │  Discovery   │  │ Load+Migrate │  │ Merge+Provenance│  │   Write    │ │
│  │             │  │              │  │               │  │             │ │
│  │ • Static    │  │ • Per-file   │  │ • N-way map   │  │ • Explicit  │ │
│  │   paths     │→│ • YAML→map  │→│   fold        │  │   scope     │ │
│  │ • Walk-up   │  │ • Migrations │  │ • merge: tags │  │ • Auto from │ │
│  │   patterns  │  │ • Re-save    │  │ • Provenance  │  │   provenance│ │
│  │ • Dual form │  │              │  │   tracking    │  │ • Atomic    │ │
│  └─────────────┘  └──────────────┘  └───────────────┘  └─────────────┘ │
│                                                                         │
│  Node tree (map[string]any) = merge engine + persistence layer          │
│  Typed struct *T = deserialized view (read/write API)                   │
│  structToMap = omitempty-safe serializer (Set → tree update)            │
│  Also: flock locking (optional), atomic I/O (temp+rename)               │
└─────────────────────────────────────────────────────────────────────────┘
```

**Key relationships:**
- Commands never see `storage` — they use `Config` and `ProjectManager` interfaces
- `config` and `project` are thin wrappers — they compose `Store[T]`, provide schemas/filenames/migrations, expose domain APIs
- `storage` is the engine — discovery, load, migrate, merge, provenance, write
- `storage` has zero domain knowledge — it doesn't know about clawker, config files, or registries

### internal/storage - Layered YAML Store Engine

Generic `Store[T]` that handles the full lifecycle of layered YAML configuration. Zero internal imports (leaf package). See `internal/storage/CLAUDE.md` for detailed API reference.

**Node tree architecture:** The node tree (`map[string]any`) is the merge engine and persistence layer. The typed struct `*T` is a deserialized view — the read/write API. Merge operates on maps only; the struct is deserialized from the merged tree at end of construction. This avoids the `omitempty` problem (YAML marshaling drops zero-value fields like `false` or `0`).

```
Load:   file → map[string]any ─┐
                                ├→ merge maps → deserialize → *T
        string → map[string]any ─┘

Set:    *T (mutated) → structToMap → merge into tree → mark dirty

Write:  tree → route by provenance → per-file atomic write
```

**Discovery** (how files are found — two additive modes):

| Mode | Options | Use case |
|------|---------|----------|
| Walk-up | `WithWalkUp()` | Config — CWD to project root, non-deterministic |
| Static | `WithConfigDir()` / `WithDataDir()` / `WithPaths()` | Registry, settings — known XDG locations |

**Filename-driven:** Store takes ordered filenames on construction (e.g., `"clawker.yaml"`, `"clawker.local.yaml"`). Walk-up is non-deterministic — at each level, checks `.clawker/{filename}` (dir form) first, falls back to `.{filename}` (flat dotfile). Both `.yaml`/`.yml` accepted. Bounded at registered project root — never reaches HOME.

**XDG convenience options:** `WithConfigDir()`, `WithDataDir()`, `WithStateDir()`, `WithCacheDir()` resolve directory paths and add them to the explicit path list. Precedence: `CLAWKER_*_DIR` > `XDG_*_HOME` > default. Explicit paths check `{dir}/{filename}` directly (no dir/flat form).

**Pipeline** (per file, before merge):
1. Read YAML → `map[string]any`
2. Run caller-provided migrations (precondition-based, idempotent)
3. Atomic re-save if any migration fired

Each file migrates independently — any file at any depth can be independently stale.

**Merge with provenance**: Fold N layer maps in priority order (closest to CWD = highest). Per-field merge strategy via `merge:"union"|"overwrite"` struct tags on `T`, extracted into a `tagRegistry` at construction. Provenance map tracks which layer won each field — used for auto-scoped writes. Absent keys mean "not set" (not iterated), present keys with zero values mean "explicitly set".

**Write model**: Explicit filename (`Write("clawker.local.yaml")`) or auto-route (`Write()` — provenance resolves each field's target). `structToMap` serializes the struct via reflection, ignoring `omitempty` tags. `mergeIntoTree` preserves unknown keys in the tree that aren't in the struct schema.

**Testing**: `storage.NewFromString[T](yaml)` is a separate constructor that bypasses the pipeline — parses YAML string → node tree → `*T`, no store machinery. Composing packages (`config/mocks`, `project/mocks`) use it to build their test doubles and use real `Store[T]` + `t.TempDir()` for isolated FS harnesses. `Store[T]` has no mock interface; consumer interfaces are the mock boundary.

**Imported by:** `internal/config`, `internal/project`

### internal/config - Configuration

Thin domain wrapper composing `storage.Store[Project]` + `storage.Store[Settings]`. Exposes the `Config` interface — a closed box where all file names, paths, and constants are private. Replaces Viper — no env var binding, no mapstructure, no fsnotify.

**Design principle**: If a caller needs information from the config package, it must use an existing `Config` method or propose a new one on the interface. No reaching into package internals.

**Two independent schemas, one interface:**
- `Settings` — host infrastructure (logging, host_proxy, monitoring)
- `Project` — project defaults (build, workspace, security, agent, loop). Tiered via walk-up.
- Callers access both through namespaced sub-accessors: `cfg.Settings().Logging`, `cfg.Project().Build.Image`, `cfg.ConfigDir()`

**File layout (full XDG — walk-up bounded at project root, never reaches HOME):**
```
~/.config/clawker/                   ← config (XDG_CONFIG_HOME)
  clawker.yaml                       ← ConfigFile (global project defaults)
  clawker.local.yaml                 ← ConfigFile (global personal overrides)
  settings.yaml                      ← SettingsFile (host infrastructure)

<walk-up-level>/                     ← dual placement (dir wins over flat)
  .clawker.yaml                      ← flat form (committed)
  .clawker.local.yaml                ← flat form (personal, gitignored)
  .clawker/                          ← OR directory form
    clawker.yaml                     ← dir form (committed)
    clawker.local.yaml               ← dir form (personal, gitignored)

~/.local/share/clawker/              ← data (XDG_DATA_HOME, owned by internal/project)
  registry.yaml                      ← project/worktree state

~/.local/state/clawker/              ← state (XDG_STATE_HOME)
  logs/                              ← log files
  cache/                             ← cached state
```

**Walk-up dual placement:** At each level, check for `.clawker/` dir first → use `clawker.yaml` inside it. No dir → fall back to `.clawker.yaml` flat dotfile. Mutually exclusive per directory.

**What `configImpl` provides to `Store[T]`:**
- Filenames (e.g., `"clawker.yaml"`, `"clawker.local.yaml"`) — ordered, same schema
- Migration functions (schema evolution)
- Schema types (`ConfigFile`, `SettingsFile`)
- Discovery options (`WithWalkUp`, `WithConfig`) — anchors locked in at construction

**What `configImpl` adds on top of `Store[T]`:**
- `Config` interface with namespaced accessors
- Path/constant helpers (`ConfigDir()`, `Domain()`, `LabelDomain()`, ~40 methods)
- `SetProject`/`SetSettings` + `WriteProject`/`WriteSettings` — typed mutation wrappers around `Store[T].Set`/`Write`

**Testing**: See `internal/config/CLAUDE.md` for test helpers and mocks.

**Boundary:**
- `config` defines schemas, filenames, migrations, and the domain interface.
- `storage` does all the mechanical work — discovery, load, migrate, merge, write.
- `project` owns project identity, CRUD, worktree lifecycle, and registry I/O.

### internal/cmd/* - CLI Commands

Two parallel command interfaces:

1. **Project Commands** (`clawker run/stop/logs`) - Project-aware, uses `--agent` flag
2. **Management Commands** (`clawker container/volume/network/image *`) - Docker CLI mimicry, positional args

Management command structure:
```
clawker container [list|inspect|logs|start|stop|kill|pause|unpause|restart|rename|wait|top|stats|update|exec|attach|cp|remove]
clawker volume    [list|inspect|create|remove|prune]
clawker network   [list|inspect|create|remove|prune]
clawker image     [list|inspect|build|remove|prune]
```

**Note**: `internal/cmd/container/shared/` contains domain orchestration logic (container init, onboarding) shared between `run/` and `create/`.

### internal/cmdutil - CLI Utilities

Shared toolkit importable by all command packages.

**Key abstractions:**
- `Factory` — Pure struct with closure fields (no methods, no construction logic). Defines the dependency contract. Constructor lives in `internal/cmd/factory/default.go`.
- Error types (`FlagError`, `SilentError`, `ExitError`) — centralized rendering in Main()
- Format/filter flags (`FormatFlags`, `FilterFlags`, `WriteJSON`, `ExecuteTemplate`)
- Arg validators (`ExactArgs`, `MinimumArgs`, `NoArgsQuoteReminder`)
- Image resolution (`ResolveImageWithSource`, `FindProjectImage`)
- Name resolution (`ResolveContainerName`, `ResolveContainerNames`)

### internal/cmd/factory - Factory Wiring

Constructor that builds a fully-wired `*cmdutil.Factory`. Imports all heavy dependencies (config, project, docker, hostproxy, iostreams, logger, prompts) and wires `sync.Once` closures.

**Key function:**
- `New(version string) *cmdutil.Factory` — called exactly once at CLI entry point

**Dependency wiring order:**
1. Config (lazy, `config.NewConfig()` via `sync.Once` — walk-up + settings load) → 2. HostProxy (lazy, reads Config) → 3. SocketBridge (lazy, reads Config) → 4. IOStreams (eager, logger initialized from Config) → 5. TUI (eager, wraps IOStreams) → 6. Project (lazy, owns registry.yaml independently from Config) → 7. Client (lazy, reads Config) → 8. GitManager (lazy, reads Config) → 9. Prompter (lazy)

Tests never import this package — they construct minimal `&cmdutil.Factory{}` structs directly.

### internal/iostreams - Testable I/O

Testable I/O abstraction following the GitHub CLI pattern.

**Key types:**
- `IOStreams` - Core I/O with TTY detection, color support, progress indicators
- `Logger` - Interface (`Debug/Info/Warn/Error() *zerolog.Event`) decoupling commands from `internal/logger`; set on IOStreams by factory
- `ColorScheme` - Color formatting that bridges to `tui/styles.go`
- `Test()` - Exported test constructor: `(*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer)` — nil Logger, uses `mocks.FakeTerm{}`

**Features:**
- TTY detection (`IsInputTTY`, `IsOutputTTY`, `IsInteractive`, `CanPrompt`)
- Color support with `NO_COLOR` env var compliance
- Progress indicators (spinners) for long operations
- Pager support (`CLAWKER_PAGER`, `PAGER` env vars)
- Alternate screen buffer for full-screen TUIs
- Terminal size detection with caching

### internal/prompter - Interactive Prompts

User interaction utilities with TTY and CI awareness.

**Key types:**
- `Prompter` - Interactive prompts using IOStreams
- `PromptConfig` - Configuration for string prompts
- `SelectOption` - Options for selection prompts

**Methods:**
- `String(cfg)` - Text input with default and validation
- `Confirm(msg, defaultYes)` - Yes/no confirmation
- `Select(msg, options, defaultIdx)` - Selection from list

## Other Key Components

| Package | Purpose |
|---------|---------|
| `internal/workspace` | Bind vs Snapshot strategies for host-container file sharing |
| `internal/containerfs` | Host Claude config preparation for container init: copies settings, plugins, credentials to config volume; prepares post-init script tar (leaf — keyring + logger only) |
| `internal/term` | Terminal capabilities, raw mode, size detection (leaf — stdlib + x/term only) |
| `internal/signals` | OS signal utilities — `SetupSignalContext`, `ResizeHandler` (leaf — stdlib only) |
| `internal/storage` | `Store[T]` — generic layered YAML store engine: discovery (static/walk-up), load+migrate, merge with provenance, scoped writes, atomic I/O, flock. Leaf — zero internal imports |
| `internal/config` | Thin wrapper composing `Store[Project]` + `Store[Settings]`. Exposes `Config` interface with namespaced accessors, path/constant helpers. See `internal/config/CLAUDE.md` |
| `internal/monitor` | Observability stack (Prometheus, Grafana, OTel) |
| `internal/logger` | Zerolog setup |
| `internal/cmdutil` | Factory struct (closure fields), error types, format/filter flags, arg validators |
| `internal/cmd/factory` | Factory constructor — wires real dependencies (sync.Once closures) |
| `internal/iostreams` | Testable I/O with TTY detection, colors, progress, pager |
| `internal/prompter` | Interactive prompts (String, Confirm, Select) |
| `internal/tui` | Reusable TUI components (BubbleTea/Lipgloss) - lists, panels, spinners, layouts, tables |
| `internal/bundler` | Image building, Dockerfile generation, semver, npm registry client |
| `internal/docs` | CLI documentation generation (used by cmd/gen-docs) |
| `internal/git` | Git operations, worktree management (leaf — stdlib + go-git only, no internal imports) |
| `internal/project` | Project domain layer: owns `registry.yaml` (via `internal/storage`), project identity resolution, registration CRUD, worktree orchestration, runtime health enrichment (`ProjectState`/`ProjectStatus`). Project commands (`internal/cmd/project/*`) are the primary UI — all domain logic (health checks, status) lives here, not in command code. Fully decoupled from `internal/config` |
| `internal/firewall` | Envoy+CoreDNS firewall stack: manager interface, config generators, certificate PKI, daemon lifecycle, rules store |
| `internal/socketbridge` | SSH/GPG agent forwarding via muxrpc over `docker exec` |
| `internal/testenv` | Unified test environment: isolated XDG dirs + optional Config/ProjectManager. Delegates from `config/mocks`, `project/mocks`, `test/e2e/harness` |

**Note:** `hostproxy/internals/` is a structurally-leaf subpackage (stdlib + embed only) that provides container-side scripts and binaries. It is imported by `internal/bundler` for embedding into Docker images, but does NOT import `internal/hostproxy` or any other internal package.

**Note:** `cmd/fawker/` is the demo CLI — faked dependencies, recorded scenarios, no Docker required. Used for visual UAT (`make fawker && ./bin/fawker image build`).

### Presentation Layer

Commands follow a **4-scenario output model** — each command picks the simplest scenario that fits:

| Scenario | Description | Packages | Example |
|----------|-------------|----------|---------|
| Static | Print and done (data, status, results) | `iostreams` + `fmt` | `container ls`, `volume rm` |
| Static-interactive | Static output with y/n prompts mid-flow | `iostreams` + `prompter` | `image prune` |
| Live-display | No user input, continuous rendering with layout | `iostreams` + `tui` | `image build` progress |
| Live-interactive | Full keyboard/mouse input, stateful navigation | `iostreams` + `tui` | `monitor up` |

**Import boundaries** (enforced by tests):
- Only `internal/iostreams` imports `lipgloss`
- Only `internal/tui` imports `bubbletea` and `bubbles`
- Only `internal/term` imports `golang.org/x/term`

**TUI Factory noun**: Commands access TUI via `f.TUI` (`*tui.TUI`). `NewTUI(ios)` is created eagerly in the factory. Commands call `opts.TUI.RunProgress(...)` for multi-step tree displays, registering lifecycle hooks via `opts.TUI.RegisterHooks(...)`.

See `cli-output-style-guide` Serena memory for full scenario details and rendering specs.

### internal/hostproxy - Host Proxy Service

HTTP service mesh mediating container-to-host interactions. See `internal/hostproxy/CLAUDE.md` for detailed architecture diagrams.

**Components:**
- `Server` - HTTP server on localhost (:18374)
- `SessionStore` - Generic session management with TTL
- `CallbackChannel` - OAuth callback interception/forwarding
- `Manager` - Lifecycle management (lazy init via Factory)
- `GitCredential` - HTTPS credential forwarding handler

**Key flows:**
- URL opening: Container → `host-open` script → POST /open/url → host browser
- OAuth: Container detects auth URL → registers callback session → rewrites URL → captures redirect
- Git HTTPS: `git-credential-clawker` → POST /git/credential → host credential store
- SSH/GPG: `socketbridge.Manager` → `docker exec` muxrpc → `clawker-socket-server` → Unix sockets

### internal/cmd/loop/shared - Autonomous Loop Engine

Runs Claude Code in per-iteration Docker containers with stream-json parsing and circuit breaker protection. See `internal/cmd/loop/CLAUDE.md` for implementation details.

**Core types:**
- `Runner` - Main loop orchestrator (per-iteration container lifecycle)
- `CircuitBreaker` - CLOSED/TRIPPED with multiple trip conditions
- `Session` / `SessionStore` - Persistent session state
- `RateLimiter` - Sliding window rate limiting
- `Analyzer` - LOOP_STATUS parser and completion detection
- `StreamHandler` / `ParseStream` - NDJSON stream-json parser for real-time output
- `TextAccumulator` - Aggregates assistant text across stream events
- `ResultEvent` - Cost, tokens, turns from Claude API result

### internal/firewall - Firewall Stack

Envoy+CoreDNS sidecar architecture providing DNS-level blocking and TLS inspection for agent containers.

**Architecture:**
```
Daemon Process          Envoy Container (.2)     CoreDNS Container (.3)
    │                        │                        │
    ├── Health check (5s) ──►│ TCP :18901             │
    ├── Health check (5s) ──────────────────────────►│ HTTP :18902/health
    ├── Container watcher (30s) — exits when no clawker containers running
    │
    ├── ensureConfigs() ────►│ envoy.yaml (bind mount) │ Corefile (bind mount)
    ├── ensureContainer() ──►│ envoyproxy/envoy        │ coredns/coredns
    └── syncProjectRules() — merges required + project rules → regenerate configs
```

**Components:**
- `FirewallManager` interface — 16 methods (lifecycle, rules, container control, bypass, status)
- `Manager` — Docker implementation using whail.Engine
- `Daemon` — detached process with dual-loop (health 5s + watcher 30s), PID file management
- Config generators: `GenerateEnvoyConfig()`, `GenerateCorefile()`
- Certificate PKI: `EnsureCA()`, `GenerateDomainCert()`, ECDSA P256
- Rules store: `storage.Store[EgressRulesFile]` with dedup via `dst:proto:port`

**Network topology:** `clawker-net` Docker bridge with IPAM. Static IPs computed from gateway: Envoy=.2, CoreDNS=.3. Agent containers join network with `--dns` pointing to CoreDNS.

**Integration:** `EnsureDaemon()` called during container creation. Factory exposes `f.Firewall()` lazy noun.

## Command Dependency Injection Pattern

Commands follow the gh CLI's NewCmd/Options/runF pattern. Factory closure fields flow through three steps:

**Step 1**: NewCmd receives Factory, cherry-picks closures into Options:
```go
func NewCmdStop(f *cmdutil.Factory, runF func(context.Context, *StopOptions) error) *cobra.Command {
    opts := &StopOptions{
        IOStreams:     f.IOStreams,     // value field
        Client:       f.Client,        // closure field
        Config:       f.Config,        // closure field
        SocketBridge: f.SocketBridge,  // closure field
    }
```

**Step 2**: Options struct declares only what this command needs:
```go
type StopOptions struct {
    IOStreams     *iostreams.IOStreams
    Client       func(context.Context) (*docker.Client, error)
    Config       func() (config.Config, error)
    SocketBridge func() socketbridge.SocketBridgeManager
    // command-specific fields...
}
```

**Step 3**: Run function receives only Options (never Factory):
```go
func stopRun(opts *StopOptions) error {
    client, err := opts.Client(context.Background())
    // ...
}
```

Factory fields are closures, so `opts.Client = f.Client` assigns the closure value directly — syntactically identical to a bound method reference.

## Command Scaffolding Template

Every command follows this 4-step pattern. No exceptions.

**Step 1 — Options struct** (declares only what this command needs):

```go
type StopOptions struct {
    // From Factory (assigned in constructor)
    IOStreams     *iostreams.IOStreams
    Client       func(context.Context) (*docker.Client, error)
    Config       func() (config.Config, error)
    SocketBridge func() socketbridge.SocketBridgeManager

    // From flags (bound by Cobra)
    Force bool

    // From positional args (assigned in RunE)
    Names []string
}
```

**Step 2 — Constructor** accepts Factory + runF test hook:

```go
func NewCmdStop(f *cmdutil.Factory, runF func(context.Context, *StopOptions) error) *cobra.Command {
    opts := &StopOptions{
        IOStreams:     f.IOStreams,
        Client:       f.Client,
        Config:       f.Config,
        SocketBridge: f.SocketBridge,
    }
```

**Step 3 — RunE** assigns positional args/flags to opts, then dispatches:

```go
    cmd := &cobra.Command{
        Use:   "stop [flags] [NAME...]",
        RunE: func(cmd *cobra.Command, args []string) error {
            opts.Names = args
            if runF != nil {
                return runF(cmd.Context(), opts)
            }
            return stopRun(cmd.Context(), opts)
        },
    }
    cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force stop")
    return cmd
```

**Step 4 — Unexported run function** receives only Options:

```go
func stopRun(ctx context.Context, opts *StopOptions) error {
    client, err := opts.Client(ctx)
    if err != nil {
        return err
    }
    // Business logic using only opts fields
}
```

**Nil-guard for runtime-context deps** (Pattern B — see DESIGN.md §3.4):

```go
func buildRun(ctx context.Context, opts *BuildOptions) error {
    if opts.Builder == nil {
        opts.Builder = build.NewBuilder(/* runtime args */)
    }
    // ...
}
```

**Parent registration** always passes `nil` for runF:

```go
cmd.AddCommand(stop.NewCmdStop(f, nil))
```

## Package Import DAG

Domain packages in `internal/` form a directed acyclic graph with four tiers:

```
┌─────────────────────────────────────────────────────────────────┐
│  LEAF PACKAGES — "Pure Utilities"                               │
│                                                                 │
│  Import: standard library only (or external-only like go-git)   │
│  Imported by: anyone                                            │
│                                                                 │
│  Clawker examples: logger, term, text, signals, monitor, docs, git,│
│                    storage                                         │
└────────────────────────────┬────────────────────────────────────┘
                             │ imported by
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│  FOUNDATION PACKAGES — "Infrastructure"                         │
│                                                                 │
│  Import: leaves only (+ own sub-packages)                       │
│  Imported by: middles, composites, commands                     │
│                                                                 │
│  Universally imported as infrastructure by most of the codebase.│
│  Their imports are leaf-only or type-level declarations.        │
│                                                                 │
│  Clawker examples:                                              │
│    config/ → logger, storage                                    │
│    iostreams/ → logger, term, text                              │
│    cmdutil/ → type-only imports for Factory struct fields +     │
│              output helpers via iostreams                        │
└────────────────────────────┬────────────────────────────────────┘
                             │ imported by
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│  MIDDLE PACKAGES — "Core Domain Services"                       │
│                                                                 │
│  Import: leaves + foundation (+ own sub-packages)               │
│  Imported by: composites, commands                              │
│                                                                 │
│  Clawker examples:                                              │
│    bundler/ → config + own subpackages + hostproxy/internals (embed-only leaf) (no docker) │
│    tui/ → iostreams, text (+ bubbletea, bubbles)                │
│    containerfs/ → keyring, logger (leaf — no docker runtime)    │
│    hostproxy/ → logger                                          │
│    socketbridge/ → config, logger                               │
│    prompter/ → iostreams                                        │
│    project/ → config, storage, iostreams, logger                │
└────────────────────────────┬────────────────────────────────────┘
                             │ imported by
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│  COMPOSITE PACKAGES — "Subsystems"                              │
│                                                                 │
│  Import: leaves + foundation + middles + own sub-packages       │
│  Imported by: commands only                                     │
│                                                                 │
│  Clawker examples:                                              │
│    docker/ → bundler, config, logger, pkg/whail, pkg/whail/buildkit│
│    firewall/ → config, logger, storage, moby/client (daemon exception)│
│    workspace/ → config, docker, logger                          │
│    cmd/loop/shared/ → docker, config, logger                    │
└─────────────────────────────────────────────────────────────────┘
```

### Import Direction Rules

```
  ✓  foundation → leaf             config imports logger
  ✓  middle → leaf                 bundler imports logger
  ✓  middle → foundation           bundler imports config
  ✓  composite → middle            docker imports bundler
  ✓  composite → foundation        docker imports config
  ✓  composite → leaf              loop/shared imports logger

  ✗  leaf → foundation             logger must never import config
  ✗  leaf → leaf (sibling)         leaves have zero internal imports
  ✗  middle ↔ middle (unrelated)   bundler must never import prompter
  ✗  foundation ↔ foundation       config must never import iostreams
  ✗  Any cycle                     A → B → A is always wrong
```

**Lateral imports** between unrelated middle packages are the most common violation. If two middle packages need shared behavior, extract the shared part into a leaf package.

### Test Subpackages

Test doubles follow a `<package>/<package>test/` naming convention. Each provides fakes/mocks/builders for its parent package:

| Subpackage | Provides |
|------------|----------|
| `testenv/` | `New(t, opts...)` → isolated XDG dirs + optional Config/ProjectManager |
| `config/` (stubs.go) | `NewMockConfig()`, `NewFakeConfig()`, `NewConfigFromString()` |
| `docker/dockertest/` | `FakeClient`, test helpers |
| `git/gittest/` | `InMemoryGitManager` |
| `hostproxy/hostproxytest/` | `MockManager` (implements `HostProxyService`) |
| `iostreams` | `Test()` → `(*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer)` |
| `term/mocks/` | `FakeTerm` — stub satisfying `iostreams.term` interface |
| `logger/loggertest/` | `TestLogger` (captures output), `New()`, `NewNop()` |
| `firewall/mocks/` | `FirewallManagerMock` (moq-generated) |
| `socketbridge/socketbridgetest/` | `MockManager` |
| `storage` | `ValidateDirectories()` — XDG directory collision detection |

### Where `cmdutil` Fits

`cmdutil` is a **foundation package** — its high fan-out is structural (DI container type declarations for Factory struct fields), not behavioral. Commands and the entry point import it. It imports config, docker, hostproxy, iostreams, and prompter as type-level declarations for the Factory struct.

If a utility in `cmdutil` is also needed by domain packages outside commands, extract it into a leaf package:

```
BEFORE (leaky):  docker/naming.go ──imports──▶ cmdutil
AFTER  (clean):  docker/naming.go ──imports──▶ naming/ (standalone leaf)
                 cmdutil/resolve.go ──imports──▶ naming/
```

**Rule**: If a helper touches the command framework, it stays in `cmdutil`. If it's a pure data utility, extract it.

## Anti-Patterns

| # | Anti-Pattern | Why It's Wrong |
|---|-------------|----------------|
| 1 | Run function depending on `*Factory` | Breaks interface segregation; use `*Options` only |
| 2 | Calling closure fields during construction | Defeats lazy initialization; closures are evaluated on use |
| 3 | Tests importing `internal/cmd/factory` | Construct minimal `&cmdutil.Factory{}` struct literals instead |
| 4 | Mutating Factory closures at runtime | Closures are set once in the constructor, never reassigned |
| 5 | Adding methods to Factory | Factory is a pure struct; use closure fields for all dependency providers |
| 6 | Skipping `runF` parameter | Every `NewCmd` MUST accept `runF` even if not yet tested |
| 7 | Direct Factory field access in run functions | Extract into Options first; run function never sees Factory |

## Container Naming & Labels

**Container names**: `clawker.project.agent` (3-segment) or `clawker.agent` (2-segment when project is empty)
**Volume names**: `clawker.project.agent-purpose` (purposes: `workspace`, `config`, `history`)

**Labels** (all `dev.clawker.*`):

| Label | Purpose |
|-------|---------|
| `managed` | `true` — authoritative ownership marker |
| `project` | Project name (omitted when project is empty) |
| `agent` | Agent name |
| `version` | Clawker version |
| `image` | Source image reference |
| `workdir` | Host working directory |
| `created` | RFC3339 timestamp |
| `purpose` | Volume purpose (volumes only) |

**Filtering**: `ClawkerFilter()`, `ProjectFilter(project)`, `AgentFilter(project, agent)` generate Docker filter args.

**Strict ownership**: Clawker refuses to operate on resources without `dev.clawker.managed=true`, even with the `clawker.` name prefix.

## Design Principles

1. **All Docker SDK calls go through pkg/whail** - Never bypass this layer
2. **Labels are authoritative** - `dev.clawker.managed=true` determines ownership
3. **Naming is secondary** - `clawker.*` prefix for readability, not filtering
4. **stdout for data, stderr for status** - Enables scripting/composability
5. **User-friendly errors** - All errors include "Next Steps" guidance
6. **Factory DI pattern (gh CLI)** — Pure struct in cmdutil, constructor in cmd/factory, Options in commands
