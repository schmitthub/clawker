# Clawker Key Concepts

> Type and abstraction index for the clawker codebase. Use this when you need a one-line reminder of what a named type does and which package owns it. For the full API of any symbol, read the package-specific `internal/<pkg>/CLAUDE.md` — they're lazy-loaded on demand.

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
