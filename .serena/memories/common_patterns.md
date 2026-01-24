# Common Development Patterns

## Adding a New CLI Command

1. Create package: `pkg/cmd/<cmdname>/<cmdname>.go`
2. Define options struct:

   ```go
   type CmdOptions struct {
       Agent string  // --agent flag (common for container commands)
       // other flags
   }
   ```

3. Create command function with Example field:

   ```go
   func NewCmd<Name>(f *cmdutil.Factory) *cobra.Command {
       opts := &CmdOptions{}
       cmd := &cobra.Command{
           Use:   "<cmdname>",
           Short: "Brief description",
           Example: `  # Basic usage

  clawker <cmdname>

# With agent flag

  clawker <cmdname> --agent ralph`,
           RunE: func(cmd *cobra.Command, args []string) error {
               return run<Name>(f, opts)
           },
       }
       cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name")
       // Add flag validation if needed
       // cmd.MarkFlagsOneRequired("name", "project")
       return cmd
   }

   ```
4. Register in `pkg/cmd/root/root.go`:
   ```go
   import "<package>/pkg/cmd/<cmdname>"
   // In NewCmdRoot:
   cmd.AddCommand(<cmdname>.NewCmd<Name>(f))
   ```

## Container Command Pattern (New Architecture)

For commands that operate on containers. Use `internal/docker.Client` (wraps `pkg/whail.Engine`).

**CRITICAL:** Always use `f.Client(ctx)` from the Factory. Never call `docker.NewClient(ctx)` directly.

```go
func runCmd(f *cmdutil.Factory, opts *CmdOptions) error {
    ctx := context.Background()
    cfg, _ := f.Config()

    // ALWAYS use Factory's Client method
    client, err := f.Client(ctx)
    if err != nil {
        cmdutil.HandleError(err)
        return err
    }
    // Do NOT call defer client.Close() - Factory manages lifecycle via CloseClient() in main

    var containerID string
    if opts.Agent != "" {
        // Find specific agent
        containerName, container, err := client.FindContainerByAgent(ctx, cfg.Project, opts.Agent)
        if err != nil {
            return err
        }
        if container == nil {
            return fmt.Errorf("container %s not found", containerName)
        }
        containerID = container.ID
    } else {
        // Find all containers for project
        containers, err := client.ListContainersByProject(ctx, cfg.Project, true)
        if err != nil {
            return err
        }
        if len(containers) == 0 {
            return fmt.Errorf("no containers found")
        }
        if len(containers) > 1 {
            return fmt.Errorf("multiple containers, use --agent")
        }
        containerID = containers[0].ID
    }

    // Use whail methods directly via embedded Engine
    err = client.ContainerStop(ctx, containerID, nil)
    // ... or client.ContainerKill, ContainerRestart, etc.
}
```

## Management Command Pattern (Docker CLI-style)

For Docker CLI-compatible subcommands that operate on resources by name:

```go
func NewCmdXxx(f *cmdutil.Factory) *cobra.Command {
    cmd := &cobra.Command{
        Use:     "xxx RESOURCE [RESOURCE...]",
        Aliases: []string{"alias"},
        Args:    cobra.MinimumNArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := context.Background()

            // ALWAYS use Factory's Client method - never docker.NewClient directly
            client, err := f.Client(ctx)
            if err != nil {
                cmdutil.HandleError(err)
                return err
            }
            // Note: Do NOT call defer client.Close() - Factory manages client lifecycle

            for _, name := range args {
                // Operations use whail methods via embedded Engine
                if err := client.ContainerXxx(ctx, name); err != nil {
                    fmt.Fprintf(os.Stderr, "Error: %s: %v\n", name, err)
                    continue
                }
                fmt.Printf("%s\n", name)
            }
            return nil
        },
    }
    return cmd
}
```

**IMPORTANT:** Never use `docker.NewClient(ctx)` directly in command files. Always use `f.Client(ctx)` from the Factory, which provides:
- Lazy initialization with `sync.Once` caching
- Consistent client lifecycle management
- Centralized connection handling

## Creating Containers with Labels

**Always pass `ctx` to manager methods:**

```go
containerCfg := engine.ContainerConfig{
    Name:        engine.ContainerName(cfg.Project, agentName),
    Image:       imageTag,
    Labels:      engine.ContainerLabels(cfg.Project, agentName, version, imageTag, workDir),
    // ... other config
}
containerMgr := engine.NewContainerManager(eng)
containerID, _ := containerMgr.Create(ctx, containerCfg)  // Pass ctx
```

## Workspace Strategy Pattern (Used in container run/create)

**Container commands use workspace mounts automatically:**

```go
// In run() function after resolving containerName:

// Setup workspace mounts
var workspaceMounts []mount.Mount

// Get host path (current working directory)
hostPath, err := os.Getwd()
if err != nil {
    return fmt.Errorf("failed to get working directory: %w", err)
}

// Determine workspace mode (CLI flag overrides config default)
modeStr := opts.Mode
if modeStr == "" {
    modeStr = cfg.Workspace.DefaultMode
}

mode, err := config.ParseMode(modeStr)
if err != nil {
    cmdutil.PrintError("Invalid workspace mode: %v", err)
    return err
}

// Create workspace strategy
wsCfg := workspace.Config{
    HostPath:    hostPath,
    RemotePath:  cfg.Workspace.RemotePath,
    ProjectName: cfg.Project,
    AgentName:   agent,
}

strategy, err := workspace.NewStrategy(mode, wsCfg)
if err != nil {
    return err
}

// Prepare workspace resources (important for snapshot mode)
if err := strategy.Prepare(ctx, client); err != nil {
    cmdutil.PrintError("Failed to prepare workspace: %v", err)
    return err
}

// Get workspace mount
workspaceMounts = append(workspaceMounts, strategy.GetMounts()...)

// Ensure and get config volumes
if err := workspace.EnsureConfigVolumes(ctx, client, cfg.Project, agent); err != nil {
    cmdutil.PrintError("Failed to create config volumes: %v", err)
    return err
}
workspaceMounts = append(workspaceMounts, workspace.GetConfigVolumeMounts(cfg.Project, agent)...)

// Add docker socket mount if enabled
if cfg.Security.DockerSocket {
    workspaceMounts = append(workspaceMounts, workspace.GetDockerSocketMount())
}

// Pass mounts to buildConfigs
containerConfig, hostConfig, networkConfig, err := buildConfigs(opts, workspaceMounts)
```

**Key points:**
- `--mode` flag overrides `workspace.default_mode` from config
- Default is "bind" mode if not specified
- Config volumes (claude config and history) always created
- buildConfigs() accepts mounts parameter: `func buildConfigs(opts *Options, mounts []mount.Mount)`

## Exit Code Handling Pattern

When a command needs to exit with a specific code but allow deferred cleanup to run:

```go
// Define ExitError type
type ExitError struct {
    Code int
}

func (e *ExitError) Error() string {
    return fmt.Sprintf("container exited with code %d", e.Code)
}

// Use named return to handle exit after defers
func runCmd(f *cmdutil.Factory, opts *Options) (retErr error) {
    defer func() {
        var exitErr *ExitError
        if errors.As(retErr, &exitErr) {
            os.Exit(exitErr.Code)  // Runs after all defers complete
        }
    }()

    // ... deferred cleanup ...
    defer cleanup()

    // Return ExitError instead of calling os.Exit directly
    if exitCode != 0 {
        return &ExitError{Code: exitCode}
    }
    return nil
}
```

## Quiet/JSON Output Pattern

For commands that support scripting with `--quiet` and `--json` flags:

```go
type Options struct {
    Quiet bool
    JSON  bool
}

// Add flags
cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Suppress informational output")
cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output as JSON")

// Use in command
if opts.JSON {
    return cmdutil.OutputJSON(map[string]string{"key": "value"})
}
if !opts.Quiet {
    fmt.Fprintf(os.Stderr, "Status message\n")
}
```

## Flag Validation Pattern

For flags that depend on other flags:

```go
// In runCmd, after config validation
if opts.ShellUser != "" && !opts.Shell {
    return fmt.Errorf("--user requires --shell flag")
}
if opts.Detach && opts.Remove {
    cmdutil.PrintWarning("--remove has no effect with --detach")
}
```

## Testing

```bash
# Build
go build -o bin/clawker ./cmd/clawker

# Run all tests (short mode)
go test ./...

# Run specific package tests
go test -v ./pkg/whail/...

# Static analysis
go vet ./...
go fmt ./...
```

## Mock Docker Client Pattern (Unit Tests)

For unit testing code that needs Docker without a real daemon:

```go
import (
    "context"
    "testing"

    "github.com/schmitthub/clawker/internal/testutil"
    "github.com/schmitthub/clawker/pkg/whail"
    "github.com/stretchr/testify/require"
    "go.uber.org/mock/gomock"
)

func TestImageResolution(t *testing.T) {
    ctx := context.Background()
    m := testutil.NewMockDockerClient(t)

    // IMPORTANT: Use whail types, NOT moby types directly
    m.Mock.EXPECT().
        ImageList(gomock.Any(), gomock.Any()).
        Return(whail.ImageListResult{
            Items: []whail.ImageSummary{
                {RepoTags: []string{"clawker-myproject:latest"}},
            },
        }, nil).
        AnyTimes()

    // Pass m.Client to code under test
    result, err := cmdutil.ResolveImageWithSource(ctx, m.Client, cfg, settings)
    require.NoError(t, err)
}
```

**Key points:**
- Use `whail.ImageListResult` and `whail.ImageSummary` (NOT moby types)
- `m.Mock` for setting expectations, `m.Client` for passing to code
- Regenerate mocks with `make generate-mocks`

## Whail Engine Method Pattern

When adding new methods to `pkg/whail`:

```go
// All container methods follow this pattern:
func (e *Engine) ContainerXxx(ctx context.Context, containerID string, ...) error {
    // 1. Check if container is managed
    isManaged, err := e.IsContainerManaged(ctx, containerID)
    if err != nil {
        return ErrContainerXxxFailed(containerID, err)
    }
    if !isManaged {
        return ErrContainerNotFound(containerID)
    }

    // 2. Perform the operation
    if err := e.APIClient.ContainerXxx(ctx, containerID, ...); err != nil {
        return ErrContainerXxxFailed(containerID, err)
    }
    return nil
}
```

**Key patterns:**
- Always check `IsContainerManaged` first for operations on existing containers
- Return `ErrContainerNotFound` for unmanaged containers (not a Docker error)
- Wrap errors with domain-specific error types from `errors.go`
- For methods returning data, return empty struct/nil on error before the operation

**Return types:**
- `ContainerStats` returns `io.ReadCloser` (caller must close)
- `ContainerStatsOneShot` returns `container.StatsResponseReader` (has Body and OSType)
- `CopyFromContainer` returns `(io.ReadCloser, container.PathStat, error)`
- Use `container.PathStat` not `types.ContainerPathStat` (deprecated)

## Channel-Based Method Pattern (ContainerWait)

For methods returning channels, wrap SDK errors in a goroutine:

```go
func (e *Engine) ContainerWait(ctx context.Context, containerID string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error) {
    errCh := make(chan error, 1)

    // 1. Check managed status first
    isManaged, err := e.IsContainerManaged(ctx, containerID)
    if err != nil {
        errCh <- ErrContainerWaitFailed(containerID, err)
        close(errCh)
        return nil, errCh  // Return nil for response channel
    }
    if !isManaged {
        errCh <- ErrContainerNotFound(containerID)
        close(errCh)
        return nil, errCh
    }

    // 2. Get channels from SDK
    waitCh, rawErrCh := e.APIClient.ContainerWait(ctx, containerID, condition)

    // 3. Wrap SDK errors in goroutine for consistent UX
    wrappedErrCh := make(chan error, 1)
    go func() {
        defer close(wrappedErrCh)
        if err := <-rawErrCh; err != nil {
            wrappedErrCh <- ErrContainerWaitFailed(containerID, err)
        }
    }()

    return waitCh, wrappedErrCh
}
```

**Key learnings:**
- Return `nil` for response channel when container is unmanaged (callers must check!)
- Always close error channel after sending error
- Use buffered channels (`make(chan error, 1)`) to prevent goroutine leaks
- Wrap SDK errors in goroutine to maintain consistent error formatting

## IsContainerManaged Behavior

**Important:** `IsContainerManaged` returns `(false, nil)` when container doesn't exist:

```go
func (e *Engine) IsContainerManaged(ctx context.Context, containerID string) (bool, error) {
    info, err := e.APIClient.ContainerInspect(ctx, containerID)
    if err != nil {
        if client.IsErrNotFound(err) {
            return false, nil  // NOT AN ERROR - container just doesn't exist
        }
        return false, ErrContainerInspectFailed(containerID, err)  // Wrap other errors
    }
    // Check label...
}
```

**Implications:**
- Callers cannot distinguish "not found" from "exists but unmanaged"
- Both cases result in `ErrContainerNotFound` from calling methods
- This is intentional: from user's perspective, unmanaged = doesn't exist
- Document this behavior when writing methods that use `IsContainerManaged`

## Test Helper Pattern

Test files in the same package share helpers. **Do not duplicate:**

```go
// container_test.go - define shared output
func generateContainerName(prefix string) string {
    return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func setupManagedContainer(ctx context.Context, t *testing.T, name string) string { ... }
func setupUnmanagedContainer(ctx context.Context, t *testing.T, name string, labels map[string]string) string { ... }

// copy_test.go - USE the shared output, don't redefine
func TestCopyToContainer(t *testing.T) {
    containerName := generateContainerName("test-copy")  // Uses shared helper
    // ...
}
```

## Key Files to Check When Making Changes

**Current Architecture (Migration Complete):**
- Whail Engine (reusable): `pkg/whail/engine.go`, `container.go`, `volume.go`, `network.go`, `image.go`, `copy.go`
- Whail Labels: `pkg/whail/labels.go`
- Whail Errors: `pkg/whail/errors.go`
- Clawker Client: `internal/docker/client.go` (wraps whail with clawker labels)
- Clawker Labels: `internal/docker/labels.go`
- Clawker Names: `internal/docker/names.go`
- Clawker Volume Helpers: `internal/docker/volume.go`
- Factory: `pkg/cmdutil/factory.go` (use `f.Client(ctx)`)
- Command Registration: `pkg/cmd/root/root.go`
- Workspace Setup: `internal/workspace/strategy.go` (uses `docker.Client`)
- Build Orchestration: `internal/build/build.go` (uses `docker.Client`)
- Tests: `pkg/whail/*_test.go`, `internal/docker/*_test.go`

**Note:** `internal/engine/` has been **deleted**. All code uses the above paths.
