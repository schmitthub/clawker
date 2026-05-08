# Clawker Key Concepts

> Type and abstraction index for the clawker codebase. Use this when you need a one-line reminder of what a named type does and which package owns it. For the full API of any symbol, read the package-specific `internal/<pkg>/CLAUDE.md` — they're lazy-loaded on demand.

> **CP crashing is a SECURITY incident.** eBPF programs are CP-managed but kernel-pinned under `/sys/fs/bpf` and survive CP container death. Clean lifecycle (`Stack.Stop` → `ebpfMgr.FlushAll`) only runs on the orchestrator's drain-to-zero path; panics, `log.Fatal`, and unrecovered goroutines all skip it. After a CP crash, agent containers keep running, eBPF keeps filtering against frozen rules, but CP is no longer there to update rules, expire bypasses, observe behavior, or dispatch containment. The user's mental model "CP has my agents covered" is silently false. **Therefore: no panics in code reachable from `cmd/clawker-cp/main.go` after `SetReady`. Constructors return `(nil, error)`; long-lived goroutines must `recover()`; subsystem failures degrade (`initExec = nil`, `dialer = nil`) instead of crashing the daemon.** See root `CLAUDE.md` and `internal/controlplane/CLAUDE.md` for the full statement and templates.

> **CP ≠ firewall.** Common LLM confusion. CP is unconditional infrastructure (auth, gRPC AdminService on AdminPort, AgentService listener on AgentPort, agent registry, CP→clawkerd `agent.Dialer` outbound dialer, overseer event bus, mTLS, owns clawker-net). The firewall is one optional subsystem CP manages (Envoy + CoreDNS + eBPF egress enforcement), toggled by `firewall.enable` in `settings.yaml` (the master switch is global, NOT in `clawker.yaml` — the project schema's `security.firewall` holds per-project `add_domains`/`rules` only). Disabling firewall does NOT disable CP, agent registry, agent.Dialer→clawkerd Session, ListAgents, or any non-firewall AdminService RPC. Don't gate non-firewall behavior on the firewall flag.

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
| `firewall.Handler` | gRPC handler serving the 13 firewall RPCs (the AdminService surface as a whole is 13 firewall RPCs + `ListAgents` = 14 methods; `internal/controlplane/firewall`). Embedded by `controlplane.adminServer`. `NewHandler(HandlerDeps)` panics on missing `EBPF` or `Resolver` |
| `firewall.Stack` | CP-side Envoy + CoreDNS container lifecycle — `EnsureRunning`/`Stop`/`Reload`/`WaitForHealthy`/`Status` + IP/CIDR accessors. Uses `*docker.Client` via DooD |
| `firewall.ContainerResolver` | Injectable Docker lookup: `(ctx, ref) → (id, cgroupPath, exists, err)`. Production wiring: `cmd/clawker-cp/main.go::containerResolverFromDocker`. `exists=false` + `err=nil` is the "container gone" signal |
| `firewall.EBPFCgroupPath` / `firewall.DetectCgroupDriver` / `firewall.ResolveContainerID` | Pure helpers for cgroup path resolution; driver cached on `Handler` at startup via `DetectCgroupDriver` |
| `project.Project.EgressRules()` | Builds complete rule set from project config (security.firewall rules + required internal rules like Claude API, Docker registry). Consumed by `BootstrapServicesPreStart` |
| `firewall.EgressRulesFile` | `storage.Schema` implementation for `egress-rules.yaml` (owned by CP at `FirewallDataSubdir`) |
| `firewall.ProtoRulesToConfig` / `firewall.ConfigRulesToProto` | Exported wire ↔ config rule translation used by `BootstrapServicesPostStart` |
| `dnsbpf.Handler` | CoreDNS plugin (`internal/dnsbpf`) that intercepts DNS responses and writes IP → {domain_hash, TTL} entries to the BPF dns_cache map. Registered as `dnsbpf` directive in `cmd/coredns-clawker` |
| `ebpf.Manager` | Go-side loader for clawker cgroup/sock programs (`internal/controlplane/firewall/ebpf`, compiled via bpf2go). CP calls `Load()` once at startup; `CleanupStaleBypass`/`FlushAll` drive defensive startup + drain-to-zero |
| `ebpf.EBPFManager` | Interface consumed by `firewall.Handler`: Install/Remove/Enable/Disable/SyncRoutes/FlushAll. Mock: `firewall/ebpf/mocks.EBPFManagerMock` |
| `cpboot.Manager` | Factory-facing noun (`f.ControlPlane()`) wrapping host-side CP container lifecycle: `EnsureRunning`, `Stop`, `IsRunning`, `ProbeHealthz`. Lives in `internal/controlplane/cpboot/`. Consumed by the break-glass `clawker controlplane up/down/status` verbs |
| `cpboot.EnsureRunning` | Package-level host-side CP container bootstrap (idempotent, mutex-guarded, mount-mode reconciliation, health-poll). Consumed via `ensureRunning` seam by `adminClientFunc` |
| `controlplane.AgentWatcher` | Polls Docker for `purpose=agent` containers; on drain-to-zero (past grace/threshold, `ListErrCeiling`-bounded) fires drain callback for CP self-shutdown (INV-B2-007). `Run` is at-most-once (`atomic.Bool`) |
| `f.AdminClient(ctx)` | Factory lazy noun returning `adminv1.AdminServiceClient` — transparent CP bootstrap on first call, mTLS + OAuth2 + keepalive. Rebuilds `grpc.ClientConn` only on `TransientFailure`/`Shutdown`. Mock: `controlplane/mocks.AdminServiceClientMock` |
| `agent.Registry` | Sqlite-persisted record of registered agents keyed by SHA-256 over the mTLS peer cert DER (`[sha256.Size]byte`). CP is the SOLE writer — writes rows via Register handler, evicts via dockerevents `container/destroy` + startup reap. Channel-bound identity — `Lookup(thumbprint, cn)` resolves an entry by thumbprint and verifies the supplied peer cert CN matches the pre-computed `canonical_cn` column. Mismatch on thumbprint OR CN collapses to `ErrUnknownAgent`. Backing store: `modernc.org/sqlite`. Lives in `internal/controlplane/agent` |
| `overseer.Overseer` | Typed event bus + in-memory worldview state for the CP. Replaces the deleted `informer` package. Producers (`dockerevents`, `agent`) call `overseer.Publish[T](bus, ev)` with typed events; subscribers call `Subscribe[T](bus, name)` for a typed channel. Bus loop owns subscriber registry + `State` (Containers + Agents); `Snapshot(ctx)` returns a deep-copied projection. Per-subscriber drop-oldest under buffer pressure; panic recovery on filter/applier closures. Lives in `internal/controlplane/overseer` |
| `agent.IdentityInterceptor` | Paired (unary, stream) interceptors in `internal/controlplane/agent/identity_interceptor.go`. Resolves peer cert thumbprint to an `agent.Entry` for every non-opted-out RPC. Register is opted out (row doesn't exist pre-call). Stream wrapper's `Context()` is on the wrapper (NOT promoted) — promotion would silently break identity binding for streaming RPCs. Wired AFTER `AuthInterceptor` on the agent listener |
| `agent.Dialer` | CP-side outbound dialer for `ClawkerdService.Session`. Permissive trust by design (asymmetric — see root CLAUDE.md). Every cert/CN/registry outcome surfaces as typed fields on the `SessionConnected` overseer event; the dial only fails on connectivity (TCP timeout, container gone, retry exhausted, ctx cancelled). Drives one-time Register handshake on registry miss. fd-leak ceiling tracks Close failures. Lives in `internal/controlplane/agent` |
| `agent.WithEntry` / `agent.EntryFromContext` | Trust-projection seam between `IdentityInterceptor` and per-agent RPC handlers. `WithEntry(nil)` panics; `EntryFromContext` returns `ok=false` on nil entry — defends the typed-nil-pointer silent identity vacuum |
| `auth.MintAgentCert` | Generates a per-agent mTLS leaf cert signed by the CLI CA. Signature: `MintAgentCert(caCertPath, caKeyPath string, project ProjectSlug, agent AgentName)`. Typed identity values (built via `NewProjectSlug`/`NewAgentName`) push validation to the wire boundary so the helper itself trusts its inputs. CN is composed inside via `auth.CanonicalAgentCN(project, agent)` so every CLI caller produces the same canonical shape and the agent handler's CN cross-check has a single equality to enforce. 24h lifetime, returns PEM + SHA-256 thumbprint over cert.Raw. Material is for delivery to the container's writable layer via Docker CopyToContainer (see `consts.BootstrapDir`) — never persisted on the host |
| `auth.CanonicalAgentCN` | Pure function `CanonicalAgentCN(project ProjectSlug, agent AgentName) string` composing the canonical agent CN. Typed identity values guarantee callers can't fabricate malformed components. Single source of truth for the canonical form, shared by `MintAgentCert` (issuance) and `agent.Registry.Add` (pre-computed `canonical_cn` column read by `Lookup`). Empty project → 2-segment `clawker.<agent>` matching `docker.ContainerName` behavior |
| `auth.BuildAgentAssertion` | ES256 client_assertion identifying clawkerd as the `clawker-agent` Hydra OAuth2 client. Same signing key as the CLI assertion; only iss/sub differ. 24h TTL covers typical container session length |
| `shared.AgentBootstrap` / `GenerateAgentBootstrap` / `WriteAgentBootstrapToContainer` / `RegisterAgentInRegistry` | Building blocks for the CLI's per-agent boot sequence: PKCE pair + mTLS leaf + CA + assertion in one struct, tar-streamed into the container's writable layer at `consts.BootstrapDir`. `GenerateAgentBootstrap(caCert, caKey, project, agent, hydraURL, signingKey)` mints the cert with canonical CN composed from (project, agent). `RegisterAgentInRegistry(reg, b, project, agent, containerID)` writes the `(thumbprint, container_id, canonical_cn, project, agent_name)` row directly to the host-side sqlite registry — no AnnounceAgent RPC, no PKCE consume |
| `clawkerd.Binary` | `//go:embed assets/clawkerd` exports the per-container agent daemon as `[]byte`. Bundler writes it into every per-project image at `/usr/local/bin/clawkerd`; entrypoint launches it before the firewall healthz wait |
| `cmd/clawkerd` | Per-container agent daemon. Reads bootstrap from `/run/clawker/bootstrap`, resolves `CLAWKER_AGENT` + `CLAWKER_PROJECT` env, starts the inbound `ClawkerdService` mTLS listener on `:7700` (CN-pinned to CP), idles on `ctx.Done` for the container's lifetime. One outbound call: CP-triggered Register handshake (`registerCoordinator`) exchanges the single-use Hydra assertion for an access token and dials CP's AgentService to write the identity row. Structured zerolog via `internal/logger.New()` to `/var/log/clawker/clawkerd.log` (50MB rotation, 7d retain, 3 backups) |
| `clawkerd.ClawkerdService` | Inbound gRPC service hosted by `cmd/clawkerd`. Single bidi-stream RPC `Session` — CP dials, sends typed `Command` payloads (Hello / ShellCommand / Stdin / CloseStdin / Signal / Shutdown), receives streamed `Response` payloads (Welcome / StdoutChunk / StderrChunk / StageExit / Done / Error). The Session bidi-stream IS the per-command dispatch channel — no clawkerd→CP outbound RPC |
| `controlplane.AgentMethodScopes()` | Per-listener scope vocabulary for the agent gRPC listener. Empty in this branch (AgentService has no inbound RPCs). Mirror of `AdminMethodScopes`; `TestAgentMethodScopes_CoversAllRPCs` walks the proto descriptor so a future RPC without a scope entry breaks the build |
| `shared.CommandOpts` | DI container for container start orchestration — function closures: Client, Config, ProjectManager, HostProxy, AdminClient, SocketBridge, Logger |
| `shared.ContainerStart()` | Three-phase container start: `BootstrapServicesPreStart` → docker start → `BootstrapServicesPostStart` (3 RPCs: FirewallInit → FirewallAddRules → FirewallEnable). Used by `run` and `start` |
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
