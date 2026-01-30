# Container Commands Package

Docker CLI-compatible container management commands. Subpackages (`run/`, `create/`, `start/`, etc.) are individual subcommands.

## Package Structure

```
internal/cmd/container/
├── container.go        # Parent command, registers subcommands
├── opts/               # Shared container options (import cycle workaround)
│   ├── opts.go         # ContainerOptions, AddFlags, BuildConfigs
│   └── opts_test.go
├── run/                # clawker container run
├── create/             # clawker container create
├── start/, stop/, ...  # Other subcommands
```

**Import cycle rule**: `container/` imports subcommands, subcommands need shared types. The `opts/` package exists to break the `container → run → container` cycle. Never put shared utilities in the parent package.

## Shared Container Options (`opts/`)

```go
import copts "github.com/schmitthub/clawker/internal/cmd/container/opts"

containerOpts := copts.NewContainerOptions()
copts.AddFlags(cmd.Flags(), containerOpts)       // Register all shared flags
copts.MarkMutuallyExclusive(cmd)                  // --agent and --name are mutually exclusive

agentName := containerOpts.GetAgentName()          // From --agent or --name
containerConfig, hostConfig, networkConfig, err := containerOpts.BuildConfigs(workspaceMounts, cfg)
```

**Key flag categories**: Basic (`Agent`, `Name`, `Image`, `TTY`, `Stdin`, `AutoRemove`, `Mode`), Environment (`Env`, `Labels`), Volumes (`Volumes`, `Tmpfs`, `ReadOnly`, `VolumesFrom`), Networking (`Publish`, `Hostname`, `DNS`, `ExtraHosts`), Resources (`Memory`, `MemorySwap`, `CPUs`, `CPUShares`), Security (`CapAdd`, `CapDrop`, `Privileged`, `SecurityOpt`), Health Checks, Process & Runtime (`Restart`, `StopSignal`, `Init`).

**Custom flag types in opts/**: `PortOpts` (port mappings), `ListOpts` (repeatable strings), `MapOpts` (key=value), `MemBytes`/`MemSwapBytes` (memory sizes), `NanoCPUs` (CPU limits).

**BuildConfigs validation**: `--memory-swap` requires `--memory`; `--no-healthcheck` conflicts with `--health-*`; `--restart` (except "no") conflicts with `--rm`.

## Image Resolution (@ Symbol)

When `opts.Image == "@"`, call `resolver.ResolveAndValidateImage()`:

```go
if opts.Image == "@" {
    resolved, err := resolver.ResolveAndValidateImage(ctx, resolver.ImageValidationDeps{...}, client, cfg, settings)
    opts.Image = resolved.Reference
}
```

**Resolution order**: 1) Project image (`clawker-<project>:latest` with labels) → 2) Settings `default_image` → 3) Config `default_image`.

**Key functions** in `internal/resolver/image.go`: `ResolveImageWithSource()`, `ResolveAndValidateImage()`, `FindProjectImage()`.

## Workspace Setup Pattern

Container commands set up workspace mounts automatically:

```go
mode, _ := config.ParseMode(opts.Mode)  // CLI flag overrides config default
strategy, _ := workspace.NewStrategy(mode, workspace.Config{...})
strategy.Prepare(ctx, client)
workspaceMounts := strategy.GetMounts()
workspaceMounts = append(workspaceMounts, workspace.GetConfigVolumeMounts(project, agent)...)
if cfg.Security.DockerSocket { workspaceMounts = append(workspaceMounts, workspace.GetDockerSocketMount()) }
```

## Command Dependency Injection Pattern

Commands use function references on Options structs rather than `*Factory` directly. `NewCmd` takes `*Factory` and wires the references:

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
    // ...
    resolution := opts.Resolution()
    project := resolution.ProjectKey
    // ...
}
```

## Exit Code Handling

Use `cmdutil.ExitError` (defined in `internal/cmdutil/output.go`) to propagate non-zero container exit codes. This allows deferred cleanup (terminal restore, container removal) to run before the process exits.

```go
import "github.com/schmitthub/clawker/internal/cmdutil"

// Return ExitError instead of calling os.Exit directly
if status != 0 {
    return &cmdutil.ExitError{Code: status}
}

// The root command checks for ExitError and calls os.Exit(code)
```

## Wait Helper Pattern (`waitForContainerExit`)

Follows Docker CLI's `waitExitOrRemoved` pattern. Wraps the dual-channel `ContainerWait` into a single `<-chan int` status channel.

```go
func waitForContainerExit(ctx context.Context, client *docker.Client, containerID string, autoRemove bool) <-chan int
```

**Critical**: Use `WaitConditionNextExit` (not `WaitConditionNotRunning`) when waiting is set up before `ContainerStart` — a "created" container is already not-running, so `WaitConditionNotRunning` returns `StatusCode=0` immediately. Use `WaitConditionRemoved` when `--rm` (auto-remove) is set.

## Attach-Then-Start Pattern (`run.go` and `start.go`)

Interactive container sessions (`-it`) use attach-before-start to avoid missing output from short-lived containers:

1. **Attach** to container before starting (prevents race with `--rm` containers)
2. **Start I/O goroutines** before `ContainerStart` (ready to receive immediately)
3. **Start container** via `ContainerStart`
4. **Resize TTY** after start — the +1/-1 trick forces SIGWINCH for TUI redraw; `ResizeHandler` monitors ongoing SIGWINCH events
5. **Wait for exit or detach** — on stream completion, wait up to 2s for exit status; timeout means Ctrl+P Ctrl+Q detach (container still running)

**Key separation**: I/O streaming (`pty.Stream`) starts pre-start; resize (`ResizeHandler`) starts post-start. This matches Docker CLI's split between `attachContainer()` and `MonitorTtySize()`.
