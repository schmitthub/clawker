# Code Style & Conventions

## Guidelines

- When building new features for the CLI or refactoring, review `.claude/docs/cli-guidelines.md`
- Use `cmdutil` output functions for all user-facing messages (never raw `fmt.Print` to stdout)
- Use `cmdutil.HandleError(err)` for Docker errors to get rich formatting
- Use `cmdutil.PrintNextSteps(...)` for actionable guidance
- All errors/warnings go to stderr via `cmdutil.PrintError()` / `cmdutil.PrintWarning()`

## Logging

- Use `zerolog` for all logging (never `fmt.Print` for debug)
- Import via `github.com/schmitthub/claucker/pkg/logger`

## Error Handling

- Errors must include actionable "Next Steps" guidance for users
- Use `DockerError` type with `FormatUserError()` for Docker-related errors

## Project Layout

- Standard Go project layout: `cmd/`, `internal/`, `pkg/`
- Use interfaces for testability (especially Docker client)

## CLI Commands

- Create in `pkg/cmd/<cmdname>/`
- Use Cobra's `RunE` pattern
- Register in root command

## Important Gotchas

- `os.Exit()` does NOT run deferred functions - always restore terminal state explicitly before calling os.Exit
- In raw terminal mode, Ctrl+C does NOT generate SIGINT - input goes directly to the container
- PTY streaming returns immediately when output closes - never wait for stdin goroutine (may be blocked on Read())
- Docker hijacked connections require proper cleanup of both read and write sides
