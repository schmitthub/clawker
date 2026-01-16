# Go Context Management Patterns

## Critical Antipattern: Stored Context in Structs

**NEVER** store `context.Context` as a struct field to be reused across method calls:

```go
// ❌ WRONG - Static context antipattern
type Engine struct {
    cli *client.Client
    ctx context.Context  // DO NOT DO THIS
}

func NewEngine(ctx context.Context) *Engine {
    return &Engine{
        cli: client,
        ctx: ctx,  // Storing context for later reuse
    }
}

func (e *Engine) DoOperation(args string) error {
    return e.cli.Operation(e.ctx, args)  // Reusing stored context
}
```

**Problems with this pattern:**
1. Context should represent the lifetime of a single operation, not a long-lived object
2. Cancellation/timeout won't work properly for individual operations
3. Makes it impossible to have different timeouts for different operations
4. Violates Go's context guidelines

## Correct Pattern: Per-Operation Context

**ALWAYS** pass `context.Context` as the first parameter to methods that perform I/O:

```go
// ✅ CORRECT - Per-operation context
type Engine struct {
    cli *client.Client
    // NO ctx field
}

func NewEngine(ctx context.Context) (*Engine, error) {
    // Use ctx only for initialization (e.g., health check)
    if err := client.Ping(ctx); err != nil {
        return nil, err
    }
    return &Engine{cli: client}, nil
}

func (e *Engine) DoOperation(ctx context.Context, args string) error {
    return e.cli.Operation(ctx, args)  // Context passed per-operation
}
```

## Current Package Method Signatures (Post-Migration)

All `pkg/whail` and `internal/docker` methods follow this pattern:

```go
// whail.Engine methods (pkg/whail/container.go)
func (e *Engine) ContainerCreate(ctx context.Context, config *container.Config, ...) (Response, error)
func (e *Engine) ContainerStart(ctx context.Context, containerID string) error
func (e *Engine) ContainerStop(ctx context.Context, containerID string, timeout *int) error
func (e *Engine) ContainerKill(ctx context.Context, containerID, signal string) error
func (e *Engine) ContainerRemove(ctx context.Context, containerID string, force bool) error

// whail.Engine methods (pkg/whail/volume.go)
func (e *Engine) VolumeCreate(ctx context.Context, name string, labels map[string]string) error
func (e *Engine) VolumeExists(ctx context.Context, name string) (bool, error)
func (e *Engine) VolumeRemove(ctx context.Context, name string, force bool) error

// docker.Client methods (internal/docker/client.go) - wraps whail.Engine
func (c *Client) ListContainers(ctx context.Context, includeAll bool) ([]Container, error)
func (c *Client) ListContainersByProject(ctx context.Context, project string, includeAll bool) ([]Container, error)
func (c *Client) FindContainerByAgent(ctx context.Context, project, agent string) (*Container, error)
func (c *Client) RemoveContainerWithVolumes(ctx context.Context, containerID string, force bool) error

// docker.Client volume helpers (internal/docker/volume.go)
func (c *Client) EnsureVolume(ctx context.Context, name string, labels map[string]string) (bool, error)
func (c *Client) CopyToVolume(ctx context.Context, volumeName, srcDir, destPath string, ignorePatterns []string) error
```

## CLI Command Pattern

```go
func runCommand(f *cmdutil.Factory, opts *Options) error {
    ctx := context.Background()  // Or use term.SetupSignalContext for cancellation
    
    // Use docker.NewClient for clawker-specific operations
    client, err := docker.NewClient(ctx)
    if err != nil {
        return err
    }
    defer client.Close()
    
    // Pass ctx to all client operations
    containers, err := client.ListContainers(ctx, true)
    client.ContainerStart(ctx, containerID)
    
    // Or use f.Client(ctx) for factory-managed client
    client, err := f.Client(ctx)
    defer f.CloseClient()
}
```

## Cleanup Context Pattern

When cleanup runs in deferred functions, use a fresh context since the original may be cancelled:

```go
func runCommand(...) error {
    ctx, cancel := term.SetupSignalContext(context.Background())
    defer cancel()
    
    defer func() {
        // Use background context for cleanup - original ctx may be cancelled
        cleanupCtx := context.Background()
        // Or with timeout:
        cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        
        client.ContainerRemove(cleanupCtx, containerID, true)
        client.VolumeRemove(cleanupCtx, volumeName, true)
    }()
    
    // ... main operation using ctx
}
```

## When to Use Context

- **DO** pass context to any method that:
  - Makes network calls (Docker API, HTTP requests)
  - Performs I/O operations
  - May need cancellation or timeout support

- **DON'T** pass context to:
  - Pure computation functions
  - Simple getters/setters
  - Methods that only manipulate in-memory data

## Refactoring Checklist

When adding context to existing code:
1. Add `ctx context.Context` as first parameter
2. Update all call sites to pass context
3. Use `context.Background()` for cleanup contexts
4. Remove any stored `ctx` fields from structs
5. Run `go build ./...` to find missing updates
6. Run `go test ./...` to verify tests still pass
