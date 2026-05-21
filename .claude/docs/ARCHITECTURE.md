# Clawker Architecture

> High-level architecture overview. Use Serena for detailed method/type exploration.

## Related Docs

- `.claude/docs/DESIGN.md` — behavior and product-level rationale.
- `internal/storage/CLAUDE.md` — storage package API, node tree architecture, merge/write internals.
- `internal/config/CLAUDE.md` — config package API, write semantics, and testing details.

## System Layers

```
┌──────────────────────────────────────────────────────────────────────┐
│  CLI Layer                                                            │
│  cmd/clawker → internal/clawker → internal/cmd/root                   │
│  10 command groups, 50+ subcommands (Cobra + Factory DI)              │
└──────┬────────────────────────┬────────────────────┬─────────────────┘
       │                        │                    │
       ▼                        ▼                    ▼
┌──────────────┐  ┌─────────────────────┐  ┌───────────────────────┐
│ Container    │  │ Configuration       │  │ Security              │
│ Subsystem    │  │ Subsystem           │  │ Subsystem             │
│              │  │                     │  │                       │
│ docker/      │  │ storage/ (engine)   │  │ controlplane/ (CP daemon — Envoy+DNS+BPF) │
│ workspace/   │  │ config/ (project)   │  │ hostproxy/ (auth)     │
│ containerfs/ │  │ config/ (settings)  │  │ socketbridge/ (SSH)   │
│ bundler/     │  │ project/ (registry) │  │ keyring/ (creds)      │
│              │  │ storeui/ (TUI edit) │  │                       │
│ pkg/whail    │  │                     │  │                       │
│ (engine lib) │  │                     │  │                       │
└──────┬───────┘  └─────────────────────┘  └───────────────────────┘
       │
       ▼
  moby/moby (Docker SDK)
```

> **CP ≠ firewall — common LLM confusion.** The "Security Subsystem" column above contains both `controlplane/` (CP daemon — **unconditional**: auth, AdminService, AgentService listener, sqlite-persisted agent registry, CP→clawkerd `agent.Dialer`, overseer event bus, mTLS, owns clawker-net) and `controlplane/firewall/` (**one optional subsystem CP manages**, toggled by `firewall.enable` in `settings.yaml`; the project schema's `security.firewall` holds per-project rules only, NOT the master switch). They are not the same. Disabling firewall does NOT disable CP, AdminService, AgentService, agent registry, agent.Dialer→clawkerd Session, ListAgents, or any non-firewall AdminService RPC. CP owns firewall, not vice versa. Don't gate non-firewall behavior on the firewall flag.

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
- `Project` — project defaults (build, workspace, security, agent). Tiered via walk-up.
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

11 command groups with 50+ subcommands, each in its own subpackage:

| Command Group | Subcommands |
|---------------|-------------|
| `container/` | list, run, start, stop, kill, exec, attach, logs, inspect, cp, pause, unpause, restart, rename, remove, stats, top, update, wait, create |
| `image/` | list, build, inspect, remove, prune |
| `volume/` | list, create, inspect, remove, prune |
| `network/` | list, create, inspect, remove, prune |
| `project/` | init, register, list, info, edit, remove |
| `worktree/` | add, list, prune, remove |
| `firewall/` | (single package — status, list, add, remove, reload, up, down, enable, disable, bypass, rotate-ca — all route through `f.AdminClient` gRPC to the CP daemon) |
| `controlplane/` | up, down, status (break-glass host-side CP container lifecycle; normal CLI paths bring the CP up transparently on first `AdminClient` call) |
| `monitor/` | init, up, down, status |
| `settings/` | edit |
| `skill/` | install, show, remove |

**Top-level shortcuts**: `init` → `project init`, `build` → `image build`, `run`/`start` → `container run`/`start`, `generate`, `version`

**Shared packages**: `container/shared/` and `skill/shared/` contain domain orchestration logic shared across subcommands within their group.

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
| `internal/storage` | `Store[T]` — generic layered YAML store engine: discovery (static/walk-up), load+migrate, merge with provenance, scoped writes, atomic I/O, flock. **Leaf** — zero internal imports. See `internal/storage/CLAUDE.md` |
| `internal/config` | Thin wrapper composing `Store[Project]` + `Store[Settings]`. Exposes `Config` interface with namespaced accessors, path/constant helpers (~40 methods). **Foundation** — imports storage only. See `internal/config/CLAUDE.md` |
| `internal/monitor` | Observability stack templates (OTel Collector, OpenSearch, OpenSearch Dashboards, Prometheus) |
| `internal/logger` | Zerolog setup |
| `internal/cmdutil` | Factory struct (closure fields), error types, format/filter flags, arg validators |
| `internal/cmd/factory` | Factory constructor — wires real dependencies (sync.Once closures) |
| `internal/iostreams` | Testable I/O with TTY detection, colors, progress, pager |
| `internal/prompter` | Interactive prompts (String, Confirm, Select) |
| `internal/tui` | Reusable TUI components (BubbleTea/Lipgloss) - lists, panels, spinners, layouts, tables, field browser, list editor, textarea editor |
| `internal/storeui` | Generic orchestration layer bridging `Store[T]` and TUI field editing: reflection-based field discovery, domain override merging, per-field save with layer targeting. See `internal/storeui/CLAUDE.md` |
| `internal/config/storeui/settings` | Domain adapter for `storeui.Edit[Settings]`: field overrides (labels, descriptions, hidden complex types), layer targets (local/user/original) |
| `internal/config/storeui/project` | Domain adapter for `storeui.Edit[Project]`: field overrides for build/agent/workspace/security sections, layer targets |
| `internal/bundler` | Image building, Dockerfile generation, semver, npm registry client |
| `internal/docs` | CLI documentation generation (used by cmd/gen-docs) |
| `internal/git` | Git operations, worktree management (leaf — stdlib + go-git only, no internal imports) |
| `internal/project` | Project domain layer: owns `registry.yaml` (via `internal/storage`), project identity resolution, registration CRUD, worktree orchestration, runtime health enrichment (`ProjectState`/`ProjectStatus`). Project commands (`internal/cmd/project/*`) are the primary UI — all domain logic (health checks, status) lives here, not in command code. Fully decoupled from `internal/config` |
| `internal/controlplane` | CP daemon core: Ory auth stack, AdminService composition, startup orchestrator, agent watcher, CP container config |
| `internal/controlplane/agent` | Unified CP-side agent surface: `Dialer` (CP→clawkerd outbound mTLS, permissive trust), `Registry` + sqlite writer (identity store), Register handler, `IdentityInterceptor`, `Start` umbrella, session/agent event types. See `internal/controlplane/agent/CLAUDE.md` |
| `internal/controlplane/overseer` | Typed event bus + in-memory `State` worldview: generic pub/sub (`Publish[T]`, `Subscribe[T]`), single-goroutine event loop, `ContainerView` + `Agent` projections. Zero CP-sibling imports. See `internal/controlplane/overseer/CLAUDE.md` |
| `internal/controlplane/dockerevents` | Docker events `Feeder` (reconnecting stream → typed `DockerEvent` on overseer bus), container+network reconcile, managed-label filtering |
| `internal/controlplane/adminclient` | CLI-side `Dial` for AdminService gRPC (mTLS + auto-refreshing OAuth2 bearer token via Hydra) |
| `internal/controlplane/cpboot` | Host-side CP lifecycle: `EnsureRunning`/`Stop`/`CPRunning`, `Manager` interface, embedded clawker-cp + ebpf-manager binaries |
| `internal/controlplane/firewall` | Firewall domain: `Handler` (13 RPCs), `Stack` (Envoy+CoreDNS container lifecycle), `ActionQueue` (serialized mutation), Envoy/CoreDNS config generators, certificate PKI, rules store, cgroup helpers, drift resolver, rich error types |
| `internal/controlplane/firewall/ebpf` | eBPF loader + `Manager` (cgroup programs, pinned maps); break-glass `ebpf-manager` CLI under `cmd/` |
| `internal/socketbridge` | SSH/GPG agent forwarding via muxrpc over `docker exec` |
| `internal/testenv` | Unified test environment: isolated XDG dirs + optional Config/ProjectManager. Delegates from `config/mocks`, `project/mocks`, `test/e2e/harness` |

**Note:** `hostproxy/internals/` is a structurally-leaf subpackage (stdlib + embed only) that provides container-side scripts and binaries. It is imported by `internal/bundler` for embedding into Docker images, but does NOT import `internal/hostproxy` or any other internal package.

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

### Firewall Subsystem (CP-owned)

Envoy + custom CoreDNS + **clawker-cp (control plane)** trio providing DNS-level egress blocking, TLS inspection, and per-container cgroup BPF enforcement. Enabled by default (`firewall.enable: true` in `settings.yaml`).

The CP container is the **single owner** of firewall state, eBPF lifetime, and Envoy/CoreDNS lifecycle. There is no host-side firewall daemon and no `internal/firewall/` package. CLI commands speak the 13-method `AdminService` gRPC over mTLS + OAuth2 JWT via `f.AdminClient(ctx)`. See `internal/controlplane/CLAUDE.md` and `internal/controlplane/firewall/CLAUDE.md` for package references.

**Architecture overview:**

```
                  Host CLI                               CP Container (clawker-cp, PID 1)
  f.AdminClient(ctx) ──(mTLS + OAuth2 JWT)──► AdminService gRPC
                                                    │
                                                    ▼
                                         firewall.Handler (13 RPCs)
                                                    │
                           ┌────────────────┬───────┴──────┬─────────────┐
                           ▼                ▼              ▼             ▼
                      ebpf.Manager    firewall.Stack   RulesStore    certs.go
                      (pinned maps)   (Envoy+CoreDNS)  (egress-rules)
                           │                │
                           │                ▼
                           │         Envoy (.2) + CoreDNS (.3) on clawker-net
                           ▼
                      /sys/fs/bpf/clawker/{container_map,bypass_map,dns_cache,route_map,metrics_map}
```

**Host-side bootstrap (`internal/controlplane/cpboot/bootstrap.go`):**

- `EnsureRunning(ctx, dc, cfg, log)` — idempotent, mutex-guarded. Steps: ensure CP image → `ContainerCreate` (static IP via `NetworkingConfig.IPAMConfig.IPv4Address`, `on-failure` restart policy max 3) → `ContainerStart` → poll `/healthz` on `127.0.0.1:<HealthPort>`. Mount-mode reconciliation (stop+remove+recreate) if `FirewallDataSubdir` is RO or mount set diverges (INV-B2-006).
- `Stop(ctx, dc, log)` — stops the CP container; `clawker-cp`'s SIGTERM handler drains the firewall stack and flushes per-container eBPF state before exiting, so no orphans remain (INV-B2-008).

Consumed via the `ensureRunning` package-level seam by `adminClientFunc`, so the first CLI `AdminClient` call transparently brings the CP up. The break-glass `clawker controlplane up/down/status` verbs expose these directly via `f.ControlPlane()`.

**CP self-shutdown (`internal/controlplane/watcher.go` + drain callback):**

`AgentWatcher` polls Docker every 30s for containers with `purpose=agent, managed=true`. After `(missed_threshold=2) × pollInterval + grace=60s` of drain-to-zero, it fires the drain callback. Drain callback ordering (INV-B2-007, stricter than spec §6 prose): `handler.CancelAllBypassTimers` → `grpcServer.GracefulStop` → `firewall.Stack.Stop` → `ebpf.Manager.FlushAll`. The CP exits clean (code 0) and the `on-failure` restart policy does NOT retrigger.

Watcher hardening: `ListErrCeiling` bounds Docker-wedged blindness; `started atomic.Bool` enforces at-most-once `Run`; negative options panic instead of snapping to defaults.

**13-method AdminService surface:** See `internal/controlplane/firewall/CLAUDE.md` for the full RPC table. Highlights:

- `FirewallInit` (global) — idempotent stack-up; BPF attach happens at CP startup, not here.
- `FirewallEnable(container_id, config)` (per-container) — INV-B2-016 drift guard: resolves `container_id → cgroup_path` via Docker on every call; warns on stored-vs-fresh `cgroup_id` delta; returns `FailedPrecondition` if container gone.
- `FirewallBypass` dead-man timer goes through the same `resolveBypassCgroupID` resolver so re-enroll on expiry is drift-guarded.
- Per-container requests carry only `container_id` — no `cgroup_path` field on the wire.

**Config generation:** Two pure functions translate egress rules into firewall stack configs (`internal/controlplane/firewall/envoy_config.go`, `coredns_config.go`). `GenerateEnvoyConfig(rules)` produces an Envoy bootstrap YAML with a TLS listener (`:10000`, TLS Inspector) ordered as MITM chains (path rules) → SNI passthrough chains (domain-allow) → default deny, plus sequential TCP listeners (`:10001+`) for non-HTTP protocols. `GenerateCorefile(rules)` produces a CoreDNS Corefile with per-domain forward zones (Cloudflare malware-blocking `1.1.1.2`/`1.0.0.2`), Docker internal zones forwarding to `127.0.0.11`, and a catch-all NXDOMAIN template. **Every forward zone invokes the `dnsbpf` plugin** (between `log` and `forward`) which writes `IP → {domain_hash, TTL}` entries to the pinned BPF `dns_cache` map in real time. Both generators are deterministic.

**Embedded binaries:** Four Linux binaries are cross-compiled and `go:embed`'d into the clawker CLI. The first three need clang + libbpf for BPF byte code and are built inside the pinned multi-stage `Dockerfile.controlplane`; clawkerd is pure Go with no BPF deps and is built via a plain `CGO_ENABLED=0` cross-compile in the Makefile:

- `cmd/clawker-cp` → `internal/controlplane/cpboot/assets/clawker-cp` (embedded by `cpboot/embed_cp.go`). Baked into `clawker-controlplane:latest` at runtime.
- `internal/controlplane/firewall/ebpf/cmd` → `internal/controlplane/cpboot/assets/ebpf-manager` (embedded by `cpboot/embed_ebpf.go`). Break-glass CLI bundled alongside `clawker-cp` in the same image.
- `cmd/coredns-clawker` → `internal/controlplane/firewall/assets/coredns-clawker` (embedded by `firewall/embed_coredns.go`). Baked into `clawker-coredns:latest`.
- `cmd/clawkerd` → `internal/clawkerd/assets/clawkerd` (embedded by `internal/clawkerd/embed.go` as `clawkerd.Binary`). The bundler streams it into every per-project agent build context (`internal/bundler/dockerfile.go`), so each generated `clawker-<project>:latest` image carries it at `/usr/local/bin/clawkerd`. clawkerd is the container's `ENTRYPOINT` and runs as PID 1 — it supervises the user CMD (forks via `SysProcAttr.Credential` for kernel-side privilege drop, forwards signals to the child pgroup, two-phase Wait4 reaper for orphan drain). See `cmd/clawkerd/CLAUDE.md` for the full PID-1 contract. Images are tagged `clawker-<project>:latest` only — there is no SHA-suffixed cache variant. Cache invalidation is delegated to the Docker builder: BuildKit uses its content-addressed layer cache, and the classic builder uses its `probeCache` chain. `clawkerd` is placed as the last `COPY` before `ENTRYPOINT` so a clawkerd binary bump invalidates only that single tail layer; `TestBuildContext_LateClawkerBlock` pins this ordering.

Image builds use `drainBuildStream`/`drainPullStream` helpers that distinguish `io.EOF` from truncated streams and decode `error` / `errorDetail.message` (BuildKit emits the detailed form). See root `CLAUDE.md` "Security → Version Pinning" for the multi-arch manifest rule. BPF toolchain pins live in the Makefile's `BPF_APT_DEPS` variable; see `internal/controlplane/firewall/ebpf/CLAUDE.md` for the bump procedure.

**Global BPF route_map:** BPF `route_key` is `{domain_hash, dst_port}` — **global**, not per-container. Container enforcement is gated on presence in `container_map`. `FirewallSyncRoutes` replaces the global route_map atomically. `ebpf.Manager.Load()` detects pinned maps whose key/value sizes changed (e.g., after a schema change) and removes them before loading new programs.

**Certificate PKI:** Path-based egress rules require TLS interception. `EnsureCA` creates or loads a self-signed ECDSA P-256 CA keypair in `FirewallDataSubdir/certs`. `GenerateDomainCert` signs per-domain certificates for Envoy's MITM termination. `FirewallRotateCA` replaces the CA and re-signs all domain certs. The CA certificate is injected into agent containers at build time so TLS verification succeeds through the proxy.

**Rule persistence:** Active egress rules are stored via `storage.Store[EgressRulesFile]` backed by `egress-rules.yaml` under `FirewallDataSubdir`. Rules are deduped by `dst:proto:port` composite key (`RuleKey`). `ProjectRules(cfg)` merges required internal rules (Claude API, Docker registry) with project-specific rules; `BootstrapServicesPostStart` sends the union to `FirewallAddRules` before issuing `FirewallEnable`.

**Network isolation:** The CP creates an isolated Docker bridge network (`clawker-net`) with deterministic static IPs computed from the gateway address — `gateway+EnvoyIPLastOctet` (.2) for Envoy, `gateway+CoreDNSIPLastOctet` (.3) for CoreDNS, `gateway+CPIPLastOctet` (.202) for the CP container. Agent containers join this network with `--dns` pointing to the CoreDNS IP. Static-IP assignment cannot go through whail's `EnsureNetwork` helper (which hard-overwrites `EndpointSettings`) — call `dc.EnsureNetwork` first, then explicit `NetworkingConfig.IPAMConfig.IPv4Address` in `ContainerCreate`.

**Custom CoreDNS container:** Runs with `CAP_BPF + CAP_SYS_ADMIN` and a bind mount of `/sys/fs/bpf` so the `dnsbpf` plugin can open the pinned `dns_cache` map. Image `clawker-coredns:latest` is built from `cmd/coredns-clawker` on demand by `firewall.Stack.ensureCorednsImage`. The stock `coredns/coredns:1.14.2` image is no longer used.

**Integration points:** Commands call `f.AdminClient(ctx)` to obtain an `adminv1.AdminServiceClient`. `BootstrapServicesPostStart` issues 3 RPCs in order (FirewallInit → FirewallAddRules → FirewallEnable). Break-glass verbs use `f.ControlPlane()` for direct container lifecycle control.

### internal/dnsbpf - CoreDNS Plugin

In-tree CoreDNS plugin that populates the pinned BPF `dns_cache` map in real time. Files: `setup.go` (plugin registration, zone capture, pinned map open), `dnsbpf.go` (`ServeDNS` handler wrapping downstream with `nonwriter`, iterates `dns.A` answers, computes `IPToUint32` + `DomainHash`, writes to the map), `bpfmap.go` (thin `cilium/ebpf` wrapper matching `dns_entry` struct layout), `log.go` (CoreDNS-style logger). The domain hash uses the **Corefile zone name** rather than the response qname, so wildcard zones (`.example.com`) produce a single hash across all subdomains. Imports `internal/controlplane/firewall/ebpf` directly for `IPToUint32`, `DomainHash`, and `Uint32ToIP` helpers — no duplication.

The plugin is consumed exclusively by `cmd/coredns-clawker/main.go`, a custom CoreDNS entrypoint that blank-imports the stock plugins it needs (`forward`, `health`, `log`, `reload`, `template`) plus the dnsbpf plugin, and prepends `"dnsbpf"` to `dnsserver.Directives` so it runs outermost in every server block. The resulting binary is cross-compiled for Linux, embedded via `go:embed` in `internal/controlplane/firewall/embed_coredns.go`, and built on demand into `clawker-coredns:latest` by `firewall.Stack.ensureCorednsImage`.

### internal/controlplane - Clawker Control Plane

Containerized, privileged, long-lived Go service that owns authoritative state for managed containers. Runs `cmd/clawker-cp` as PID 1 in the `clawker-controlplane` container. Responsibilities:

1. **Authoritative eBPF management** — owns `ebpf.Manager.Load()` lifetime for the process lifetime; defensive startup cleanup (`CleanupStaleBypass`, INV-B2-013); drain-to-zero flush (`FlushAll`, INV-B2-007).
2. **AdminService gRPC surface** — 13-method scope-corrected firewall surface + `ListAgents`, embedded alongside `*firewall.Handler` in `controlplane.adminServer`. All RPCs require uniform `"admin"` scope (INV-B2-009).
3. **Ory auth stack** — Hydra (OAuth2, `client_credentials` + `private_key_jwt` ES256), Kratos (identity, webui placeholder), Oathkeeper (reverse proxy, webui placeholder). Hydra introspection validates bearer tokens; fail-closed on any error.
4. **Aggregate health** — `/healthz` on `HealthPort` probes all 7 service ports before returning 200.
5. **Agent watcher + self-shutdown** — `AgentWatcher` polls Docker; on drain-to-zero fires the drain callback (queue close → graceful gRPC stop → bypass timer cancel → Stack stop → BPF flush → feeder stop → overseer close) and exits cleanly (restart policy `on-failure` does not retrigger).
6. **Overseer worldview** — `overseer.Overseer` is the typed event bus + in-memory `State` projection. All CP-internal communication flows through it: dockerevents publishes container lifecycle, agent package publishes session/registration/trust events.
7. **Agent lifecycle** — `agent.Start` is the single umbrella entry point wiring registry reap, container/destroy eviction, and container/start dial into one bundle.

CLI-side dial shape: `internal/controlplane/adminclient.Dial` builds two TLS configs — `tokenTLSCfg` (plain TLS for Hydra token endpoint) + `grpcTLSCfg` (mTLS with CA-signed client cert for AdminService), with auto-refreshing bearer token interceptor. Future agent clients plug in by being registered as additional OAuth2 clients with their own CA-signed certs.

Key packages:

- `internal/controlplane` — `adminServer` composition (embeds `firewall.Handler` + explicit `ListAgents`), Ory auth machinery (`authz.go`, `hydra_client.go`, `startup.go`, `ory_configs.go`, `subprocess.go`), `AgentMethodScopes` for the agent listener, `AgentWatcher`. Per-listener `AuthInterceptor` instances.
- `internal/controlplane/agent` — **Unified CP-side agent surface.** Consolidates the prior `agentdial/` and `agentregistry/` packages into one package keyed on the agent axis. Contains: `Dialer` (CP→clawkerd outbound mTLS dial with permissive trust — see asymmetric trust in root CLAUDE.md), `Registry` interface + sqlite-backed `NewSQLiteWriter` (persisted identity keyed by SHA-256 cert thumbprint + container_id), `Handler` (AgentService.Register handler with container inspection + peer cert capture), `IdentityInterceptor` (cert-thumbprint → registry lookup, fail-secure opt-out for Register), `Start` umbrella (reap + evict subscriber + dial subscriber), and typed overseer events (`SessionConnecting/Connected/Failed/Broken`, `AgentRegistered`, `AgentUntrusted`). CP is the SOLE sqlite writer — fixes WAL coherence across macOS bind-mount. See `internal/controlplane/agent/CLAUDE.md`.
- `internal/controlplane/overseer` — **Typed event bus + worldview state.** Generic pub/sub (`Publish[T]`, `Subscribe[T]`, `SubscribeFiltered[T]`) with single-goroutine event loop serializing `State` mutation. `State` holds `Containers` (map of `ContainerView`) and `Agents` (map of `Agent` with session lifecycle + identity + trust verdict). Events implement `applier` interface to mutate state. Deep-copy `Snapshot` for readers. Zero imports from CP siblings — producers import overseer, not reverse. See `internal/controlplane/overseer/CLAUDE.md`.
- `internal/controlplane/dockerevents` — **Docker events feeder.** `Feeder` subscribes to Docker's event stream with automatic reconnection and publishes `DockerEvent` (wraps `events.Message`) to the overseer bus. `DockerEvent.ApplyTo` projects container start/stop/destroy + rename into `State.Containers`. `EventsClient` interface abstracts Docker API for testability. Includes `reconcile` (full container+network sync on reconnect) and managed-label filtering.
- `internal/controlplane/adminclient` — **CLI-side AdminService dial.** `Dial(ctx, adminPort, hydraPort, ...grpc.DialOption)` returns `adminv1.AdminServiceClient`. Handles mTLS + auto-refreshing OAuth2 bearer token via Hydra `client_credentials` grant. Moved from the former `internal/auth/cp_dial.go`.
- `internal/controlplane/firewall` — Firewall domain: `Handler` (13 RPCs), `Stack` (Envoy+CoreDNS lifecycle), `ActionQueue` (single-goroutine FIFO serializing all firewall mutations — bringup, teardown, reconcile, enable, disable, bypass), Envoy+CoreDNS config generators, certificate PKI, rules store, cgroup helpers, drift resolver, rich error types with gRPC status integration. See `internal/controlplane/firewall/CLAUDE.md`.
- `internal/controlplane/firewall/ebpf` — BPF loader + manager + bpf2go bindings. See `internal/controlplane/firewall/ebpf/CLAUDE.md`.
- `internal/controlplane/firewall/ebpf/cmd` — break-glass `ebpf-manager` CLI bundled alongside `clawker-cp` in the container image.
- `internal/controlplane/cpboot` — Host-side CP lifecycle: `EnsureRunning`/`Stop`/`CPRunning`, `BuildCPContainerConfig`, `Manager` interface + `NewManager`, embedded clawker-cp + ebpf-manager binaries (`go:embed`). Split from parent so `cmd/clawker-cp` can import `internal/controlplane` without dragging in embed directives for its own binary.
- `internal/controlplane/mocks` — moq-generated: `IntrospectorMock`, `AdminServiceClientMock`.
- `internal/clawkerd` — `//go:embed assets/clawkerd` exports the per-container daemon binary; bundler drops it into every per-project image at `/usr/local/bin/clawkerd`.
- `cmd/clawkerd` — per-container agent daemon: mTLS listener on `:7700`, `ClawkerdService.Session` bidi-stream for CP command dispatch, `registerCoordinator` for one-time CP-triggered Register handshake. Boot sequence in `cmd/clawkerd/CLAUDE.md`.
- `api/admin/v1` — AdminService proto + method-scope registration (`AdminMethodScopes`, covered by `TestAdminMethodScopes_CoversAllRPCs`).
- `api/agent/v1` — AgentService proto. `Register` RPC for clawkerd→CP identity binding; method-scope map at `internal/controlplane/agent_method_scopes.go` maps it to `ScopeAgentSelfRegister`.
- `cmd/clawker-cp/main.go` — daemon entry point. Wires Ory stack + firewall `Handler` + `ActionQueue` + overseer bus + dockerevents `Feeder` + `agent.Start` (registry + dialer + evict/dial subscribers) + `AgentWatcher` + drain callback + admin listener + agent listener (with chained Auth + Identity interceptors).

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

Domain packages form a directed acyclic graph verified via `goda`. Tiers describe import constraints, not importance.

```
┌─────────────────────────────────────────────────────────────────┐
│  LEAF PACKAGES — zero internal imports                           │
│                                                                 │
│  Import: standard library only (or external-only like go-git)   │
│  Imported by: anyone                                            │
│                                                                 │
│  storage, git, logger, text, term, signals, build, update,      │
│  keyring, pkg/whail                                             │
└────────────────────────────┬────────────────────────────────────┘
                             │ imported by
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│  FOUNDATION PACKAGES — import leaves only                        │
│                                                                 │
│  Universally imported as infrastructure by most of the codebase.│
│                                                                 │
│  config → storage                                               │
│  iostreams → term, text                                         │
│  ebpf → logger (BPF loader, global route_map via SyncRoutes)    │
└────────────────────────────┬────────────────────────────────────┘
                             │ imported by
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│  DOMAIN PACKAGES — import leaves + foundation                    │
│                                                                 │
│  Core business logic. Import leaves and foundation packages.     │
│                                                                 │
│  project → config, git, logger, storage, text                   │
│  bundler → config + own subpackages                             │
│  tui → iostreams, text                                          │
│  prompter → iostreams                                           │
│  storeui → iostreams, storage, tui                              │
│  dnsbpf → ebpf (CoreDNS plugin, real-time dns_cache writes)     │
│  overseer → logger (typed event bus, zero CP-sibling imports)    │
│  dockerevents → overseer, logger (Docker events feeder)          │
│  controlplane/agent → auth, consts, dockerevents, overseer,      │
│                       logger, api/agent/v1, api/clawkerd/v1      │
│  controlplane/firewall → config, docker, logger, storage,       │
│                          controlplane/firewall/ebpf (+ embedded │
│                          coredns-clawker binary)                │
│  controlplane → config, docker, logger, controlplane/firewall,  │
│                 controlplane/firewall/ebpf                      │
│  controlplane/cpboot → config, docker, logger (+ embedded cp +  │
│                        ebpf-manager binaries)                   │
│  controlplane/adminclient → auth, consts, api/admin/v1           │
│  hostproxy → config, logger                                     │
│  socketbridge → config, logger                                  │
│  containerfs → config, keyring, logger                          │
│  monitor → config                                               │
│  docs → config, storage                                         │
└────────────────────────────┬────────────────────────────────────┘
                             │ imported by
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│  COMPOSITE PACKAGES — import domain packages                     │
│                                                                 │
│  docker → bundler, config, build, logger, signals, term,        │
│           pkg/whail, pkg/whail/buildkit                         │
│  workspace → config, docker, logger                             │
│  cmdutil → config, controlplane/cpboot, controlplane/adminclient,│
│            docker, git, hostproxy, iostreams, logger, project,  │
│            prompter, socketbridge, tui, api/admin/v1            │
│            (mostly type-level imports for Factory struct fields) │
└─────────────────────────────────────────────────────────────────┘
```

### Import Direction Rules

```
  ✓  foundation → leaf             config imports storage
  ✓  domain → leaf                 controlplane/firewall imports storage
  ✓  domain → foundation           project imports config
  ✓  composite → domain            docker imports bundler
  ✓  composite → foundation        docker imports config

  ✗  leaf → anything internal      storage must never import config
  ✗  foundation ↔ foundation       config must never import iostreams
  ✗  Any cycle                     A → B → A is always wrong
```

**Lateral imports** between unrelated domain packages are the most common violation. If two domain packages need shared behavior, extract the shared part into a leaf package.

### Test Subpackages

Each package with complex dependencies provides test infrastructure:

| Subpackage | Provides |
|------------|----------|
| `testenv/` | `New(t, opts...)` → isolated XDG dirs + optional Config/ProjectManager; `WriteYAML` |
| `config/mocks/` | `NewBlankConfig()`, `NewFromString(projectYAML, settingsYAML)`, `NewIsolatedTestConfig(t)`, `ConfigMock` (moq) |
| `docker/mocks/` | `FakeClient` (wraps `whailtest.FakeAPIClient`), `SetupXxx` helpers, fixtures, assertions |
| `project/mocks/` | `NewMockProjectManager()`, `NewMockProject(name, repoPath)`, `NewTestProjectManager(t, gitFactory)` |
| `git/gittest/` | `InMemoryGitManager` (memfs-backed, seeded with initial commit) |
| `whail/whailtest/` | `FakeAPIClient` (80+ Fn fields, call recording), build scenarios, `EventRecorder` |
| `controlplane/mocks/` | `IntrospectorMock`, `AdminServiceClientMock` (moq-generated) |
| `controlplane/cpboot/mocks/` | `ManagerMock` (moq-generated) for host-side CP lifecycle noun |
| `controlplane/agent/` (test-only) | `RegistryMock` (moq-generated, lives in package itself to avoid import cycle) |
| `controlplane/firewall/ebpf/mocks/` | `EBPFManagerMock` (moq-generated) |
| `hostproxy/hostproxytest/` | `MockHostProxy` |
| `socketbridge/mocks/` | `MockManager` |
| `iostreams` | `Test()` → `(*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer)` |
| `term/mocks/` | `FakeTerm` — stub satisfying `iostreams.term` interface |
| `storage` | `ValidateDirectories()` — XDG directory collision detection |

### Where `cmdutil` Fits

`cmdutil` is a **composite package** by import count — it imports config, controlplane/cpboot, docker, git, hostproxy, iostreams, logger, project, prompter, socketbridge, tui, and `api/admin/v1`. However, its high fan-out is structural (type declarations for Factory struct fields like `AdminClient func(ctx) (adminv1.AdminServiceClient, error)` and `ControlPlane func() cpboot.Manager`), not behavioral. It contains no construction logic — that lives in `cmd/factory/`. Commands and the entry point import cmdutil for the Factory type and shared utilities.

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

**Container names**: `clawker.project.agent` (3-segment, project-scoped) or `clawker.agent` (2-segment, global-scope agent)
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
