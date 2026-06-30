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
│  12 command groups, 50+ subcommands (Cobra + Factory DI)              │
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

> **CP ≠ firewall — common LLM confusion.** The "Security Subsystem" column above contains both `controlplane/` (CP daemon — **unconditional**: auth, AdminService, AgentService listener, sqlite-persisted agent registry, CP→clawkerd `agent.Dialer`, pub/sub event topics, mTLS, owns clawker-net) and `controlplane/firewall/` (**one optional subsystem CP manages**, toggled by `firewall.enable` in `settings.yaml`; the project schema's `security.firewall` holds per-project rules only, NOT the master switch). They are not the same. Disabling firewall does NOT disable CP, AdminService, AgentService, agent registry, agent.Dialer→clawkerd Session, ListAgents, or any non-firewall AdminService RPC. CP owns firewall, not vice versa. Don't gate non-firewall behavior on the firewall flag.

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
│  Node tree (yaml.Node) = merge engine + persistence layer (comments ride)│
│  Typed struct *T = immutable snapshot (lock-free Read via atomic.Pointer)│
│  Set(path,value)/Remove(path) graft into the node tree                  │
│  Also: flock locking (optional), atomic I/O (temp+rename)               │
└─────────────────────────────────────────────────────────────────────────┘
```

**Key relationships:**

- Commands never see `storage` — they use `Config` and `ProjectManager` interfaces
- `config` and `project` are thin wrappers — they compose `Store[T]`, provide schemas/filenames/migrations, expose domain APIs
- `storage` is the engine — discovery, load, migrate, merge, provenance, write
- `storage` has zero domain knowledge — it doesn't know about clawker, config files, or registries

### internal/storage - Layered YAML Store Engine

Generic `Store[T]` that handles the full lifecycle of layered YAML configuration. Leaf package — its only `internal/` import is `internal/consts` (itself stdlib-only), for XDG directory resolution and the dotted config-directory name. See `internal/storage/CLAUDE.md` for detailed API reference.

**Node-native architecture:** Every layer and the merged tree are `yaml.Node` trees, so comments ride from load through merge to write. The typed struct `*T` is an immutable snapshot decoded from the merged node and published via `atomic.Pointer` (lock-free `Read`). `map[string]any` survives only as a transient decode view (`LayerInfo.Data`). This avoids the `omitempty` problem — the value handed to `Set` is grafted as-is, so explicit zero values (`false`, `0`, `""`) are preserved.

```
Load:   file/string → layer node ─→ merge nodes → decode → immutable *T

Set:    encode value → graft into merged node at path → mark dirty → re-decode

Write:  dirty paths → route by provenance → graft into target layer node → per-file atomic write
```

**Discovery** (how files are found — two additive modes):

| Mode | Options | Use case |
|------|---------|----------|
| Walk-up | `WithWalkUp(anchorDir)` | Config — CWD up to a caller-supplied anchor (config passes the resolved project root), non-deterministic |
| Static | `WithConfigDir()` / `WithDataDir()` / `WithPaths()` | Registry, settings — known XDG locations |

**Filename-driven:** Store takes ordered filenames on construction (e.g., `"clawker.yaml"`, `"clawker.local.yaml"`). Walk-up is non-deterministic — at each level, checks `.clawker/{filename}` (dir form) first, falls back to `.{filename}` (flat dotfile). Both `.yaml`/`.yml` accepted. Bounded at the anchor directory — never reaches HOME; an empty anchor disables walk-up entirely.

**XDG convenience options:** `WithConfigDir()`, `WithDataDir()`, `WithStateDir()`, `WithCacheDir()` resolve directory paths and add them to the explicit path list. Precedence: `CLAWKER_*_DIR` > `XDG_*_HOME` > default. Explicit paths check `{dir}/{filename}` directly (no dir/flat form).

**Pipeline** (per file, before merge):

1. Read YAML → layer node (`loadNode`, comments intact)
2. Run caller-provided migrations against each layer's own node (precondition-based, idempotent)
3. Atomic re-save of any layer a migration changed

Each file migrates independently — any file at any depth can be independently stale.

**Merge with provenance**: Fold N layer node trees in priority order (closest to CWD = highest). Per-field merge strategy via `merge:"union"|"overwrite"` struct tags on `T`, extracted into a `tagRegistry` at construction. Provenance map tracks which layer won each field — used for auto-scoped writes. Absent keys mean "not set" (not iterated), present keys with zero values mean "explicitly set".

**Write model**: Targeted (`Write(ToPath(p))` / `Write(ToLayer(i))`) or auto-route (`Write()` — provenance resolves each dirty field's target). Each dirty value is grafted into a clone of the target layer's own node tree (preserving its comments), then encoded and atomically written. Node merge preserves unknown keys in the tree that aren't in the struct schema.

**Testing**: `storage.NewFromString[T](yaml)` is the same call as `New` (`New` delegates to it); with no path options it discovers nothing on disk and the seed YAML is the only layer — an in-memory double, parsed through the real schema. Composing packages (`config/mocks`, `project/mocks`) use it to build their test doubles and use real `Store[T]` + `t.TempDir()` for isolated FS harnesses. `Store[T]` has no mock interface; consumer interfaces are the mock boundary.

**Imported by:** `internal/config`, `internal/project`, `internal/state`

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

12 command groups with 50+ subcommands, each in its own subpackage:

| Command Group | Subcommands |
|---------------|-------------|
| `container/` | list, run, start, stop, kill, exec, attach, logs, inspect, cp, pause, unpause, restart, rename, remove, stats, top, update, wait, create |
| `image/` | list, build, inspect, remove, prune |
| `volume/` | list, create, inspect, remove, prune |
| `network/` | list, create, inspect, remove, prune |
| `project/` | init, register, list, info, edit, remove |
| `worktree/` | add, list, prune, remove |
| `auth/` | rotate |
| `firewall/` | (single package — status, list, add, remove, reload, refresh, up, down, enable, disable, bypass, rotate-ca — all route through `f.AdminClient` gRPC to the CP daemon) |
| `controlplane/` | up, down, status, agents (break-glass host-side CP container lifecycle; CP is bootstrapped by the container start path, not transparently via `AdminClient`) |
| `monitor/` | init, up, down, status |
| `settings/` | edit |
| `skill/` | install, show, remove |

**Top-level shortcuts**: `init` → `project init`, plus 20 Docker-style aliases (e.g. `build`, `run`, `start`, `ps`, `rm`, `rmi`, `exec`, `logs`, `stop`, `attach`, …) — see `internal/cmd/root/aliases.go`. Also: `generate`, `version`.

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

0. ProjectRegistry (lazy, `project.NewRegistry()` — sole constructor of registry storage; shared by Config, GitManager, ProjectManager, and commands) → 1. Config (lazy, `config.NewConfig()` via `sync.Once` — settings load + project walk-up anchored at the registry-resolved root) → 2. ProjectManager (lazy, reads Config for the `name:` override + Logger + ProjectRegistry; registry CRUD lives in `internal/project`) → 3. Logger (lazy, reads Config) → 4. HostProxy (lazy, reads Config) → 5. SocketBridge (lazy, reads Config) → 6. IOStreams (eager, `iostreams.System()`) → 7. TUI (eager, wraps IOStreams) → 8. Client (lazy, reads Config) → 9. GitManager (lazy, anchors at the registry-resolved project root — no Config dependency) → 10. Prompter (lazy) → 11. AdminClient (lazy, reads Config) → 12. ControlPlane (lazy, reads Config + Logger + Client) → 13. HttpClient (lazy, stdlib `*http.Client`)

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
| `internal/storage` | `Store[T]` — generic layered YAML store engine: discovery (static/walk-up), load+migrate, merge with provenance, scoped writes, atomic I/O, flock. **Leaf** — only internal import is `internal/consts` (stdlib-only). See `internal/storage/CLAUDE.md` |
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
| `internal/controlplane` | CP daemon orchestrator (`cmd.go`): constructs pub/sub topics + each domain's state repo, wires handlers, resolves cross-domain read-only DI, runs the startup/drain-to-zero sequence. See `controlplane/CLAUDE.md` |
| `controlplane/agent` | Unified CP-side agent surface: `Dialer` (CP→clawkerd outbound mTLS, permissive trust), `Registry` + sqlite writer (identity store), Register handler, `IdentityInterceptor`, `Start` umbrella, `AgentEvent` envelope + state repo. See `controlplane/agent/CLAUDE.md` |
| `controlplane/pubsub` | Generic, stateless pub/sub pipe: `Topic[T]`/`Event[T]`, non-blocking `Publish` with back-pressure, per-subscriber bounded buffer + drop-oldest, panic-recovered delivery. Zero CP-sibling imports — knows only envelopes and subscribers, never domains. See `controlplane/pubsub/CLAUDE.md` |
| `controlplane/dockerevents` | Docker events `Feeder` (reconnecting stream → typed `DockerEvent` on a pub/sub topic), container state repo, managed-label filtering |
| `controlplane/adminclient` | CLI-side `Dial` for AdminService gRPC (mTLS + auto-refreshing OAuth2 bearer token via Hydra) |
| `controlplane/auth` | Typed agent/project identity primitives (`ProjectSlug`, `AgentName`, `AgentFullName`) shared by cert minting and the identity gate |
| `controlplane/manager` | Host-side CP lifecycle: `EnsureRunning`/`Stop`/`CPRunning`, `Manager` interface, embedded clawkercp + ebpf-manager binaries |
| `controlplane/otel` | CP-side `NewOtelLoggerProvider` factory — per-subsystem OTLP log providers over mTLS to the trusted-infra receiver |
| `controlplane/server` | AdminService composition (`NewAdminServer`) + `AuthInterceptor` authz + AgentService listener wiring |
| `controlplane/subprocess` | Ory subprocess lifecycle manager (start, wait-healthy, crash detection, ordered shutdown) |
| `controlplane/firewall` | Firewall domain: `Handler` (13 RPCs), `Stack` (Envoy+CoreDNS container lifecycle), `ActionQueue` (serialized mutation), Envoy/CoreDNS config generators, certificate PKI, rules store, cgroup helpers, drift resolver, rich error types |
| `controlplane/firewall/ebpf` | eBPF loader + `Manager` (cgroup programs, pinned maps); break-glass `ebpf-manager` CLI under `cmd/` |
| `controlplane/firewall/ebpf/netlogger` | Per-decision-point egress event emitter — drains BPF `events_ringbuf`, enriches by `cgroup_id` via pub/sub enrollment events, emits OTLP log records (`service.name=ebpf-egress`) on the trusted infra lane |
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

Envoy + custom CoreDNS + **clawkercp (control plane)** trio providing DNS-level egress blocking, TLS inspection, and per-container cgroup BPF enforcement. Enabled by default (`firewall.enable: true` in `settings.yaml`).

The CP container is the **single owner** of firewall state, eBPF lifetime, and Envoy/CoreDNS lifecycle. There is no host-side firewall daemon and no `internal/firewall/` package. CLI commands speak the 13-method `AdminService` gRPC over mTLS + OAuth2 JWT via `f.AdminClient(ctx)`. See `controlplane/CLAUDE.md` and `controlplane/firewall/CLAUDE.md` for package references.

**Architecture overview:**

```
                  Host CLI                               CP Container (clawkercp, PID 1)
  f.AdminClient(ctx) ──(mTLS + OAuth2 JWT)──► AdminService gRPC
                                                    │
                                                    ▼
                                         firewall.Handler (13 RPCs)
                                                    │
                           ┌────────────────┬───────┴──────┬─────────────┬───────────────────┐
                           ▼                ▼              ▼             ▼                   ▼
                      ebpf.Manager    firewall.Stack   RulesStore    certs.go         netlogger.Service
                      (pinned maps)   (Envoy+CoreDNS)  (egress-rules)                 (ringbuf→OTLP)
                           │                │                                                │
                           │                ▼                                                ▼
                           │         Envoy (.2) + CoreDNS (.3) on clawker-net      clawker-ebpf-egress index
                           ▼                                                       (service.name=ebpf-egress)
                      /sys/fs/bpf/clawker/{container_map, bypass_map, dns_cache,
                                           route_map, metrics_map, events_ringbuf,
                                           events_drops, ratelimit_state, ratelimit_drops}
```

**Host-side bootstrap (`controlplane/manager/bootstrap.go`):**

- `EnsureRunning(ctx, EnsureOpts)` — idempotent, mutex-guarded. Steps: ensure CP image → `ContainerCreate` (static IP via `NetworkingConfig.IPAMConfig.IPv4Address`, `on-failure` restart policy max 3) → `ContainerStart` → poll `/healthz` on `127.0.0.1:<HealthPort>`. Mount-mode reconciliation (stop+remove+recreate) if `FirewallDataSubdir` is RO or mount set diverges (INV-B2-006).
- `Stop(ctx, dc, log)` — stops the CP container; `clawkercp`'s SIGTERM handler drains the firewall stack and flushes per-container eBPF state before exiting, so no orphans remain (INV-B2-008).

`EnsureRunning` is called by `BootstrapServicesPreStart` on the container start/restart path — the CP is NOT bootstrapped transparently by `AdminClient` (which is a pure dial). The break-glass `clawker controlplane up/down/status/agents` verbs expose CP lifecycle directly via `f.ControlPlane()`.

**CP self-shutdown (`controlplane/agent/watcher.go` + the orchestrator's drain callback in `internal/controlplane/cmd.go`):**

`AgentWatcher` polls Docker every 30s for containers with `purpose=agent, managed=true`. After `(missed_threshold=2) × pollInterval + grace=60s` of drain-to-zero, it fires the drain callback. Drain callback ordering (INV-B2-007): `actionQueue.Close` → `grpcServer.GracefulStop` → `handler.CancelAllBypassTimers` → `firewall.Stack.Stop` → `ebpf.Manager.FlushAll`. The CP exits clean (code 0) and the `on-failure` restart policy does NOT retrigger.

Watcher hardening: `ListErrCeiling` bounds Docker-wedged blindness; `started atomic.Bool` enforces at-most-once `Run`; negative options panic instead of snapping to defaults.

**13-method AdminService surface:** See `controlplane/firewall/CLAUDE.md` for the full RPC table. Highlights:

- `FirewallInit` (global) — idempotent stack-up; BPF attach happens at CP startup, not here.
- `FirewallEnable(container_id, config)` (per-container) — INV-B2-016 drift guard: resolves `container_id → cgroup_path` via Docker on every call; warns on stored-vs-fresh `cgroup_id` delta; returns `FailedPrecondition` if container gone.
- `FirewallBypass` dead-man timer goes through the same `resolveBypassCgroupID` resolver so re-enroll on expiry is drift-guarded.
- Per-container requests carry only `container_id` — no `cgroup_path` field on the wire.

**Config generation:** Two pure functions translate egress rules into firewall stack configs (`controlplane/firewall/envoy_config.go`, `coredns_config.go`). `GenerateEnvoyConfig(rules)` produces an Envoy bootstrap YAML with a TLS listener (`:10000`, TLS Inspector) ordered as MITM chains (path rules) → SNI passthrough chains (domain-allow) → default deny, plus sequential TCP listeners (`:10001+`) for non-HTTP protocols. `GenerateCorefile(rules)` produces a CoreDNS Corefile with per-domain forward zones (Cloudflare malware-blocking `1.1.1.2`/`1.0.0.2`), Docker internal zones forwarding to `127.0.0.11`, and a catch-all NXDOMAIN template. **Every forward zone invokes the `dnsbpf` plugin** (between `log` and `forward`) which writes `IP → {domain_hash, TTL}` entries to the pinned BPF `dns_cache` map in real time. Both generators are deterministic.

**Embedded binaries:** Four Linux binaries are cross-compiled and `go:embed`'d into the clawker CLI. The first three need clang + libbpf for BPF byte code and are built inside the pinned multi-stage `Dockerfile.controlplane`; clawkerd is pure Go with no BPF deps and is built via a plain `CGO_ENABLED=0` cross-compile in the Makefile:

- `cmd/clawkercp` → `controlplane/manager/assets/clawkercp` (embedded by `manager/embed_cp.go`). Baked into a content-derived CP image at runtime under the `clawker-controlplane` repo, with a `bin-<short SHA>` tag derived from the embedded `clawkercp` + `ebpf-manager` bytes. The tag changes whenever either embedded binary changes, so `EnsureRunning` ImageInspects the resolved tag as an exact-content cache check. A running container whose `consts.LabelCPBinarySHA` doesn't match the host binary's embedded SHA is force-removed and recreated.
- `controlplane/firewall/ebpf/cmd` → `controlplane/manager/assets/ebpf-manager` (embedded by `manager/embed_ebpf.go`). Break-glass CLI bundled alongside `clawkercp` in the same image.
- `cmd/coredns-clawker` → `controlplane/firewall/assets/coredns-clawker` (embedded by `firewall/embed_coredns.go`). Baked into `clawker-coredns:latest`.
- `cmd/clawkerd` → `clawkerd/embed/assets/clawkerd` (embedded by `clawkerd/embed/embed.go` as `clawkerdembed.Binary`). The bundler streams it into every per-project agent build context (`internal/bundler/dockerfile.go`), so each generated `clawker-<project>:latest` image carries it at `/usr/local/bin/clawkerd`. clawkerd is the container's `ENTRYPOINT` and runs as PID 1 — it supervises the user CMD (forks via `SysProcAttr.Credential` for kernel-side privilege drop, forwards signals to the child pgroup, two-phase Wait4 reaper for orphan drain). See `clawkerd/CLAUDE.md` for the full PID-1 contract. Images are tagged `clawker-<project>:latest` only — there is no SHA-suffixed cache variant. Cache invalidation is delegated to the Docker builder: BuildKit uses its content-addressed layer cache, and the classic builder uses its `probeCache` chain. `clawkerd` is placed as the last `COPY` before `ENTRYPOINT` so a clawkerd binary bump invalidates only that single tail layer; `TestBuildContext_LateClawkerBlock` pins this ordering.

Image builds use `drainBuildStream`/`drainPullStream` helpers that distinguish `io.EOF` from truncated streams and decode `error` / `errorDetail.message` (BuildKit emits the detailed form). See root `CLAUDE.md` "Security → Version Pinning" for the multi-arch manifest rule. BPF toolchain pins live in the Makefile's `BPF_APT_DEPS` variable; see `controlplane/firewall/ebpf/CLAUDE.md` for the bump procedure.

**Global BPF route_map:** BPF `route_key` is `{domain_hash, dst_port}` — **global**, not per-container. Container enforcement is gated on presence in `container_map`. `FirewallSyncRoutes` replaces the global route_map atomically. `ebpf.Manager.Load()` detects pinned maps whose key/value sizes changed (e.g., after a schema change) and removes them before loading new programs.

**Certificate PKI:** Path-based egress rules require TLS interception. `EnsureCA` creates or loads a self-signed ECDSA P-256 CA keypair in `FirewallDataSubdir/certs`. `GenerateDomainCert` signs per-domain certificates for Envoy's MITM termination. `FirewallRotateCA` replaces the CA and re-signs all domain certs. The CA certificate is injected into agent containers at build time so TLS verification succeeds through the proxy.

**Rule persistence:** Active egress rules are stored via `storage.Store[EgressRulesFile]` backed by `egress-rules.yaml` under `FirewallDataSubdir`. Rules are deduped by `dst:proto:port` composite key (`RuleKey`). `cfg.EgressRules()` merges required internal rules (Claude API, Docker registry) with project-specific rules; `BootstrapServicesPreStart` sends the union to `FirewallAddRules`, then `BootstrapServicesPostStart` issues `FirewallEnable` (per-container, after docker start creates the cgroup).

**Network isolation:** The CP creates an isolated Docker bridge network (`clawker-net`) with deterministic static IPs computed from the gateway address — `gateway+EnvoyIPLastOctet` (.2) for Envoy, `gateway+CoreDNSIPLastOctet` (.3) for CoreDNS, `gateway+CPIPLastOctet` (.202) for the CP container. Agent containers join this network with `--dns` pointing to the CoreDNS IP. Static-IP assignment cannot go through whail's `EnsureNetwork` helper (which hard-overwrites `EndpointSettings`) — call `dc.EnsureNetwork` first, then explicit `NetworkingConfig.IPAMConfig.IPv4Address` in `ContainerCreate`.

**Custom CoreDNS container:** Runs with `CAP_BPF + CAP_SYS_ADMIN` and a bind mount of `/sys/fs/bpf` so the `dnsbpf` plugin can open the pinned `dns_cache` map. Image `clawker-coredns:latest` is built from `cmd/coredns-clawker` on demand by `firewall.Stack.ensureCorednsImage`. The stock `coredns/coredns:1.14.2` image is no longer used.

**Integration points:** Commands call `f.AdminClient(ctx)` to obtain an `adminv1.AdminServiceClient`. `BootstrapServicesPreStart` issues `FirewallInit` → `FirewallAddRules`; `BootstrapServicesPostStart` issues `FirewallEnable` (per-container). Break-glass verbs use `f.ControlPlane()` for direct container lifecycle control.

### internal/dnsbpf - CoreDNS Plugin

In-tree CoreDNS plugin that populates the pinned BPF `dns_cache` map in real time. Files: `setup.go` (plugin registration, zone capture, pinned map open), `dnsbpf.go` (`ServeDNS` handler wrapping downstream with `nonwriter`, iterates `dns.A` answers, computes `IPToUint32` + `DomainHash`, writes to the map), `bpfmap.go` (thin `cilium/ebpf` wrapper matching `dns_entry` struct layout), `log.go` (CoreDNS-style logger). The domain hash uses the **Corefile zone name** rather than the response qname, so wildcard zones (`.example.com`) produce a single hash across all subdomains. Imports `controlplane/firewall/ebpf` directly for `IPToUint32`, `DomainHash`, and `Uint32ToIP` helpers — no duplication.

The plugin is consumed exclusively by `cmd/coredns-clawker/main.go`, a custom CoreDNS entrypoint that blank-imports the stock plugins it needs (`forward`, `health`, `log`, `reload`, `template`) plus the dnsbpf plugin, and prepends `"dnsbpf"` to `dnsserver.Directives` so it runs outermost in every server block. The resulting binary is cross-compiled for Linux, embedded via `go:embed` in `controlplane/firewall/embed_coredns.go`, and built on demand into `clawker-coredns:latest` by `firewall.Stack.ensureCorednsImage`.

### controlplane - Clawker Control Plane

Containerized, privileged, long-lived Go service that owns authoritative state for managed containers. Runs `cmd/clawkercp` as PID 1 in the `clawker-controlplane` container. Responsibilities:

1. **Authoritative eBPF management** — owns `ebpf.Manager.Load()` lifetime for the process lifetime; defensive startup cleanup (`CleanupStaleBypass`, INV-B2-013); drain-to-zero flush (`FlushAll`, INV-B2-007).
2. **AdminService gRPC surface** — 13-method scope-corrected firewall surface + `ListAgents`, embedded alongside `*firewall.Handler` in `controlplane.adminServer`. All RPCs require uniform `"admin"` scope (INV-B2-009).
3. **Ory auth stack** — Hydra (OAuth2, `client_credentials` + `private_key_jwt` ES256), Kratos (identity, webui placeholder), Oathkeeper (reverse proxy, webui placeholder). Hydra introspection validates bearer tokens; fail-closed on any error.
4. **Aggregate health** — `/healthz` on `HealthPort` probes all 7 service ports before returning 200.
5. **Agent watcher + self-shutdown** — `AgentWatcher` polls Docker; on drain-to-zero fires the drain callback (queue close → graceful gRPC stop → bypass timer cancel → Stack stop → netlogger stop → DNS GC stop → BPF flush → feeder cancel + pub/sub topic close) and exits cleanly (restart policy `on-failure` does not retrigger).
6. **Pub/sub + bounded-context state** — `controlplane/pubsub` is a dumb, stateless, type-safe pipe (`Topic[T]`/`Event[T]`). CP-internal communication flows through it: dockerevents publishes container lifecycle, the agent package publishes `AgentEvent`s. There is no central worldview — each domain owns and projects into its OWN private state repo; cross-domain reads go through a read-only interface the orchestrator injects.
7. **Agent lifecycle** — `agent.Start` is the single umbrella entry point wiring registry reap, container/destroy eviction, and container/start dial into one bundle.
8. **eBPF egress event emission (`netlogger.Service`)** — drains BPF `events_ringbuf`, enriches by `cgroup_id` via pub/sub `EBPFContainerEnrolled` events, ships OTLP log records (`service.name=ebpf-egress`) over mTLS to the trusted-infra OTLP receiver. Provider built via `otel.NewOtelLoggerProvider`. Records land in the `clawker-ebpf-egress` OpenSearch index. Degraded paths emit `event=netlogger_unavailable` and leave firewall enforcement untouched.

CLI-side dial shape: `controlplane/adminclient.Dial` builds two TLS configs — `tokenTLSCfg` (plain TLS for Hydra token endpoint) + `grpcTLSCfg` (mTLS with CA-signed client cert for AdminService), with auto-refreshing bearer token interceptor. Future agent clients plug in by being registered as additional OAuth2 clients with their own CA-signed certs.

**Pub/sub + bounded-context state.** CP-internal events flow through `controlplane/pubsub`, a dumb generic pipe: `Topic[T]` transports a strongly-typed `Event[T]` envelope and nothing else — it has no notion of state, stores, or any domain. Each domain that needs persistence owns and projects into its OWN private `Store`/`Repository`, mutated only by that domain's subscriber callbacks; there is no central `State` or worldview. One domain exposes to another only a read-only interface it chooses to publish, injected by the orchestrator (`internal/controlplane/cmd.go`) — never direct cross-domain state access. Delivery is resilient by contract: per-subscriber bounded buffers with drop-oldest on overflow, non-blocking `Publish`, and every handler invocation wrapped in `recover` so a single panicking subscriber cannot kill PID 1 and strand pinned eBPF (the CP-crash-is-a-security-incident invariant).

Key packages:

- `internal/controlplane` — the orchestrator `cmd.go`: constructs pub/sub topics, constructs each domain's state repo, wires subscribers, resolves cross-domain read-only DI, runs the ordered startup + drain-to-zero sequence.
- `controlplane/server` — `adminServer` composition (embeds `firewall.Handler` + explicit `ListAgents`) + `AuthInterceptor` authz + AgentService listener wiring. Per-listener `AuthInterceptor` instances (agent listener wired with `agentv1.AgentMethodScopes()` from `api/agent/v1`).
- `controlplane/subprocess` — Ory subprocess lifecycle manager (start, wait-healthy, crash detection, ordered shutdown); `controlplane/auth` — typed identity primitives; `controlplane/otel` — `NewOtelLoggerProvider` factory.
- `controlplane/agent` — **Unified CP-side agent surface.** Consolidates the prior `agentdial/` and `agentregistry/` packages into one package keyed on the agent axis. Contains: `Dialer` (CP→clawkerd outbound mTLS dial with permissive trust — see asymmetric trust in root CLAUDE.md), `Registry` interface + sqlite-backed `NewSQLiteWriter` (persisted identity keyed by SHA-256 cert thumbprint + container_id), `Handler` (AgentService.Register handler with container inspection + peer cert capture), `IdentityInterceptor` (peer-IP-grounded identity gate, applied to every RPC incl. Register), `Start` umbrella (reap + evict subscriber + dial subscriber), and the typed `AgentEvent` envelope (session/exec/registry actions) folded into the package's own state repo. CP is the SOLE sqlite writer — fixes WAL coherence across macOS bind-mount. See `controlplane/agent/CLAUDE.md`.
- `controlplane/pubsub` — **Generic, stateless pub/sub pipe.** `Topic[T]`/`Event[T]`: `Subscribe(func(Event[T]))`, non-blocking `Publish` with back-pressure, per-subscriber bounded buffer + drop-oldest (counted), panic-recovered delivery (one bad subscriber can't kill PID 1 — CP §3.4). Holds no application state and prescribes no state pattern. Zero imports from CP siblings — domains import pubsub, never the reverse. See `controlplane/pubsub/CLAUDE.md`.
- `controlplane/dockerevents` — **Docker events feeder.** `Feeder` subscribes to Docker's event stream with automatic reconnection and publishes `DockerEvent` (wraps `events.Message`) on a `pubsub.Topic`. A subscriber folds container start/stop/destroy + rename into the package's own container state repo. `EventsClient` interface abstracts Docker API for testability. Includes `reconcile` (full container+network sync on reconnect) and managed-label filtering.
- `controlplane/adminclient` — **CLI-side AdminService dial.** `Dial(ctx, adminPort, hydraPort, ...grpc.DialOption)` returns `adminv1.AdminServiceClient`. Handles mTLS + auto-refreshing OAuth2 bearer token via Hydra `client_credentials` grant.
- `controlplane/firewall` — Firewall domain: `Handler` (13 RPCs), `Stack` (Envoy+CoreDNS lifecycle), `ActionQueue` (single-goroutine FIFO serializing all firewall mutations — bringup, teardown, reconcile, enable, disable, bypass), Envoy+CoreDNS config generators, certificate PKI, rules store, cgroup helpers, drift resolver, rich error types with gRPC status integration. See `controlplane/firewall/CLAUDE.md`.
- `controlplane/firewall/ebpf` — BPF loader + manager + bpf2go bindings. See `controlplane/firewall/ebpf/CLAUDE.md`.
- `controlplane/firewall/ebpf/netlogger` — userspace consumer of the BPF `events_ringbuf`. Enriches per-decision records with `{container_id, agent, project, domain}` via pub/sub enrollment events + dockerevents eviction, ships OTLP log records (`service.name=ebpf-egress`) through `otel.NewOtelLoggerProvider` to the trusted-infra OTLP receiver. See `controlplane/firewall/ebpf/netlogger/CLAUDE.md`.
- `controlplane/firewall/ebpf/cmd` — break-glass `ebpf-manager` CLI bundled alongside `clawkercp` in the container image.
- `controlplane/manager` — Host-side CP lifecycle: `EnsureRunning`/`Stop`/`CPRunning`, `BuildCPContainerConfig`, `Manager` interface + `NewManager`, embedded clawkercp + ebpf-manager binaries (`go:embed`). Split out so `cmd/clawkercp` can import the CP packages without dragging in embed directives for its own binary.
- `api/admin/v1/mocks` — moq-generated `AdminServiceClientMock`; `controlplane/auth/mocks` — moq-generated `IntrospectorMock`.
- `clawkerd/embed` — `//go:embed assets/clawkerd` (package `clawkerdembed`) exports the per-container daemon binary as `clawkerdembed.Binary`; bundler drops it into every per-project image at `/usr/local/bin/clawkerd`.
- `clawkerd` — per-container agent daemon (package): mTLS listener on `:7700`, `ClawkerdService.Session` bidi-stream for CP command dispatch, `registerCoordinator` for one-time CP-triggered Register handshake. Boot sequence in `clawkerd/CLAUDE.md`. `cmd/clawkerd` is the thin entrypoint (`os.Exit(clawkerd.Main())`); `Main`/`run` live in `internal/clawkerd`.
- `api/admin/v1` — AdminService proto + method-scope registration (`AdminMethodScopes`, covered by `TestAdminMethodScopes_CoversAllRPCs`).
- `api/agent/v1` — AgentService proto. `Register` RPC for clawkerd→CP identity binding; `AgentMethodScopes()` in `api/agent/v1/agent.go` maps it to `ScopeSelfRegister`.
- `cmd/clawkercp/clawkercp.go` — thin daemon wrapper (`os.Exit(controlplane.Main())`). The real orchestration lives in `internal/controlplane/cmd.go`, which wires the Ory stack + firewall `Handler` + `ActionQueue` + pub/sub topics + dockerevents `Feeder` + `agent.Start` (registry + dialer + evict/dial subscribers) + `AgentWatcher` + drain callback + admin listener + agent listener (with chained Auth + Identity interceptors) + `netlogger.Service` (via `otel.NewOtelLoggerProvider` + `circuitExporter`).

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
│  LEAF PACKAGES — no internal imports (consts exempt: stdlib-only) │
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
│  project → consts, git, logger, storage, text                   │
│  bundler → config + own subpackages                             │
│  tui → iostreams, text                                          │
│  prompter → iostreams                                           │
│  storeui → iostreams, storage, tui                              │
│  dnsbpf → ebpf (CoreDNS plugin, real-time dns_cache writes)     │
│  controlplane/pubsub → logger (dumb typed pipe, zero            │
│                        CP-sibling imports)                       │
│  controlplane/dockerevents → controlplane/pubsub, logger        │
│  controlplane/agent → auth, consts, dockerevents, pubsub,        │
│                       logger, api/agent/v1, api/clawkerd/v1      │
│  controlplane/firewall → config, docker, logger, storage,       │
│                          controlplane/firewall/ebpf (+ embedded │
│                          coredns-clawker binary)                │
│  controlplane/otel → config, logger,                            │
│                 go.opentelemetry.io/otel(/log,/sdk/log,         │
│                 /exporters/otlp/otlplog/otlploggrpc)            │
│  internal/controlplane (orchestrator cmd.go) → config, docker,  │
│                 logger, controlplane/{pubsub,agent,server,      │
│                 dockerevents,firewall,firewall/ebpf,subprocess, │
│                 otel,auth}                                       │
│  controlplane/firewall/ebpf/netlogger → config, logger,         │
│                 controlplane/dockerevents,                      │
│                 controlplane/firewall/ebpf,                     │
│                 go.opentelemetry.io/otel/log,                   │
│                 go.opentelemetry.io/otel/sdk/log                │
│  controlplane/manager → config, docker, logger (+ embedded cp + │
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
│  cmdutil → config, controlplane/manager, controlplane/adminclient,│
│            docker, git, hostproxy, iostreams, logger, project,  │
│            prompter, socketbridge, tui, api/admin/v1            │
│            (mostly type-level imports for Factory struct fields) │
└─────────────────────────────────────────────────────────────────┘
```

### Import Direction Rules

```
  ✓  foundation → leaf             config imports storage
  ✓  domain → leaf                 controlplane/firewall imports storage
  ✓  domain → foundation           bundler imports config
  ✓  composite → domain            docker imports bundler
  ✓  composite → foundation        docker imports config

  ✗  leaf → anything internal      storage must never import config
                                   (internal/consts is exempt: stdlib-only,
                                   foundational vocabulary)
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
| `api/admin/v1/mocks/` | `AdminServiceClientMock` (moq-generated) |
| `controlplane/auth/mocks/` | `IntrospectorMock` (moq-generated) |
| `controlplane/manager/mocks/` | `ManagerMock` (moq-generated) for host-side CP lifecycle noun |
| `controlplane/agent/` (test-only) | `RegistryMock` (moq-generated, lives in package itself to avoid import cycle) |
| `controlplane/firewall/ebpf/mocks/` | `EBPFManagerMock` (moq-generated) |
| `controlplane/firewall/ebpf/netlogger/` (test-only) | In-package seams: `Sink` interface (`recordingSink` for processor tests), `ContainerInspecter` interface (`fakeInspecter`), `readerSource` interface (`fakeRingbuf`); `newTestService` helper wires bus subscriptions without requiring CAP_BPF |
| `hostproxy/hostproxytest/` | `MockHostProxy` |
| `socketbridge/mocks/` | `SocketBridgeManagerMock` (moq-generated) |
| `iostreams` | `Test()` → `(*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer)` |
| `term/mocks/` | `FakeTerm` — stub satisfying `iostreams.term` interface |
| `storage` | `ValidateDirectories()` — XDG directory collision detection |

### Where `cmdutil` Fits

`cmdutil` is a **composite package** by import count — it imports config, controlplane/manager, docker, git, hostproxy, iostreams, logger, project, prompter, socketbridge, tui, and `api/admin/v1`. However, its high fan-out is structural (type declarations for Factory struct fields like `AdminClient func(ctx) (adminv1.AdminServiceClient, error)` and `ControlPlane func() manager.Manager`), not behavioral. It contains no construction logic — that lives in `cmd/factory/`. Commands and the entry point import cmdutil for the Factory type and shared utilities.

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
