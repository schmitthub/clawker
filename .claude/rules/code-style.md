---
description: Code style guidelines for the clawker codebase
---

# Code Style

## Logging
- `zerolog` is for **file logging only** — never for user-visible output
- File logging to `~/.local/clawker/logs/clawker.log` with rotation (50MB, 7 days, 3 backups)
- User-visible output uses `fmt.Fprintf` to IOStreams (`ios.ErrOut` for status, `ios.Out` for data)
- `logger.Debug()` / `logger.Warn()` are fine for diagnostic file logs
- Project/agent context: `logger.SetContext(project, agent)` adds structured fields
- Never use `logger.Fatal()` in Cobra hooks — return errors instead

## Whail Client Enforcement
- No package imports APIClient from `github.com/moby/moby/client` directly except `pkg/whail`
- No package imports `pkg/whail` directly except `internal/docker`
- `pkg/whail` decorates moby client, exposing the same interface — all moby methods available through whail
- It is ok to import `github.com/moby/moby/api/types` and related types directly as needed

## Terminal Gateway
- Only `internal/term` imports `golang.org/x/term` — no other package should
- Use `term.IsTerminalFd(fd)` and `term.GetTerminalSize(fd)` instead of `x/term` directly
- `internal/term` is a leaf package (stdlib + `x/term` only, zero `internal/` imports)

## Presentation Layer

### Library Import Boundaries
- Only `internal/iostreams` imports `lipgloss` — no other package should
- Only `internal/tui` imports `bubbletea` and `bubbles` — no other package should

### Output Scenarios

Commands fall into one of four output scenarios. Choose imports accordingly:

| Scenario | Description | Packages | Example |
|----------|-------------|----------|---------|
| Non-interactive / static | Print and done. Status messages, tables, results. | `iostreams` + `fmt` | `fmt.Fprintf(ios.ErrOut, "Warning: %s\n", msg)` |
| Static-interactive | Static streaming output with y/n prompts mid-flow. | `iostreams` + `prompter` | Config confirmation, `image prune` |
| Live-display | No user input, but continuous rendering with layout management. | `iostreams` + `tui` | `image build` progress display |
| Live-interactive | Full keyboard/mouse input, stateful navigation. | `iostreams` + `tui` | `monitor up` |

### Rules
- `iostreams` is foundational — every command imports it
- `tui` is additive — import alongside iostreams for live display/interactive scenarios
- A command may import both `iostreams` and `tui`
- Commands access TUI via `f.TUI` (Factory noun), not by calling tui package functions directly
- zerolog is for file logging only — user-visible output uses `fmt.Fprintf` to IOStreams

## Output Conventions (gh-style)

**Pattern**: Follow GitHub CLI (`gh`) conventions — `fmt.Fprintf` with `ios.ColorScheme()` directly.

```go
cs := ios.ColorScheme()
fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.WarningIcon(), "BuildKit is not available")
```

- **Tables**: `tableprinter.New(ios, headers...)` — never raw `tabwriter`
- **Semantic colors**: `cs.Primary/Success/Warning/Error()` via `ios.ColorScheme()`
- **Icons**: `cs.SuccessIcon()`, `cs.WarningIcon()`, `cs.FailureIcon()`, `cs.InfoIcon()`
- **Data output**: stdout only (for scripting, e.g., `ls` table output)
- **Status messages**: `fmt.Fprintf(ios.ErrOut, ...)` for human-readable stderr output
- **Errors**: Return typed errors to Main() for centralized rendering — never print errors directly
  - `return fmt.Errorf("context: %w", err)` — default error
  - `return cmdutil.FlagErrorf("bad flag: %s", val)` — triggers usage display
  - `return cmdutil.SilentError` — error already displayed
- **Warnings**: Inline `fmt.Fprintf(ios.ErrOut, "%s ...\n", cs.WarningIcon(), msg)` — not errors, not bubbled
- **Next steps**: Inline numbered guidance with `fmt.Fprintf(ios.ErrOut, ...)`

### Deprecated (do not use in new code)
- `cmdutil.HandleError`, `cmdutil.PrintError`, `cmdutil.PrintWarning`, `cmdutil.PrintNextSteps`
- `ios.PrintSuccess/Warning/Info/Failure()` — deleted
- `ios.RenderHeader/Divider/KeyValue/Status()` — deleted

## Cobra Commands
- Always use `PersistentPreRunE` (never `PersistentPreRun`)
- Always include `Example` field with indented examples
- Subpackages under `internal/cmd/<noun>/` are for subcommands only
- Exception: `opts/` package exists for import cycle avoidance (parent imports subcommands, subcommands need shared types)

## CLI Guidelines Reference
- Follow conventions from https://clig.dev/ for CLI design patterns
