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

```go
func runCmd(f *cmdutil.Factory, opts *CmdOptions) error {
    ctx := context.Background()
    cfg, _ := f.Config()
    
    // Use the new docker.Client
    client, err := f.Client(ctx)
    if err != nil {
        return err
    }
    defer f.CloseClient()

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

## Container Command Pattern (Legacy)

For commands still using `internal/engine` (being migrated).

```go
func runCmd(f *cmdutil.Factory, opts *CmdOptions) error {
    ctx := context.Background()
    cfg, _ := f.Config()
    eng, _ := engine.NewEngine(ctx)
    defer eng.Close()

    var containerID string
    if opts.Agent != "" {
        // Specific agent
        containerName := engine.ContainerName(cfg.Project, opts.Agent)
        existing, _ := eng.FindContainerByName(ctx, containerName)  // Pass ctx
        if existing == nil {
            return fmt.Errorf("container not found")
        }
        containerID = existing.ID
    } else {
        // Find containers for project
        containers, _ := eng.ListClawkerContainersByProject(ctx, cfg.Project, true)  // Pass ctx
        if len(containers) == 0 {
            return fmt.Errorf("no containers found")
        }
        if len(containers) > 1 {
            // Show available agents, ask user to specify
            return fmt.Errorf("multiple containers, use --agent")
        }
        containerID = containers[0].ID
    }
    // ... operate on containerID
}
```

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

## Workspace Strategy Pattern

**Pass `ctx` to Prepare and EnsureConfigVolumes:**

```go
// Setup workspace (bind or snapshot mode)
wsConfig := workspace.Config{
    HostPath:       workDir,
    RemotePath:     cfg.Workspace.RemotePath,
    ProjectName:    cfg.Project,
    AgentName:      agentName,  // Required for volume naming
    IgnorePatterns: ignorePatterns,
}
strategy, _ := workspace.NewStrategy(mode, wsConfig)
strategy.Prepare(ctx, eng)  // ctx passed through
mounts := strategy.GetMounts()

// Ensure config volumes exist
workspace.EnsureConfigVolumes(ctx, eng, cfg.Project, agentName)  // Pass ctx
```

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

# Run all tests
go test ./...

# Run specific package tests
go test -v ./internal/engine/...

# Static analysis
go vet ./...
go fmt ./...
```

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

## Key Files to Check When Making Changes

**New Architecture (preferred):**
- Container operations: `pkg/whail/container.go`, `pkg/whail/copy.go`
- Labels: `pkg/whail/labels.go`, `internal/docker/labels.go`
- Naming: `internal/docker/names.go`
- Client: `internal/docker/client.go`
- Factory: `pkg/cmdutil/factory.go` (use `f.Client(ctx)`)
- Errors: `pkg/whail/errors.go`
- Tests: `pkg/whail/container_test.go`, `internal/docker/client_test.go`

**Legacy (being migrated):**
- Container operations: `internal/engine/container.go`, `client.go`
- Naming/labels: `internal/engine/names.go`, `labels.go`
- Workspace setup: `internal/workspace/strategy.go`
- Command registration: `pkg/cmd/root/root.go`
- Tests: `internal/engine/container_test.go`
