# Container Attach Command

Attaches local stdin/stdout/stderr to a running container. Supports TTY mode with terminal resize and non-TTY mode with stdcopy demultiplexing.

## Key Files

| File | Purpose |
|------|---------|
| `attach.go` | Command definition and `attachRun` implementation |
| `attach_test.go` | Tier 1 (flag parsing) + Tier 2 (Cobra+Factory) tests |

## Flow

1. **Resolve container name** — `--agent` flag resolves to `clawker.<project>.<agent>`
2. **Connect to Docker** — `opts.Client(ctx)`
3. **Find container** — `FindContainerByName` + verify running state
4. **Start host proxy** — enables container-to-host actions (browser opening, etc.)
5. **Inspect container** — determine TTY mode from `Config.Tty`
6. **Attach** — `ContainerAttach` returns hijacked connection
7. **Handle I/O** — TTY path (Stream + resize) or non-TTY path (stdcopy demux)

## TTY Mode: Stream + Resize Pattern

The TTY path separates I/O streaming from resize handling (canonical pattern from `start.go`):

```go
// 1. Start I/O streaming in goroutine
streamDone := make(chan error, 1)
go func() { streamDone <- pty.Stream(ctx, hijacked.HijackedResponse) }()

// 2. Resize immediately (container already running)
//    +1/-1 trick forces SIGWINCH for TUI redraw on re-attach
resizeFunc(uint(height+1), uint(width+1))
resizeFunc(uint(height), uint(width))

// 3. Monitor for window resize events (SIGWINCH)
resizeHandler := signals.NewResizeHandler(resizeFunc, pty.GetSize)
resizeHandler.Start()
defer resizeHandler.Stop()

// 4. Wait for stream completion
return <-streamDone
```

**Key difference from `start.go`**: No attach-before-start ordering concern. The container is already running, so I/O and resize can start immediately. No `waitForContainerExit` or detach timeout needed.

## Non-TTY Mode

Uses `stdcopy.StdCopy` to demultiplex Docker's multiplexed stdout/stderr stream. Stdin is forwarded via `io.Copy` unless `--no-stdin`.

## Error Handling

- Docker connection errors: `return fmt.Errorf("connecting to Docker: %w", err)` — centralized in Main()
- Attach errors: `return fmt.Errorf("attaching to container: %w", err)` — centralized in Main()
- Container not found/not running: `return fmt.Errorf(...)` — descriptive messages

## Dependencies

- `internal/docker` — PTYHandler, ContainerAttach, ContainerResize, ContainerInspect
- `internal/signals` — ResizeHandler for SIGWINCH monitoring
- `internal/logger` — Debug logging for resize failures
- `internal/hostproxy` — Host proxy for container-to-host communication

## Testing

- **Tier 1**: Flag parsing via `runF` trapdoor (no Docker)
- **Tier 2**: Cobra+Factory with `dockertest.FakeClient` — tests Docker connection error, container not found, container not running, non-TTY happy path
- TTY path requires real terminal — covered by integration tests in `test/commands/`
