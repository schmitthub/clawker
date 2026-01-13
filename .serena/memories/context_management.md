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

## Engine Package Method Signatures

All `internal/engine` methods follow this pattern:

```go
// Engine methods (internal/engine/client.go)
func (e *Engine) ContainerCreate(ctx context.Context, config *container.Config, ...) (Response, error)
func (e *Engine) ContainerStart(ctx context.Context, containerID string) error
func (e *Engine) VolumeExists(ctx context.Context, name string) (bool, error)
func (e *Engine) ImagePull(ctx context.Context, imageRef string) (io.ReadCloser, error)

// ContainerManager methods (internal/engine/container.go)
func (cm *ContainerManager) Create(ctx context.Context, cfg ContainerConfig) (string, error)
func (cm *ContainerManager) Start(ctx context.Context, containerID string) error
func (cm *ContainerManager) FindOrCreate(ctx context.Context, cfg ContainerConfig) (string, bool, error)

// VolumeManager methods (internal/engine/volume.go)
func (vm *VolumeManager) EnsureVolume(ctx context.Context, name string, labels map[string]string) (bool, error)
func (vm *VolumeManager) CopyToVolume(ctx context.Context, volumeName, srcDir, destPath string, ignorePatterns []string) error

// ImageManager methods (internal/engine/image.go)
func (im *ImageManager) EnsureImage(ctx context.Context, imageRef string) error
func (im *ImageManager) BuildImage(ctx context.Context, buildContext io.Reader, opts BuildImageOpts) error
```

## CLI Command Pattern

```go
func runCommand(f *cmdutil.Factory, opts *Options) error {
    ctx := context.Background()  // Or use term.SetupSignalContext for cancellation
    
    eng, err := engine.NewEngine(ctx)
    if err != nil {
        return err
    }
    defer eng.Close()
    
    // Pass ctx to all engine operations
    containers, err := eng.ListClawkerContainers(ctx, true)
    containerMgr := engine.NewContainerManager(eng)
    containerMgr.Start(ctx, containerID)
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
        
        containerMgr.Remove(cleanupCtx, containerID, true)
        eng.VolumeRemove(cleanupCtx, volumeName, true)
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
