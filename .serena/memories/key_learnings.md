**Key Learnings**:
  - Channel-based methods like `ContainerWait` need goroutines to wrap SDK errors
  - Test helper functions should not be duplicated across test files in same package
  - `IsContainerManaged` silently returns `(false, nil)` for not-found - document this behavior
  - Cobra shows parent commands without subcommands under "Additional help topics"
  - Once subcommands are added, they move to "Available Commands"
  - Commands use positional args for container names (Docker-like)
  - Helper function `splitArgs` shared across test files in same package
  - Commands MUST USE `internal/docker.Client` instead of legacy `internal/engine` or `github.com/moby/moby/client.Client`
  - Terminal visual state (alternate screen buffer, cursor visibility, text attributes) is separate from termios mode. `term.Restore()` sends ANSI escape sequences (`\x1b[?1049l\x1b[?25h\x1b[0m\x1b(B`) to reset visual state before restoring termios after container detach/exit.
  - Never bypass whail - scaffold with TODO if method missing
  - Stats streaming requires goroutines for concurrent container stat collection
  - Memory size parsing needs case-insensitive suffix handling
  - Cobra interprets args starting with `-` as flags; use `--` separator or avoid such test inputs
  - exec command must check container is running before creating exec instance
  - cp command uses tar archives for file transfer; handle both copy directions
  - attach command detects container TTY from ContainerInspect
  - Subcommands go in their own subpackages (volume/list/list.go not volume/list.go)
  - shlex.Split strips quotes, so test expected values shouldn't include quotes
  - prune workaround: list+remove individual volumes instead of waiting for VolumesPrune
  - Global flag `-D/--debug` reserves `-D` shorthand; don't reuse it in subcommands
  - VolumesPrune needs `all=true` filter to prune named volumes (Docker default only prunes anonymous)
  - Test ordering matters: tests that remove resources affect later tests using same resources
  - Container create/run reuse buildConfigs for Docker config construction
  - Run command must handle both TTY and non-TTY I/O with proper channel selection
  - Test slices: GetStringArray returns empty slice not nil; use helper to compare nil==[]
  - Docker SDK ImageBuildOptions does not have Platform field; SDK version determines available fields
  - Alias pattern for Cobra: create thin wrapper that calls underlying command and overrides Examples
  - StringArray flags for multi-value inputs (--tag, --build-arg, --label) need parsing to maps
  - Label merging: user labels first, then clawker labels (clawker takes precedence)
  - Build args support KEY without value (nil pointer) for env passthrough
  - Deprecated flags: use MarkHidden + MarkDeprecated for backward compatibility
  - Host proxy pattern: HTTP server on localhost, containers connect via host.docker.internal
  - Host proxy enables container-to-host actions like opening URLs in browser
  - Factory pattern for hostproxy: lazy init with sync.Once, EnsureRunning() before container commands
  - BROWSER env var set to /usr/local/bin/host-open so CLI tools use host proxy automatically
  - Terminal attach TUI redraw: Use Docker CLI's +1/-1 resize trick to force redraw. Resize to (height+1, width+1) then back to (height, width) - this forces SIGWINCH and triggers TUI redraw. See docker/cli attach.go resizeTTY(). Implemented in StreamWithResize.

## Testing Infrastructure Learnings

- **Build tags for test isolation**: Use `//go:build integration` and `//go:build e2e` to separate tests by Docker requirement
- **Test naming**: `*_integration_test.go` for Docker tests, `*_e2e_test.go` for binary execution tests
- **Never silent discard errors in cleanup**: Use `errors.Join()` to aggregate, or `t.Logf()` for warnings
- **Cleanup context**: Always use `context.Background()` in `t.Cleanup()` since original context may be cancelled
- **Agent name uniqueness**: Include timestamp + random suffix for parallel test safety: `fmt.Sprintf("test-%s-%d", time.Now().Format("150405"), rand.Intn(10000))`
- **Container readiness fail-fast**: Check container state in readiness loops - return error immediately if container exited
- **Log streaming connection errors**: Connection reset/broken pipe indicates container died, not transient error
- **Both invocation patterns**: Always test `--agent flag` AND `container name` patterns for all container commands
- **FindProjectRoot utility**: Use `testutil.FindProjectRoot()` instead of duplicating - uses `runtime.Caller()` for reliability
- **Test image cleanup**: `BuildTestImage` automatically registers `t.Cleanup()` for image removal
- **Readiness timeout constants**: `DefaultReadyTimeout` (60s local), `CIReadyTimeout` (120s CI), `E2EReadyTimeout` (180s E2E)

## Ralph Command Implementation Learnings

- **Non-TTY exec for output capture**: Set `Tty: false` in ExecCreateOptions to get proper stdout/stderr multiplexing. Docker adds 8-byte header to each frame.
- **Circuit breaker pattern**: Use mutex for thread safety, track consecutive no-progress loops, trip immediately on BLOCKED status
- **RALPH_STATUS parsing**: Use regex with `(?s)` DOTALL flag: `(?s)---RALPH_STATUS---(.+?)---END_RALPH_STATUS---`
- **Session persistence**: Store to `~/.local/clawker/ralph/sessions/` and `circuit/` directories as JSON
- **Time pointer for omitempty**: Change `TrippedAt time.Time` to `*time.Time` to avoid "omitempty has no effect" warning
- **ExecInspect needs options**: `client.ExecInspect(ctx, execID, docker.ExecInspectOptions{})` - second arg required
- **Config wiring**: Check if CLI flag is at default value before applying config default (avoids overwriting explicit CLI choices)
- **Subcommand registration**: Parent command adds subcommands via `cmd.AddCommand()` in NewCmd function
