---
description: Code style guidelines for the clawker codebase
---

# Code Style

## Logging
- Use `zerolog` only (never `fmt.Print` for debug output)
- File logging to `~/.local/clawker/logs/clawker.log` with rotation (50MB, 7 days, 3 backups)
- Interactive mode: `logger.SetInteractiveMode(true)` suppresses console logs, file logs continue
- Project/agent context: `logger.SetContext(project, agent)` adds structured fields
- `logger.Debug()` never suppressed; `Info/Warn/Error` suppressed on console in interactive mode
- Never use `logger.Fatal()` in Cobra hooks — return errors instead

## Whail Client Enforcement
- No package imports APIClient from `github.com/moby/moby/client` directly except `pkg/whail`
- No package imports `pkg/whail` directly except `internal/docker`
- `pkg/whail` decorates moby client, exposing the same interface — all moby methods available through whail
- It is ok to import `github.com/moby/moby/api/types` and related types directly as needed

## Presentation Layer Import Boundaries
- Only `internal/iostreams` imports `lipgloss` — no other package should
- Only `internal/tui` imports `bubbletea` and `bubbles` — no other package should
- Simple commands use `f.IOStreams` only — never import `tui`
- TUI commands (monitor) use `f.TUI` only — never import `iostreams`
- A command never imports both `iostreams` and `tui`

## Output Conventions
- Use `ios.NewTablePrinter()` for tables, never raw `tabwriter`
- Use `ios.PrintSuccess/Warning/Info/Failure()` for icon-prefixed status output (stderr)
- Use `cs.Primary/Success/Warning/Error()` for semantic coloring via `ios.ColorScheme()`
- Use `ios.RenderHeader/Divider/KeyValue/Status()` for structural stdout output
- Data output: stdout only (for scripting, e.g., `ls` table output)
- Errors: `cmdutil.HandleError(err)` for Docker errors, `ios.RenderError(err)` for styled errors

## Cobra Commands
- Always use `PersistentPreRunE` (never `PersistentPreRun`)
- Always include `Example` field with indented examples
- Subpackages under `internal/cmd/<noun>/` are for subcommands only
- Exception: `opts/` package exists for import cycle avoidance (parent imports subcommands, subcommands need shared types)

## CLI Guidelines Reference
- Follow conventions from https://clig.dev/ for CLI design patterns
