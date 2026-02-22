# Container Exec Command

Executes a command in a running container. Supports TTY mode with terminal resize, non-TTY mode with stdcopy demultiplexing, and detached mode.

## Key Files

| File | Purpose |
|------|---------|
| `exec.go` | Command definition, `execRun` implementation, `checkExecExitCode` helper |
| `exec_test.go` | Tier 1 (flag parsing) + Tier 2 (Cobra+Factory) tests |

## Flow

1. **Resolve container name** — `--agent` flag resolves to `clawker.<project>.<agent>`
2. **Connect to Docker** — `opts.Client(ctx)`
3. **Find container** — `FindContainerByName` + verify running state
4. **Credential forwarding** — host proxy + git credentials + socket bridge env injection
5. **Create exec instance** — `ExecCreate` with command, env, workdir, user, TTY config
6. **Route by mode**:
   - **Detach**: `ExecStart` + print exec ID
   - **TTY**: PTY setup + Stream goroutine + resize handler + exit code check
   - **Non-TTY**: stdcopy demux + optional stdin forwarding + exit code check

## Credential Forwarding

Exec injects git credential env vars into exec'd processes automatically:

1. **Host proxy** — if enabled, `CLAWKER_HOST_PROXY=<url>` for HTTPS credential forwarding
2. **Git credentials** — `workspace.SetupGitCredentials()` provides env for HTTPS/SSH/GPG
3. **Socket bridge** — `SocketBridge.EnsureBridge()` starts/ensures SSH/GPG agent forwarding daemon

## TTY Mode: Stream + Resize Pattern

Uses the canonical pattern (shared with `start.go` and `attach.go`):

```go
// 1. Start I/O streaming in goroutine
streamDone := make(chan error, 1)
go func() { streamDone <- pty.Stream(ctx, hijacked.HijackedResponse) }()

// 2. Resize immediately (exec is on a running container)
//    +1/-1 trick forces SIGWINCH for TUI redraw
resizeFunc(uint(height+1), uint(width+1))
resizeFunc(uint(height), uint(width))

// 3. Monitor for window resize events (SIGWINCH)
resizeHandler := signals.NewResizeHandler(resizeFunc, pty.GetSize)
resizeHandler.Start()
defer resizeHandler.Stop()

// 4. Wait for stream, then check exit code
<-streamDone
checkExecExitCode(ctx, client, execID)
```

## Non-TTY Mode

Uses `stdcopy.StdCopy` to demultiplex Docker's multiplexed stdout/stderr stream. Stdin forwarded via `io.Copy` when `--interactive`.

## Exit Code Handling

`checkExecExitCode` inspects the exec instance and returns `fmt.Errorf("command exited with code %d")` for non-zero exits. Inspect failures are logged but don't fail the command.

## Error Handling

All errors use `return fmt.Errorf("context: %w", err)` for centralized rendering in Main():
- Docker connection: `"connecting to Docker: %w"`
- Container not found: `"failed to find container %q: %w"`
- Container not running: `"container %q is not running"`
- Exec create: `"creating exec instance: %w"`
- Detach start: `"starting detached exec: %w"`
- Attach: `"attaching to exec: %w"`

## Dependencies

- `internal/docker` — PTYHandler, ExecCreate/Start/Attach/Inspect/Resize
- `internal/signals` — ResizeHandler for SIGWINCH monitoring
- `internal/hostproxy` — Host proxy for credential forwarding
- `internal/socketbridge` — SSH/GPG agent socket forwarding
- `internal/workspace` — SetupGitCredentials for exec sessions
- Logging via `ios.Logger` (iostreams interface) — `checkExecExitCode` takes `log iostreams.Logger` parameter

## Testing

- **Tier 1**: Flag parsing via `runF` trapdoor — all flags, agent mode, args parsing
- **Tier 2**: Cobra+Factory with `dockertest.FakeClient` — Docker connection error, container not found, container not running, detach mode, non-TTY happy path, non-zero exit code
- TTY path requires real terminal — covered by integration tests in `test/commands/`
