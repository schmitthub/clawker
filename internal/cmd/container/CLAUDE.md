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

When `opts.Image == "@"`, call `cmdutil.ResolveAndValidateImage()`:

```go
if opts.Image == "@" {
    resolved, err := cmdutil.ResolveAndValidateImage(ctx, client, cfg, settings)
    opts.Image = resolved.Reference
}
```

**Resolution order**: 1) Project image (`clawker-<project>:latest` with labels) → 2) Settings `default_image` → 3) Config `default_image`.

**Key functions** in `internal/cmdutil/resolve.go`: `ResolveImageWithSource()`, `ResolveAndValidateImage()`, `FindProjectImage()`.

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

```go
type ExitError struct { Code int }
func (e *ExitError) Error() string { return fmt.Sprintf("container exited with code %d", e.Code) }

func runCmd(...) (retErr error) {
    defer func() {
        var exitErr *ExitError
        if errors.As(retErr, &exitErr) { os.Exit(exitErr.Code) }
    }()
    // Return ExitError instead of calling os.Exit directly
}
```
