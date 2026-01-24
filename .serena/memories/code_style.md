# Code Style & Conventions

## Guidelines

- When building new features for the CLI or refactoring, review `.claude/docs/cli-guidelines.md`
- Use `cmdutil` output functions for all user-facing messages (never raw `fmt.Print` to stdout)
- Use `cmdutil.HandleError(err)` for Docker errors to get rich formatting
- Use `cmdutil.PrintNextSteps(...)` for actionable guidance
- All errors/warnings go to stderr via `cmdutil.PrintError()` / `cmdutil.PrintWarning()`
- Use `cmdutil.PrintStatus(quiet, ...)` for status messages that respect `--quiet` flag
- Use `cmdutil.OutputJSON(data)` for JSON output when `--json` flag is set
- Docker client API methods must only be wrapped with managed label checks in @pkg/whail if they directly read, update, create, or delete Docker resources (containers, images, volumes, networks). Do NOT wrap lower-level methods that are used internally by these higher-level methods because the checks and added methods will be redundant. The whail client otherwise implements the Docker client interface so the unwrapped methods will be available for use as needed. For example `client.ExecStart` does not need to be wrapped because it only receives an ExecID that was created by `client.ExecCreate`, which is wrapped in whail with the managed label check.

## Logging

- Use `zerolog` for all logging (never `fmt.Print` for debug)
- Import via `github.com/schmitthub/clawker/internal/logger`

## Error Handling

- Errors must include actionable "Next Steps" guidance for users
- Use `DockerError` type with `FormatUserError()` for Docker-related errors

## Project Layout

- Standard Go project layout: `cmd/`, `internal/`, `pkg/`
- Use interfaces for testability (especially Docker client)

## CLI Commands

- Create in `internal/cmd/<cmdname>/`
- Use Cobra's `RunE` pattern
- Register in root command

## Cobra CLI Best Practices

- Always use `PersistentPreRunE` not `PersistentPreRun` - never use `logger.Fatal()` in hooks
- Always include `Example` field with formatted usage examples in all commands
- Route status messages to stderr (`fmt.Fprintln(os.Stderr, ...)`)
- Keep stdout clean for data output only (e.g., `ls` table for scripting)
- Use `cmd.MarkFlagsOneRequired()` and `cmd.MarkFlagsMutuallyExclusive()` for flag validation
- Use `cmdutil.HandleError(err)` for Docker errors to get rich formatting

## Configuration

- Always use `yaml.Marshal` when generating user yaml config files or templates off of Go structs (to ensure consistency with struct structure and tags)

## Important Gotchas

- **Always use `f.Client(ctx)` from Factory** - never call `docker.NewClient(ctx)` directly in command files. The Factory provides lazy initialization with `sync.Once` caching and manages client lifecycle via `CloseClient()` in `internal/clawker/cmd.go`.
- `os.Exit()` does NOT run deferred functions - use `ExitError` type with named returns to allow cleanup before exit
- In raw terminal mode, Ctrl+C does NOT generate SIGINT - input goes directly to the container
- PTY streaming returns immediately when output closes - never wait for stdin goroutine (may be blocked on Read())
- Docker hijacked connections require proper cleanup of both read and write sides
- Never use `logger.Fatal()` in Cobra hooks - it bypasses error handling
- Cobra's `MarkFlagsOneRequired()` must be called after flags are defined
- Use `context.Background()` as parent for cleanup contexts (passed context may be cancelled)
- Do not pass `context.Context` during struct initialization and re-use it for all of its member methods this is an anti-pattern. Do pass `context.Context` to each method individually
- Terminal attach resize: Use +1/-1 trick for TUI redraw on attach. Resize to (h+1,w+1) then (h,w) forces SIGWINCH. Implemented in `StreamWithResize`.
- Terminal visual state reset: When restoring terminal after container detach/exit, `term.Restore()` sends escape sequences BEFORE restoring termios (`\x1b[?1049l\x1b[?25h\x1b[0m\x1b(B`) to leave alternate screen, show cursor, reset colors, and select ASCII charset.
