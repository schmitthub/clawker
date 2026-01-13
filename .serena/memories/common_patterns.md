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

## Container Command Pattern

For commands that operate on containers (logs, stop, etc.):

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
        existing, _ := eng.FindContainerByName(containerName)
        if existing == nil {
            return fmt.Errorf("container not found")
        }
        containerID = existing.ID
    } else {
        // Find containers for project
        containers, _ := eng.ListClawkerContainersByProject(cfg.Project, true)
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

```go
containerCfg := engine.ContainerConfig{
    Name:        engine.ContainerName(cfg.Project, agentName),
    Image:       imageTag,
    Labels:      engine.ContainerLabels(cfg.Project, agentName, version, imageTag, workDir),
    // ... other config
}
containerMgr := engine.NewContainerManager(eng)
containerID, _ := containerMgr.Create(containerCfg)
```

## Workspace Strategy Pattern

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
strategy.Prepare(ctx, eng)
mounts := strategy.GetMounts()
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

## Key Files to Check When Making Changes

- Container operations: `internal/engine/container.go`, `client.go`
- Naming/labels: `internal/engine/names.go`, `labels.go`
- Workspace setup: `internal/workspace/strategy.go`
- Command registration: `pkg/cmd/root/root.go`
- Tests: `internal/engine/container_test.go`
