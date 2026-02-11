# Container Commands Package

Docker CLI-compatible container management commands. Subpackages (`run/`, `create/`, `start/`, etc.) are individual subcommands.

## Package Structure

```
internal/cmd/container/
├── container.go        # Parent command, registers subcommands
├── opts/               # Shared container flag types (import cycle workaround)
├── shared/             # Shared domain logic (container init orchestration)
├── run/                # clawker container run (RunOptions, NewCmdRun)
├── create/             # clawker container create (CreateOptions, NewCmdCreate)
├── start/              # clawker container start (StartOptions, NewCmdStart)
├── exec/               # clawker container exec (ExecOptions, NewCmdExec)
└── ... (stop, attach, logs, list, inspect, cp, kill, pause, unpause, remove, rename, restart, stats, top, update, wait)
```

**Import cycle rule**: `container/` imports subcommands, subcommands need shared types. The `opts/` package exists to break the `container -> run -> container` cycle for flag types. The `shared/` package contains domain orchestration logic used by multiple subcommands. Never put shared utilities in the parent package.

## Migration Status

All 20 container commands use canonical patterns. No deprecated helpers remain.

| Pattern | Before | After | Commands Affected |
|---------|--------|-------|-------------------|
| `cmdutil.HandleError` | 27 calls | 0 — replaced with `return fmt.Errorf("context: %w", err)` | All 17 non-migrated commands |
| `tabwriter.NewWriter` | 4 usages | 0 — replaced with `opts.TUI.NewTable(headers...)` | list, top, stats |
| `StreamWithResize` | 2 usages | 0 — replaced with `pty.Stream` + `signals.NewResizeHandler` | attach, exec |
| `"Error: %v"` raw output | Multiple | 0 — replaced with `cs.FailureIcon()` pattern | Multi-container loop commands |
| Tier 2 tests | 6 commands | 20 commands — all have Cobra+Factory tests | 14 commands gained Tier 2 tests |

### Canonical Error Handling

```go
// Docker connection error — return wrapped error to Main() for centralized rendering
client, err := opts.Client(ctx)
if err != nil {
    return fmt.Errorf("connecting to Docker: %w", err)
}

// Per-item errors in multi-container loops — icon + name + error to stderr
cs := ios.ColorScheme()
fmt.Fprintf(ios.ErrOut, "%s %s: %v\n", cs.FailureIcon(), name, err)
```

### Canonical Table Rendering

```go
// Static table output via TUI Factory noun
tp := opts.TUI.NewTable("NAME", "STATUS", "IMAGE")
for _, c := range containers {
    tp.AddRow(c.Name, c.Status, c.Image)
}
return tp.Render()
```

### Canonical Stream + Resize (attach/exec)

```go
// Separate I/O from resize — I/O in goroutine, resize after
streamDone := make(chan error, 1)
go func() { streamDone <- pty.Stream(ctx, hijacked.HijackedResponse) }()
if pty.IsTerminal() {
    w, h, _ := pty.GetSize()
    resizeFunc(uint(h+1), uint(w+1))  // +1/-1 trick forces SIGWINCH
    resizeFunc(uint(h), uint(w))
    rh := signals.NewResizeHandler(resizeFunc, pty.GetSize)
    rh.Start()
    defer rh.Stop()
}
return <-streamDone
```

### Format/Filter Flags (list command)

`container list` supports `--format`/`--json`/`-q`/`--filter key=value` via `cmdutil.FormatFlags` and `cmdutil.FilterFlags`. Valid filter keys: `name`, `status`, `agent`. See `internal/cmd/image/list/` for the canonical pattern.

### Per-Command Documentation

- `attach/CLAUDE.md` — Attach-to-running-container pattern, Stream+resize
- `exec/CLAUDE.md` — Exec credential injection, TTY/non-TTY paths, detach mode
- `start/CLAUDE.md` — Attach-then-start pattern, waitForContainerExit
- `shared/CLAUDE.md` — ContainerInitializer, init orchestration

## Parent Command (`container.go`)

```go
// NewCmdContainer creates the parent "container" command and registers all subcommands.
func NewCmdContainer(f *cmdutil.Factory) *cobra.Command
```

Registers: `NewCmdAttach`, `NewCmdCp`, `NewCmdCreate`, `NewCmdExec`, `NewCmdInspect`, `NewCmdKill`, `NewCmdList`, `NewCmdLogs`, `NewCmdPause`, `NewCmdRemove`, `NewCmdRename`, `NewCmdRestart`, `NewCmdRun`, `NewCmdStart`, `NewCmdStats`, `NewCmdStop`, `NewCmdTop`, `NewCmdUnpause`, `NewCmdUpdate`, `NewCmdWait`.

All subcommand constructors follow: `NewCmd*(f *cmdutil.Factory, runF func(context.Context, *XxxOptions) error) *cobra.Command`

## Shared Container Options (`opts/`)

```go
import copts "github.com/schmitthub/clawker/internal/cmd/container/opts"

containerOpts := copts.NewContainerOptions()
copts.AddFlags(cmd.Flags(), containerOpts)       // Register all shared flags
copts.MarkMutuallyExclusive(cmd)                  // --agent and --name are mutually exclusive

agentName := containerOpts.GetAgentName()          // From --agent or --name
containerConfig, hostConfig, networkConfig, err := containerOpts.BuildConfigs(flags, mounts, cfg)
containerOpts.ValidateFlags()                      // Cross-field validation
```

**Exported types in opts/**:
- `ContainerOptions` — all container flags (naming, env, volumes, resources, networking, security, health, runtime)
- `ListOpts` — repeatable string flags (`NewListOpts`, `NewListOptsRef`)
- `MapOpts` — key=value flags (`NewMapOpts`)
- `PortOpts` — port mapping flags (`NewPortOpts`)
- `NetworkOpt` — advanced `--network` syntax with `NetworkAttachmentOpts`

**Exported functions in opts/**:
- `AddFlags(flags, opts)` — register all shared container flags
- `MarkMutuallyExclusive(cmd)` — mark `--agent`/`--name` mutually exclusive
- `ResolveAgentName(agent, generateRandom)` — resolve agent name with fallback
- `FormatContainerName(project, agent)` — format `clawker.<project>.<agent>`
- `ParseLabelsToMap(labels)` — convert `[]string{"k=v"}` to `map[string]string`
- `MergeLabels(base, user)` — merge label maps (base takes precedence)
- `NeedsSocketBridge(cfg)` — returns true if config enables GPG or SSH forwarding (shared by run/start/exec)

**BuildConfigs validation**: `--memory-swap` requires `--memory`; `--no-healthcheck` conflicts with `--health-*`; `--restart` (except "no") conflicts with `--rm`; namespace mode validation (PID, IPC, UTS, userns, cgroupns).

**Key flag categories**: Basic (`Agent`, `Name`, `Image`, `TTY`, `Stdin`, `AutoRemove`, `Mode`), Environment (`Env`, `EnvFile`, `Labels`, `LabelsFile`), Volumes (`Volumes`, `Tmpfs`, `ReadOnly`, `VolumesFrom`, `Mounts`), Networking (`Publish`, `Hostname`, `DNS`, `ExtraHosts`, `NetMode`), Resources (`Memory`, `MemorySwap`, `CPUs`, `CPUShares`, `BlkioWeight`, `PidsLimit`), Security (`CapAdd`, `CapDrop`, `Privileged`, `SecurityOpt`), Health Checks, Process & Runtime (`Restart`, `StopSignal`, `Init`), Devices (`Devices`, `GPUs`, `DeviceCgroupRules`).

## Shared Domain Logic (`shared/`)

Container init orchestration — domain logic shared between `run/` and `create/` subcommands.

### ContainerInitializer (Factory noun)

Progress-tracked container initialization. Both `run` and `create` use `ContainerInitializer.Run()` for the 5-step init flow with TUI progress display.

```go
import "github.com/schmitthub/clawker/internal/cmd/container/shared"

// Construct from Factory (wired in NewCmdRun / NewCmdCreate)
initializer := shared.NewContainerInitializer(f)

// Run with pre-resolved params (after image resolution)
result, err := initializer.Run(ctx, shared.InitParams{
    Client: client, Config: cfg, ContainerOptions: containerOpts,
    Flags: opts.flags, Image: containerOpts.Image,
    StartAfterCreate: opts.Detach,
})
// result.ContainerID, result.AgentName, result.ContainerName, result.Warnings
```

### Low-level helpers (called by ContainerInitializer internally)

```go
// One-time claude config init (copy strategy + credentials)
shared.InitContainerConfig(ctx, shared.InitConfigOpts{...})

// Inject onboarding marker into container
shared.InjectOnboardingFile(ctx, shared.InjectOnboardingOpts{...})
```

**Exported types in shared/**:
- `ContainerInitializer` — Factory noun for progress-tracked container init
- `InitParams` — runtime values: Client, Config, ContainerOptions, Flags, Image, StartAfterCreate
- `InitResult` — outputs: ContainerID, AgentName, ContainerName, HostProxyRunning, Warnings
- `InitConfigOpts` — project/agent names, `*config.ClaudeCodeConfig`, `CopyToVolumeFn` (DI for Docker volume copy)
- `InjectOnboardingOpts` — container ID, `CopyToContainerFn` (DI for Docker container copy)
- `CopyToVolumeFn` — function type matching `(*docker.Client).CopyToVolume` signature
- `CopyToContainerFn` — simplified function type for tar-to-container copy

**Exported functions in shared/**:
- `NewContainerInitializer(f)` — construct from Factory; captures IOStreams, TUI, GitManager, HostProxy
- `(*ContainerInitializer).Run(ctx, InitParams)` — 5-step progress: workspace, config, env, create, start
- `InitContainerConfig(ctx, InitConfigOpts)` — one-time claude config init for new containers (copy strategy + credentials)
- `InjectOnboardingFile(ctx, InjectOnboardingOpts)` — writes `~/.claude.json` onboarding marker to container

## Image Resolution (@ Symbol)

When `opts.Image == "@"`, call `client.ResolveImageWithSource(ctx)`:

```go
if opts.Image == "@" {
    resolvedImage, err := client.ResolveImageWithSource(ctx)
    // nil → no image found; caller prints error + next steps
    // Source == ImageSourceDefault → verify exists, offer rebuild via shared.RebuildMissingDefaultImage
    opts.Image = resolvedImage.Reference
}
```

**Resolution order**: 1) Project image with `:latest` tag (by label lookup) -> 2) Merged `default_image` from config/settings.

Interactive rebuild logic lives in `shared/image.go` (`RebuildMissingDefaultImage`). Commands pass `client.BuildDefaultImage` as the build function — the Client method delegates to `BuildDefaultImageFunc` when non-nil (fawker/tests), otherwise runs the real build.

## Workspace Setup Pattern

Workspace setup is handled internally by `ContainerInitializer.Run()` in the "Prepare workspace" step. It calls `workspace.SetupMounts()` which consolidates mode resolution, strategy creation, volume mounts, and config volume setup.

## Command Dependency Injection Pattern

Commands use function references on Options structs rather than `*Factory` directly. `NewCmd*` takes `*Factory` and wires the references:

```go
type StopOptions struct {
    IOStreams   *iostreams.IOStreams
    Client     func(context.Context) (*docker.Client, error)
    Resolution func() *config.Resolution
    Agent   bool
    Timeout int
    Signal  string
    containers []string
}
```

Run functions accept `*Options` only:

```go
func runStop(opts *StopOptions) error {
    ctx := context.Background()
    client, err := opts.Client(ctx)  // Call function ref, not Factory
    resolution := opts.Resolution()
    project := resolution.ProjectKey
}
```

## Exec Credential Forwarding

The `exec` command automatically injects git credential forwarding env vars (like `CLAWKER_HOST_PROXY` and `CLAWKER_GIT_HTTPS`) into exec'd processes. This enables git operations inside exec sessions. HTTPS credentials are forwarded via host proxy, while SSH/GPG agent forwarding is handled by the socketbridge (started automatically via `SocketBridge.EnsureBridge`). Credentials are set up via `workspace.SetupGitCredentials()`.

## SocketBridge Wiring

The `run`, `start`, and `exec` commands wire `f.SocketBridge()` to start a per-container bridge daemon that forwards SSH/GPG agent sockets via `docker exec` + muxrpc protocol. `EnsureBridge` is idempotent — safe to call from both `run` and subsequent `exec` invocations on the same container.

The `stop` and `remove` commands wire `f.SocketBridge()` to stop the bridge daemon before the container is stopped or removed. This prevents stale bridge processes whose docker exec sessions are dead from being reused on container restart. Bridge cleanup is best-effort — errors are logged via `logger.Warn()` but do not fail the stop/remove operation. A nil `SocketBridge` is safe (guarded by nil check).

## Testing

Container command tests use the **Cobra+Factory pattern** -- the canonical approach for testing commands end-to-end without a Docker daemon.

### Pattern

1. Create `dockertest.NewFakeClient()` and configure needed setup helpers
2. Build a `*cmdutil.Factory` with faked closures (`testFactory` helper)
3. Call `NewCmdRun(f, nil)` -- `nil` runF means the real run function executes
4. Set args, execute, assert on output and `fake.AssertCalled`

### Per-Package Helpers

`testFactory` and `testConfig` are **per-package** (not shared). Each command package creates its own helpers suited to its specific dependencies. Copy and adapt from `run/run_test.go` when adding tests to other subcommands.

### Test Tiers

- **Tier 1** (flag parsing): Use `runF` trapdoor to capture Options without execution
- **Tier 2** (integration): Use Cobra+Factory pattern with `nil` runF for full pipeline
- **Tier 3** (unit): Call domain functions directly without Cobra or Factory

See `.claude/memories/TESTING-REFERENCE.md` for full templates and decision matrix.

---

## Exit Code Handling

Use `cmdutil.ExitError` (defined in `internal/cmdutil/output.go`) to propagate non-zero container exit codes. This allows deferred cleanup (terminal restore, container removal) to run before the process exits.

```go
if status != 0 {
    return &cmdutil.ExitError{Code: status}
}
```

## Attach-Then-Start Pattern (`run.go` and `start.go`)

Interactive container sessions (`-it`) use attach-before-start to avoid missing output from short-lived containers:

1. **Attach** to container before starting (prevents race with `--rm` containers)
2. **Start I/O goroutines** before `ContainerStart` (ready to receive immediately)
3. **Start container** via `ContainerStart`
4. **Resize TTY** after start -- the +1/-1 trick forces SIGWINCH for TUI redraw
5. **Wait for exit or detach** -- on stream completion, wait up to 2s for exit status; timeout means Ctrl+P Ctrl+Q detach (container still running)

**Key separation**: I/O streaming (`pty.Stream`) starts pre-start; resize starts post-start. This matches Docker CLI's split between `attachContainer()` and `MonitorTtySize()`.

**Alt screen handoff**: For interactive runs (`-it`), `InitParams.AltScreen=true` renders the init progress display in BubbleTea's alternate screen buffer. When progress finishes, the alt screen clears automatically, giving a clean terminal for the container's TTY session. Detached runs and `create` leave `AltScreen=false` for inline progress.
