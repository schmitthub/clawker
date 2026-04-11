# Clawker

<critical_instructions>

## MANTRA

This is an in-development alpha project. Features are sometimes made quickly or in haste, architecture and design can change Legacy code that no longer fits the big picture can be encountered often therefore...

* we don't write bad hackish code just to get a single task or feature done. that is a sure fire way to create massive technical debt.
* you must always be thinking about the big picture.
* when we find gaps and bad patterns we pivot and address them before continuing so that the project can scale painlessly
* when planning work, you must always consider the impact on architecture, design, testing, documentation, user and developer experience.
* always think to yourself "will this make future work easier or harder? does this decision serve the entire project as a whole or just my task's?" if harder, rethink your approach.
For example:
* If a component needs a new public method, consider if it belongs in the package's public API at all. Should it be private? Should it be in another package?
* If an internal package is missing a test subpackage, an interface, a mock implementation, or a fake for testing, add it to fit our standard patterns and factory DI pattern so that every other caller can benefit.

Finding and fixing greater issues is fun, its more important than the task at hand. You love to pivot on fixing technical debt and improving architecture. You are a software craftsman.

**Your success depends on this mantra**

## Required Tooling

### MUST USE

1. **Serena** - Code exploration, symbol search, semantic editing:
   * `initial_instructions` → `check_onboarding_performed` → `list_memories`
   * `search_for_pattern`,`find_symbol`,`get_symbols_overview`,`find_referencing_symbols` for navigation
   * `think_about_collected_information` after research
   * `think_about_task_adherence` before changes
   * `replace_symbol_body`, `insert_after_symbol`,`insert_before_symbol`,`rename_symbol` for edits
   * `think_about_whether_you_are_done` after task
   * `write_memory`, `edit_memory`, `delete_memory` to update memories with current status before completion

2. **deepwiki** - Always use deepwiki MCP for documentation about GitHub repositories and open source software configurations, functionality, features, code architecture, infrastructure, and code design without the user having to ask for it. If you can't find an answer use context7. If that fails then use default tools. Use the following commands:
   * read_wiki_structure - Get a list of documentation topics for a GitHub repository
   * read_wiki_contents - View documentation about a GitHub repository
   * ask_question - Ask any question about a GitHub repository and get an AI-powered, context-grounded response

3. **Context7** - When I need library/API documentation, code generation, setup or configuration steps without me having to explicitly ask.
   * `resolve-library-id` first, then `get-library-docs`
   * For: Docker SDK, spf13/cobra, spf13/viper, rs/zerolog, gopkg.in/yaml.v3

4. **github mcp** - Use github's mcp for repository-specific information like PR status, issues, code search, and commit history. Use the following commands:

### Workflow Requirements

**Planning**: You MUST adhere to design in `.claude/docs/DESIGN.md` and architecture in `.claude/docs/ARCHITECTURE.md` when planning work. If changes are needed, update those docs accordingly.
**Testing**: You MUST adhere to TDD, writing tests before code changes. All tests must pass before considering a change complete. If a fixture, golden file, interface, mock, fake, or test helper is needed, add it.
If an integration test is required, add it to the appropriate `test/*/` subpackage. If a new test suite is needed, create it under `test/`.
**Documentation**: You MUST update README.md, */**CLAUDE.md, and relevant memories after
It does not matter if the work has to be done in an out-of-scope dependency, it is for the greater good of the project which matters more than anything else.

</critical_instructions>

## Repository Structure

```
├── cmd/
│   ├── clawker/               # Main CLI binary
│   ├── clawker-generate/      # Code generation helper
│   ├── coredns-clawker/       # Custom CoreDNS build embedding the dnsbpf plugin (Linux; embedded via go:embed into internal/firewall)
│   └── gen-docs/              # CLI doc generator (man/markdown/rst/yaml)
├── internal/
│   ├── build/                 # Build-time metadata (version, date) — leaf, stdlib only
│   ├── bundler/               # Dockerfile generation, content hashing, semver, npm registry (leaf — no docker import)
│   ├── clawker/               # Main application lifecycle
│   ├── cmd/                   # Cobra commands (container/, volume/, network/, image/, version/, loop/, worktree/, firewall/, root/)
│   │   ├── factory/           # Factory constructor — wires real dependencies
│   │   ├── settings/          # Settings parent command + edit subcommand
│   │   ├── skill/             # Skill plugin management (install/show/remove) — wraps claude CLI
│   │   └── project/edit/      # Project edit subcommand
│   ├── cmdutil/               # Factory struct, error types, arg validators (lightweight)
│   ├── config/                # Storage.Store[T] config engine: schema types, multi-file loading, constants (see internal/config/CLAUDE.md)
│   │   └── storeui/           # Domain adapters for storeui: settings/, project/
│   ├── containerfs/           # Host Claude config preparation for container init
│   ├── dnsbpf/                # CoreDNS plugin: writes DNS A/AAAA resolutions to the BPF dns_cache map in real time (used by cmd/coredns-clawker)
│   ├── docker/                # Clawker Docker middleware, image building (wraps pkg/whail + bundler)
│   │   └── mocks/             # FakeClient, test helpers, moby mock transport
│   ├── docs/                  # CLI doc generation (man, markdown, rst, yaml)
│   ├── ebpf/                  # eBPF cgroup programs + Go manager (clawker.c compiled via bpf2go); `cmd/` host-side subcommand invoked by firewall manager (init, sync-routes, enable, disable)
│   ├── firewall/              # Envoy+CoreDNS firewall stack: manager interface, config generators, certs, daemon, rules store; embeds pre-built ebpf-manager and coredns-clawker binaries (ebpf_embed.go, coredns_embed.go)
│   │   └── mocks/             # FirewallManagerMock (moq-generated)
│   ├── git/                   # Git operations, worktree management (leaf — no internal imports, uses go-git)
│   │   └── gittest/           # InMemoryGitManager for testing
│   ├── hostproxy/             # Host proxy for container-to-host communication
│   │   ├── hostproxytest/     # MockHostProxy for integration tests
│   │   └── internals/         # Container-side hostproxy client scripts
│   ├── iostreams/             # I/O streams, colors, styles, spinners, progress, layout
│   ├── keyring/               # Keyring service for credential storage
│   ├── logger/                # Struct-based zerolog (file + optional OTEL bridge); Factory noun
│   ├── monitor/               # Monitoring stack templates (Grafana, Prometheus, Loki)
│   ├── project/               # Project registration in user registry
│   ├── prompter/              # Interactive prompts (String, Confirm, Select)
│   ├── signals/               # OS signal utilities (leaf — stdlib only)
│   ├── socketbridge/          # SSH/GPG agent forwarding via muxrpc over docker exec
│   │   └── socketbridgetest/  # MockManager for testing
│   ├── storage/               # Multi-file YAML store: discovery, merge, provenance-aware write, dir validation
│   ├── storeui/               # Generic TUI for browsing/editing Store[T] instances (bridges storage + tui)
│   ├── term/                  # Terminal capabilities + raw mode (leaf — sole x/term gateway)
│   │   └── mocks/             # FakeTerm stub (satisfies iostreams.term interface)
│   ├── testenv/               # Unified test environment: isolated dirs, config, project manager (test-only)
│   ├── text/                  # Pure text utilities (leaf — stdlib only)
│   ├── tui/                   # Interactive TUI layer: BubbleTea models, viewports, panels (imports iostreams for styles)
│   ├── update/                # Background update checker — GitHub releases API, 24h cached notifications (foundation — no internal imports)
│   └── workspace/             # Bind vs Snapshot strategies
├── pkg/
│   └── whail/                 # Reusable Docker engine with label-based isolation
│       └── buildkit/          # BuildKit client (moby/buildkit) — isolated heavy deps
├── test/
│   ├── e2e/                   # E2E integration tests (firewall, mounts, migrations, presets)
│   │   └── harness/           # CLI test harness (delegates dirs to testenv, adds chdir + Factory + Run)
│   └── whail/                 # Whail BuildKit integration tests (Docker + BuildKit)
├── scripts/
│   ├── install.sh                 # curl|bash installer (downloads pre-built binary from GitHub releases)
│   ├── install-hooks.sh           # Install pre-commit hooks
│   ├── check-claude-freshness.sh  # CLAUDE.md staleness checker
│   ├── clawker-leak-monitor.sh    # Docker resource leak monitor
│   ├── gen-dep-graphs.sh          # Dependency graph generator
│   ├── gen-notice.sh              # Third-party notice generator
│   └── localenv-dotenv.sh         # Local env .env updater (used by `make localenv`)
└── templates/                 # clawker.yaml scaffolding
```

## Build Commands

```bash
# Install via Homebrew
brew install schmitthub/tap/clawker

# Install pre-built binary (no Go required)
curl -fsSL https://raw.githubusercontent.com/schmitthub/clawker/main/scripts/install.sh | bash
bash scripts/install.sh --version v0.1.3 --dir $HOME/.local/bin  # Pin version + custom dir

go build -o bin/clawker ./cmd/clawker  # Build CLI
make test                                 # Unit tests (no Docker, excludes test/e2e,whail)
./bin/clawker --debug run @              # Debug logging
go run ./cmd/gen-docs --doc-path docs --markdown            # Regenerate CLI docs
go run ./cmd/gen-docs --doc-path docs --markdown --website   # Regenerate CLI docs for Mintlify (MDX-safe + frontmatter)
npx mintlify dev --docs-directory docs                       # Local Mintlify preview (http://localhost:3000)

# Golden file tests
GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeedRecordedScenarios -v  # Regenerate JSON testdata

# Docker-required tests (directory separation, no build tags)
go test ./test/e2e/... -v -timeout 10m           # E2E integration tests
go test ./test/whail/... -v -timeout 5m          # Whail BuildKit integration tests

# Pre-commit hooks (mirrors CI quality gates locally)
bash scripts/install-hooks.sh          # Install pre-commit hooks (run once after clone)
make pre-commit                        # Run all hooks against entire repo
pre-commit run gitleaks --all-files    # Run a single hook

# Semgrep version: 1.146.0
# Pre-commit uses system semgrep with --baseline-commit HEAD (diff-only scanning).
# CI uses semgrep/semgrep:1.146.0 Docker image.
# When upgrading, update both .pre-commit-config.yaml comment AND security.yml image tag.
```

## Key Concepts

| Abstraction | Purpose |
|-------------|---------|
| `Factory` | Slim DI struct with eager IO/TUI/version fields and lazy noun closures (`Config`, `Logger`, `Client`, `ProjectManager`, `GitManager`, etc.); constructor in `internal/cmd/factory` |
| `git.GitManager` | Git repository operations, worktree management (leaf package, no internal imports) |
| `docker.Client` | Clawker middleware wrapping `whail.Engine` with labels/naming. `cfg config.Config` (interface) provides all label keys. `NewClient(ctx, cfg, opts...)` (production), `NewClientFromEngine(engine, cfg)` (tests) |
| `whail.Engine` | Reusable Docker engine with label-based resource isolation |
| `WorkspaceStrategy` | Bind (live mount) vs Snapshot (ephemeral copy) |
| `PTYHandler` | Raw terminal mode, bidirectional streaming (in `docker` package) |
| `ContainerConfig` | Labels, naming (`clawker.project.agent`), volumes |
| `CreateContainer()` | Single entry point for container creation (workspace, config, env, create, inject); shared by `run` and `create` via events channel for progress |
| `IsOutsideHome(dir)` | Pure bool function in `container/shared/safety.go` — returns true when dir is `$HOME` or not within `$HOME`. Used by `run`/`create` (prompt) and `loop` (hard error) |
| `CreateContainerConfig` / `CreateContainerResult` | Input/output types for `CreateContainer()` — all deps and runtime values |
| `CreateContainerEvent` | Channel event: Step, Status (`StepRunning`/`StepComplete`/`StepCached`), Type (`MessageInfo`/`MessageWarning`), Message |
| `clawker-share` | Optional read-only bind mount from `cfg.ShareSubdir()` into containers at `~/.clawker-share` when `agent.enable_shared_dir: true`; host dir created during `clawker project init`, re-created if missing during mount setup |
| `containerfs` | Host Claude config preparation for container init: copies settings, plugins (incl. cache), credentials to config volume; rewrites host paths in plugin JSON files; prepares post-init script tar |
| `ConfigVolumeResult` | Bool flags tracking which config volumes were freshly created (`ConfigCreated`, `HistoryCreated`) — returned by `workspace.EnsureConfigVolumes` |
| `InitConfigOpts` | Options for `shared.InitContainerConfig` — project/agent names, container work dir, ClaudeCodeConfig, CopyToVolumeFn (DI) |
| `InjectPostInitOpts` | Options for `shared.InjectPostInitScript` — container ID, script content, CopyToContainerFn (DI) |
| `firewall.FirewallManager` | Interface for Envoy+CoreDNS firewall stack (15 methods: lifecycle, rules, container control, bypass, status); mock: `firewall/mocks/FirewallManagerMock` |
| `firewall.Daemon` | Detached firewall process with dual-loop (health 5s + container watcher 30s), PID file management. `EnsureDaemon()` called during container creation |
| `firewall.ProjectRules()` | Builds complete rule set from project config (security.firewall rules + required internal rules like Claude API, Docker registry) |
| `firewall.embeddedImageSpec` / `ensureEmbeddedImage` | Unified pattern for building Docker images from embedded Linux binaries on-demand. Drives both the eBPF manager (`ebpf_embed.go`) and the custom CoreDNS build (`coredns_embed.go`) |
| `firewall.syncRoutes` | Manager helper that invokes the ebpf-manager `sync-routes` subcommand to repopulate the global BPF route_map from current rules. Called on `EnsureRunning`, `regenerateAndRestart`, and container enable |
| `dnsbpf.Handler` | CoreDNS plugin (`internal/dnsbpf`) that intercepts DNS responses and writes IP → {domain_hash, TTL} entries to the BPF dns_cache map. Registered as `dnsbpf` directive in `cmd/coredns-clawker` |
| `ebpf.Manager` | Host-side Go manager for clawker cgroup/sock programs (compiled via bpf2go). Its `cmd/` subcommand (init, sync-routes, enable, disable) is embedded as a Linux binary and invoked by the firewall manager |
| `shared.CommandOpts` | DI container for container start orchestration — function closures: Client, Config, ProjectManager, HostProxy, Firewall, SocketBridge, Logger |
| `shared.ContainerStart()` | Three-phase container start: `BootstrapServicesPreStart` → docker start → `BootstrapServicesPostStart`. Used by `run` and `start` |
| `firewall.Manager` | Docker implementation of `FirewallManager` — manages Envoy/CoreDNS containers, config generation, certificate PKI, rule persistence |
| `hostproxy.HostProxyService` | Interface for host proxy operations (EnsureRunning, IsRunning, ProxyURL); mock: `hostproxytest.MockManager` |
| `hostproxy.Manager` | Concrete host proxy daemon manager (spawns subprocess); implements `HostProxyService` |
| `socketbridge.SocketBridgeManager` | Interface for socket bridge operations; mock: `sockebridgemocks.MockManager` |
| `socketbridge.Manager` | Per-container SSH/GPG agent bridge daemon (muxrpc over docker exec) |
| `iostreams.IOStreams` | I/O streams, TTY detection, colors, styles, spinners, progress, layout |
| `logger.Logger` | Struct-based zerolog wrapper with file rotation + optional OTEL bridge. Factory lazy noun (`f.Logger`). Commands capture on Options, library packages accept in constructors. Tests use `logger.Nop()` |
| `iostreams.ColorScheme` | Color palette + semantic colors + icons; canonical style source for all clawker output |
| `iostreams.SpinnerFrame` | Pure spinner rendering function used by the iostreams goroutine spinner |
| `text.*` | Pure ANSI-aware text utilities (leaf package): Truncate, PadRight, CountVisibleWidth, StripANSI, etc. |
| `tui.TablePrinter` | Table output: `bubbles/table` styled mode + tabwriter plain mode; content-aware column widths |
| `cmdutil.FlagError` | Error type triggering usage display in Main()'s centralized `printError` |
| `cmdutil.SilentError` | Sentinel error: already displayed, Main() just exits non-zero |
| `cmdutil.FormatFlags` | Reusable `--format`/`--json`/`--quiet` flag handling for list commands; populated during PreRunE. Convenience delegates (`IsJSON()`, `IsTemplate()`, etc.) avoid `Format.Format.` stutter. `ToAny[T any]` generic for template slice conversion |
| `cmdutil.FilterFlags` | Reusable `--filter key=value` flag handling; per-command key validation via `ValidateFilterKeys` |
| `cmdutil.WriteJSON` | Pretty-printed JSON output for `--json`/`--format json` modes; HTML escaping disabled (replaces deprecated `OutputJSON`) |
| `tui.TUI` | Factory noun for presentation layer; owns hooks + delegates to RunProgress. Commands capture `*TUI` eagerly, hooks registered later via `RegisterHooks()` |
| `tui.RunProgress` | Generic progress display: BubbleTea TTY mode (sliding window) + plain text; domain-agnostic via callbacks |
| `tui.ProgressStep` | Channel event type for progress display (ID, Name, Status, LogLine, Cached, Error) |
| `tui.ProgressDisplayConfig` | Configuration with CompletionVerb and callback functions: IsInternal, CleanName, ParseGroup, FormatDuration, OnLifecycle |
| `tui.LifecycleHook` | Generic hook function type for TUI lifecycle events; threaded via config structs, nil = no-op |
| `tui.HookResult` | Hook return type: Continue (bool), Message (string), Err (error) — controls post-hook execution flow |
| `whail.BuildProgressFunc` | Callback type threading build progress events through the options chain |
| `tui.RunProgram` | Launches BubbleTea programs wired to IOStreams (input/output) |
| `tui.PanelModel` | Bordered panel with focus; `PanelGroup` manages multi-panel layouts |
| `tui.ListModel` | Selectable list with scrolling; `ListItem` interface |
| `tui.ViewportModel` | Scrollable content wrapping bubbles/viewport |
| `tui.WizardField / WizardResult` | Multi-step wizard: field definitions + collected values + submit/cancel; `TUI.RunWizard` |
| `tui.SelectField / TextField / ConfirmField` | Standalone BubbleTea field models for forms; value semantics |
| `tui.RenderStepperBar` | Pure render function for horizontal step progress (icons: checkmark, filled circle, empty circle) |
| `prompter.Prompter` | Interactive prompts with TTY/CI awareness |
| `BuildKitImageBuilder` | Closure field on `whail.Engine` — label enforcement + delegation to `buildkit/` subpackage |
| `update.CheckForUpdate` | Background GitHub release check — 24h cached, suppressed in CI/DEV; wired into `Main()` via goroutine + channel |
| `update.CheckResult` | Returned when newer version available: `CurrentVersion`, `LatestVersion`, `ReleaseURL` |
| `storage.Schema` | Interface: `Fields() FieldSet`. Implemented by all `Store[T]` types (`Project`, `Settings`, `EgressRulesFile`, `ProjectRegistry`). Compile-time enforced via `Store[T Schema]` constraint. Default values come from `default` struct tags; `GenerateDefaultsYAML[T]()` produces YAML from them |
| `storage.Field` / `storage.FieldSet` | Interfaces describing configuration field metadata (Path, Kind, Label, Description, Default, Required). `NormalizeFields[T]()` reads struct tags (`yaml`, `label`, `desc`, `default`, `required`) and produces a `FieldSet` |
| `storeui.Edit[T]` | Generic orchestrator for browsing/editing `Store[T Schema]` — a **config placement tool** (not an override editor). Browser shows merged state across all layers; edits target specific layer files. Same key across layers is inheritance, not duplication. Validation guards writes (per-layer), not editors. Schema metadata from struct tags via `enrichWithSchema`; domain adapters provide TUI-specific overrides only (Hidden, ReadOnly, Kind, Options) |
| `storeui.WalkFields` | Reflection-based struct walker: discovers all exported fields → `[]Field` with dotted YAML paths, type-mapped `FieldKind`, current values. Schema metadata enriched by `enrichWithSchema` |
| `storeui.SetFieldValue` | Reverse reflection writer: sets struct field by dotted YAML path with type-aware parsing (bool, int, duration, `[]string`, `*bool`) |
| `storeui.ApplyOverrides` | Merges domain `[]Override` onto schema-enriched fields: hidden, read-only, select options, sort order. Labels/descriptions now come from struct tags, not overrides |
| `storeui.LayerTarget` | Save destination for per-field writes: Label, Description (shortened path), Path (absolute). Domain adapters build these from `store.Layers()` |
| `tui.FieldBrowserModel` | Generic tabbed field browser/editor. Domain-agnostic — knows nothing about stores, reflection, or config schemas. Used by `storeui` to provide interactive editing for any `Store[T]`. States: Browse → Edit → PickLayer |
| `tui.ListEditorModel` | Generic string list editor with add/edit/delete: parses comma-separated input into items, returns comma-separated output |
| `tui.TextareaEditorModel` | Multiline text editor wrapping `bubbles/textarea`: Ctrl+S to save, Esc to cancel |
| `storage.Provenance` | `Store[T].Provenance(path) (LayerInfo, bool)` — returns which layer won a specific dotted field path |
| `storage.ProvenanceMap` | `Store[T].ProvenanceMap() map[string]string` — all dotted paths → source file paths |
| `storage.Write(ToPath)` | `Store[T].Write(ToPath(path)) error` — persist to explicit absolute path, bypassing provenance routing |
| `Package DAG` | leaf → middle → composite import hierarchy (see ARCHITECTURE.md) |
| `ProjectRegistry` | Persistent slug→path map (`cfg.ProjectRegistryFileName()`); CRUD/orchestration is owned by `internal/project` |
| `project.ProjectManager` | Project-layer domain API: registration, resolution, worktree lifecycle. Constructor: `NewProjectManager(cfg, gitFactory)`. `ListWorktrees(ctx)` aggregates across all projects; `Project.ListWorktrees(ctx)` returns enriched state for one project |
| `config.Config` | Configuration and path-resolution contract. Owns config file I/O and path helpers (`GetProjectRoot`, `GetProjectIgnoreFile`, `ConfigDir`, `Write`). It does not own project CRUD/worktree lifecycle orchestration |
| `build.Version` / `build.Date` | Build-time metadata injected via ldflags; `DEV` default with `debug.ReadBuildInfo` fallback |
| `WorktreeStatus` | String enum for worktree health: `WorktreeHealthy`, `WorktreeRegistryOnly`, `WorktreeGitOnly`, `WorktreeBroken`, `WorktreeDotGitMissing`, `WorktreeGitMetadataMissing` |
| `storage.ValidateDirectories` | Resolves all 4 XDG dirs (config/data/state/cache) and returns error if any pair collides — catches env var misconfiguration |
| `testenv.Env` | Unified test environment: `New(t)` creates isolated dirs + env vars; `WithConfig()` adds config; `WithProjectManager(gf)` adds PM. Accessors: `Dirs`, `Config()`, `ProjectManager()` |

Package-specific CLAUDE.md files in `internal/*/CLAUDE.md` provide detailed API references.

## CLI Commands

See `.claude/docs/CLI-VERBS.md` for complete command reference.

**Top-level shortcuts**: `init`, `build`, `run`, `start`, `monitor *`, `generate`, `loop iterate/tasks/status/reset`, `version`

**Management commands**: `container *`, `volume *`, `network *`, `image *`, `project *` (incl. `project register`, `project edit`), `worktree *`, `firewall *` (status/list/add/remove/reload/up/down/enable/disable/bypass/rotate-ca), `settings *` (`settings edit`), `skill *` (install/show/remove)

Commands use positional arguments for resource names (e.g., `clawker container stop clawker.myapp.dev`) matching Docker's interface.

## Configuration

> **For code**: Always use `Config` interface accessors (`cfg.ProjectConfigFileName()`, `cfg.SettingsFileName()`, `cfg.ProjectRegistryFileName()`, `cfg.ConfigDirEnvVar()`, `cfg.DataDirEnvVar()`, `cfg.StateDirEnvVar()`) — never hardcode filenames, paths, or env var names. See `internal/config/CLAUDE.md` for full accessor list.

### User Settings (`cfg.SettingsFileName()` → settings.yaml)

```yaml
logging:
  file_enabled: true
  max_size_mb: 50
```

### Project Registry (`cfg.ProjectRegistryFileName()` → projects.yaml)

```yaml
projects:
  my-app:
    name: "my-app"
    root: "/Users/dev/my-app"
```

Managed by `clawker project init` and `clawker project register`. The registry maps project slugs to filesystem paths.

### Project Config (`cfg.ProjectConfigFileName()` → clawker.yaml)

```yaml
build:
  image: "buildpack-deps:bookworm-scm"
  packages: ["git", "ripgrep"]
  instructions: { env: {}, copy: [], root_run: [], user_run: [] }
  inject: { after_from: [], after_packages: [] }
agent: { env_file: [], from_env: [], env: {}, post_init: "" }
workspace: { default_mode: "bind" }
security: { firewall: { enable: true }, docker_socket: false, git_credentials: { forward_https: true, forward_ssh: true, forward_gpg: true, copy_git_config: true } }
loop: { max_loops: 50, stagnation_threshold: 3, timeout_minutes: 15, skip_permissions: false, hooks_file: "", append_system_prompt: "" }
```

## Design Decisions

1. Firewall enabled, Docker socket disabled by default
2. `run`/`start` are aliases for `container run` (Docker CLI pattern)
3. Hierarchical naming: `clawker.project.agent`; labels (`dev.clawker.*`) authoritative for filtering
4. stdout for user info (data, status messages, success confirmations, next steps), stderr for warnings/errors only; `--format` flag for machine-readable output; per-scenario stream strategy (see style guide)
5. Project registry replaces directory walking for resolution
6. Empty project → 2-segment names (`clawker.agent`), labels omit `dev.clawker.project`
7. Factory is a pure struct with closure fields; constructor in `internal/cmd/factory/`. Commands receive function references on Options structs, follow NewCmd(f, runF) pattern
8. Factory noun principle: each Factory field returns a noun (thing), not a verb (action). Commands call methods on the returned noun (e.g., `f.HostProxy().EnsureRunning()` not `f.EnsureHostProxy()`)
9. Presentation layer 4-scenario model: (1) static output = `iostreams` only, (2) static-interactive = `iostreams` + `prompter`, (3) live-display = `iostreams` + `tui`, (4) live-interactive = `iostreams` + `tui`. A command may import both `iostreams` and `tui`. Commands access TUI via `f.TUI` (Factory noun). Library boundaries: only `iostreams` imports `lipgloss`; only `tui` imports `bubbletea`/`bubbles`; only `term` imports `golang.org/x/term`
10. `iostreams` owns the canonical color palette, styles, and design tokens. `tui` accesses them via qualified imports (`iostreams.PanelStyle`), `text` utilities via `text.Truncate`
11. `SpinnerFrame()` is a pure function in `iostreams` used by the goroutine spinner. The tui `SpinnerModel` wraps `bubbles/spinner` directly but maintains visual consistency through shared `CyanStyle`
12. `zerolog` is for file logging only — user-visible output uses `fmt.Fprintf` to IOStreams streams. Command-layer code accesses logger via `f.Logger` (Factory lazy noun captured on Options struct), library-layer code accepts `*logger.Logger` in constructors. Logger init happens lazily on first `f.Logger()` call
13. Package boundary rule: path resolution + config file I/O belongs to `internal/config`; project identity/CRUD/worktree lifecycle orchestration belongs to `internal/project`
14. Firewall uses a **global BPF route_map** keyed by `{domain_hash, dst_port}` (not per-container). Per-container enforcement comes from presence in `container_map`, which enables live rule sync across all running containers via `ebpf-manager sync-routes`. `connect6` routes IPv4-mapped addresses so dual-stack sockets cannot bypass the firewall.
15. CoreDNS is a **custom build** (`cmd/coredns-clawker`) that embeds the `internal/dnsbpf` plugin. The binary is `go:embed`'d into `internal/firewall/coredns_embed.go` and built into a Docker image on-demand by `ensureEmbeddedImage`, replacing the stock `coredns/coredns` image. `corednsContainerConfig` runs with `CAP_BPF + CAP_SYS_ADMIN` and a `/sys/fs/bpf` mount so the plugin can write the dns_cache map directly. `EnsureRunning` initializes eBPF before starting CoreDNS; DNS seeding from the Go side has been removed — the plugin is the source of truth.

## Mock Generation

Mocks are generated by [moq](https://github.com/matryer/moq) via `//go:generate` directives on interfaces. **Never hand-edit generated mock files.** To regenerate after changing an interface:

```bash
cd internal/<package> && go generate ./...
```

Generated mocks live in `<package>/mocks/` and are prefixed with `// Code generated by moq; DO NOT EDIT.`

## Important Gotchas

* `os.Exit()` does NOT run deferred functions — restore terminal state explicitly
* Raw terminal mode: Ctrl+C goes to container, not as SIGINT
* Never use `logger.Fatal()` in Cobra hooks — return errors instead
* Don't wait for stdin goroutine on container exit (may block on Read)
* Docker hijacked connections need cleanup of both read and write sides
* Terminal visual state (alternate screen, cursor visibility, colors) must be reset separately from termios mode — `term.Restore()` sends escape sequences before restoring raw/cooked mode
* Terminal resize +1/-1 trick: Resize to (height+1, width+1) then actual size to force SIGWINCH for TUI redraw
* E2E tests use Docker resource labels (`dev.clawker.test=true`) for cleanup; `make test-clean` removes leaked resources
* Container flag types and domain logic consolidated in `internal/cmd/container/shared/` — `CreateContainer()` is the single creation entry point
* After modifying a package's public API, update its `CLAUDE.md` and corresponding `.claude/rules/` file
* Empty projects generate 2-segment names (`clawker.dev`), not 3 (`clawker..dev`)
* Docker Desktop socket mounting: SDK `HostConfig.Mounts` (mount.Mount) behaves differently from `HostConfig.Binds` (CLI `-v`) for Unix sockets on macOS. The SDK may fail with `/socket_mnt` path errors while CLI works. Integration tests that mount sockets should skip on macOS or use Binds.
* Clawker files can be in `./.clawkerlocal/` during local development. Check here first before the defaults when the user needs you to debug problems. UAT testing is often done using local repository config dirs (see: `make localenv`).

## Context Management (Critical)

**NEVER** store `context.Context` in struct fields. Pass as first parameter to I/O methods. Use `context.Background()` for cleanup in deferred functions.

## Security

### Version Pinning

All external dependencies must be pinned to exact versions with integrity verification where possible. Never use `@latest`, floating tags, or unpinned references.

| Context | Pinning requirement | Example |
|---------|-------------------|---------|
| `go.mod` | Go manages this via `go.sum` checksums | Automatic |
| Dockerfile base images | SHA256 digest | `FROM golang:1.24.1@sha256:abc123...` |
| Dockerfile binary installs | Version + SHA256 checksum verification | `wget ... && echo "$SHA /tmp/file" \| sha256sum -c -` |
| CI workflow actions | SHA commit hash, not version tag | `uses: actions/checkout@a1b2c3d...` |
| CI tool installs | Pinned version + checksum where available | `GITLEAKS_VERSION=8.30.1` |
| Pre-commit hooks | SHA commit hash with version comment | `rev: 83d9cd68...  # frozen: v8.30.1` |
| Go tool installs (`go install`) | SHA commit hash or exact version | `go install tool@v2.0.1` or `tool@sha...` |
| Container images in code | SHA256 digest in constants | `DefaultGoBuilderImage = "golang:1.24.1@sha256:..."` |
| npm/pip installs in Dockerfiles | Exact version | `npm install -g @anthropic-ai/claude-code@${VERSION}` |
| BPF bytecode regeneration | Pinned Docker builder (base image digest + apt versions + Go toolchain digest + `bpf2go` version) | `Dockerfile.bpf-builder` — see `internal/ebpf/REPRODUCIBILITY.md`; enforced by `make bpf-verify` in CI |

**Why:** Version tags are mutable — a compromised upstream can re-tag a release. SHA pins are immutable and verifiable. This is defense-in-depth against supply chain attacks (see `docs/threat-model.mdx`).

**BPF bytecode specifically:** `internal/ebpf/clawker_*_bpfel.{go,o}` are embedded into the clawker binary via `go:embed` and run in users' kernels. They are committed for build convenience but anchored to a pinned reproducible recipe — every PR runs `make bpf-verify` in CI to regenerate under the pinned inputs and fail on any drift. Never update the committed bytecode without regenerating through `make bpf-regenerate`. See `internal/ebpf/REPRODUCIBILITY.md` for the full provenance chain and the pin-update procedure.

**When adding any new external dependency**, look up the actual release SHA/digest — do not rely on training data or cached knowledge for version hashes.

## Testing Requirements

**All tests must pass before any change is complete.** Run `make test` (unit) or `make test-all` (all suites). See Build Commands above for individual test suites. See `.claude/rules/testing.md` for conventions.

## Documentation

* `.claude/rules/` — Auto-loaded guidelines (code style, testing, path-scoped package rules)
* `.claude/docs/` — On-demand reference docs (architecture, CLI verbs, design)
* `internal/*/CLAUDE.md` — Package-specific API references (lazy-loaded)
* `.serena/memories/` — Active work-in-progress tracking

**Critical**: After code changes, update README.md (user-facing), CLAUDE.md (developer-facing), and memories as appropriate.

### Completion Gate: Plugin & Docs

After completing any bug fix or feature change:
- Check if the fix addresses an issue in `claude-plugin/clawker-support/skills/clawker-support/reference/known-issues.md`. If so, remove or update the entry so the support skill doesn't advise workarounds for fixed bugs.
- If the change affects user-facing configuration, CLI commands, or behavior, update the relevant Mintlify docs in `docs/` (hand-authored `*.mdx` pages, not `cli-reference/` which is auto-generated).

### Mintlify Documentation Site (docs.clawker.dev)

User-facing docs are powered by [Mintlify](https://mintlify.com/) and live in the `docs/` directory.

* `docs/docs.json` — Mintlify site config (theme, nav, colors, integrations)
* `docs/custom.css` — Dark terminal theme overrides (surface colors, glassmorphism navbar, amber hover glow)
* `docs/favicon.svg` — `>_` terminal prompt favicon (amber on dark)
* `docs/assets/` — Image assets directory
* `docs/index.mdx` — Homepage
* `docs/*.mdx` — Hand-authored pages (quickstart, installation, configuration)
* `docs/cli-reference/*.md` — Auto-generated via Makefile, checked in, freshness verified separately in CI (**never edit directly**)
* `docs/architecture.mdx`, `docs/design.mdx`, `docs/testing.md` — Developer docs with Mintlify frontmatter
* See `.claude/rules/mintlify-docs.md` for full conventions (theming, MDX parsing, navigation)

**Regenerating CLI reference**: `go run ./cmd/gen-docs --doc-path docs --markdown --website`

* `--website` flag produces MDX-safe output (escapes bare `<word>` angle brackets) with Mintlify frontmatter
* Source: `internal/docs/markdown.go` (`GenMarkdownTreeWebsite`, `EscapeMDXProse`)

**Local preview**: `npx mintlify dev --docs-directory docs` (requires Node.js)

**Deployment**: Mintlify-hosted with GitHub App auto-deploy. Custom domain via Cloudflare CNAME → `cname.vercel-dns.com`.

## Documentation Maintenance

* `bash scripts/check-claude-freshness.sh` — Check if CLAUDE.md files are stale vs Go source
* `/audit-memory` — Comprehensive documentation health audit (in Claude Code)
* `bash scripts/install-hooks.sh` — Install pre-commit hooks (all CI quality gates)
