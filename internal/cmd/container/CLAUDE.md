# Container Commands Package

Docker CLI-compatible container management commands. Subpackages (`run/`, `create/`, `start/`, etc.) are individual subcommands.

## Package Structure

```
internal/cmd/container/
├── container.go        # Parent command, registers subcommands
├── opts/               # Shared container options (import cycle workaround)
│   ├── opts.go         # ContainerOptions, AddFlags, BuildConfigs
│   └── network.go      # NetworkOpt, NetworkAttachmentOpts
├── run/                # clawker container run (RunOptions, NewCmdRun)
├── create/             # clawker container create (CreateOptions, NewCmdCreate)
├── start/              # clawker container start (StartOptions, NewCmdStart)
├── stop/               # clawker container stop (StopOptions, NewCmdStop)
├── exec/               # clawker container exec (ExecOptions, NewCmdExec)
├── attach/             # clawker container attach (AttachOptions, NewCmdAttach)
├── logs/               # clawker container logs (LogsOptions, NewCmdLogs)
├── list/               # clawker container ls (ListOptions, NewCmdList)
├── inspect/            # clawker container inspect (InspectOptions, NewCmdInspect)
├── cp/                 # clawker container cp (CpOptions, NewCmdCp)
├── kill/               # clawker container kill (KillOptions, NewCmdKill)
├── pause/, unpause/    # PauseOptions/UnpauseOptions, NewCmdPause/NewCmdUnpause
├── remove/             # clawker container rm (RemoveOptions, NewCmdRemove)
├── rename/             # clawker container rename (RenameOptions, NewCmdRename)
├── restart/            # clawker container restart (RestartOptions, NewCmdRestart)
├── stats/              # clawker container stats (StatsOptions, NewCmdStats)
├── top/                # clawker container top (TopOptions, NewCmdTop)
├── update/             # clawker container update (UpdateOptions, NewCmdUpdate)
└── wait/               # clawker container wait (WaitOptions, NewCmdWait)
```

**Import cycle rule**: `container/` imports subcommands, subcommands need shared types. The `opts/` package exists to break the `container -> run -> container` cycle. Never put shared utilities in the parent package.

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

## Image Resolution (@ Symbol)

When `opts.Image == "@"`, call `client.ResolveImageWithSource(ctx)`:

```go
if opts.Image == "@" {
    resolvedImage, err := client.ResolveImageWithSource(ctx)
    // nil → no image found; caller prints error + next steps
    // Source == ImageSourceDefault → verify exists, offer rebuild via handleMissingDefaultImage
    opts.Image = resolvedImage.Reference
}
```

**Resolution order**: 1) Project image with `:latest` tag (by label lookup) -> 2) Merged `default_image` from config/settings.

Interactive rebuild logic (`handleMissingDefaultImage`) lives in each command package (`run/run.go`, `create/create.go`), not in the docker package.

## Workspace Setup Pattern

```go
mode, _ := config.ParseMode(opts.Mode)  // CLI flag overrides config default
strategy, _ := workspace.NewStrategy(mode, workspace.Config{...})
strategy.Prepare(ctx, client)
workspaceMounts := strategy.GetMounts()
workspaceMounts = append(workspaceMounts, workspace.GetConfigVolumeMounts(project, agent)...)
if cfg.Security.DockerSocket { workspaceMounts = append(workspaceMounts, workspace.GetDockerSocketMount()) }
```

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

## Wait Helper Pattern (`waitForContainerExit`)

Unexported helper in `run/run.go`. Follows Docker CLI's `waitExitOrRemoved` pattern. Wraps the dual-channel `ContainerWait` into a single `<-chan int` status channel.

**Critical**: Use `WaitConditionNextExit` (not `WaitConditionNotRunning`) when waiting is set up before `ContainerStart` -- a "created" container is already not-running, so `WaitConditionNotRunning` returns immediately. Use `WaitConditionRemoved` when `--rm` (auto-remove) is set.

## Attach-Then-Start Pattern (`run.go` and `start.go`)

Interactive container sessions (`-it`) use attach-before-start to avoid missing output from short-lived containers:

1. **Attach** to container before starting (prevents race with `--rm` containers)
2. **Start I/O goroutines** before `ContainerStart` (ready to receive immediately)
3. **Start container** via `ContainerStart`
4. **Resize TTY** after start -- the +1/-1 trick forces SIGWINCH for TUI redraw
5. **Wait for exit or detach** -- on stream completion, wait up to 2s for exit status; timeout means Ctrl+P Ctrl+Q detach (container still running)

**Key separation**: I/O streaming (`pty.Stream`) starts pre-start; resize starts post-start. This matches Docker CLI's split between `attachContainer()` and `MonitorTtySize()`.
