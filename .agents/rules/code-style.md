---
description: Code style guidelines for the clawker codebase
---

# Code Style

## Logging
- `zerolog` is for **file logging only** — never for user-visible output
- File logging to `cfg.LogsSubdir()/clawker.log` with rotation (50MB, 7 days, 3 backups)
- User-visible output uses `fmt.Fprintf` to IOStreams (`ios.Out` for data/status/success/next steps; `ios.ErrOut` for warnings; see format and live-output exceptions below)
- **Factory noun pattern**: Commands access logger via `f.Logger` (lazy closure on Options struct), resolve in run function. Never import `internal/logger` directly in command code for calling log methods
- **Library packages** accept `*logger.Logger` in constructors — never use globals
- **Tests** use `logger.Nop()` — no special test infrastructure needed
- **Don't swallow errors**: Always check `opts.Logger()` return — `log, err := opts.Logger(); if err != nil { return ... }`
- Project/agent context: `log.With("project", name, "agent", agent)` returns a sub-logger with structured fields
- Never use `logger.Fatal()` in Cobra hooks — return errors instead

## Whail Client Enforcement
- No package imports APIClient from `github.com/moby/moby/client` directly except `pkg/whail`
- No package imports `pkg/whail` directly except `internal/docker`
- `pkg/whail` decorates moby client, exposing the same interface — all moby methods available through whail
- Typed imports are fine anywhere: `github.com/moby/moby/client` and `github.com/moby/moby/api/types` for type names (`client.Filters`, `container.Config`, `filters.Args`, ...). The restriction covers only the `APIClient` connector.
- Exception: standalone daemon packages (`internal/hostproxy`, `internal/cmd/bridge`) and the CP daemon entrypoint (`cmd/clawkercp`, the `internal/controlplane` watcher) may import the `APIClient` connector directly. They need lightweight Docker API access without whail's label isolation.

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
| Non-interactive / static | Print and done. Data, status, results. | `iostreams` + `fmt` | `f.TUI.NewTable(headers...)` for data, `fmt.Fprintf(ios.Out, ...)` for status |
| Static-interactive | Static streaming output with y/n prompts mid-flow. | `iostreams` + `prompter` | Config confirmation, `image prune` |
| Live-display | No user input, but continuous rendering with layout management. | `iostreams` + `tui` | `image build` progress display |
| Live-interactive | Full keyboard/mouse input, stateful navigation. | `iostreams` + `tui` | `monitor up` |

### Rules
- `iostreams` is foundational — every command imports it
- `tui` is additive — import alongside iostreams for live display/interactive scenarios
- A command may import both `iostreams` and `tui`
- Commands access TUI via `f.TUI` (Factory noun), not by calling tui package functions directly
- zerolog is for file logging only — user-visible output uses `fmt.Fprintf` to IOStreams
- **TUI composition**: If you need a special view that doesn't exist, create a generic one in `tui` that can be customized or expanded upon in the command layer package you need it in. TUI is generic infrastructure — it never contains consumer-specific logic.

## Output Conventions (gh-style)

**Pattern**: Follow GitHub CLI (`gh`) conventions — `fmt.Fprintf` with `ios.ColorScheme()` directly.

```go
cs := ios.ColorScheme()
fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.WarningIcon(), "BuildKit is not available")
```

- **Tables**: `f.TUI.NewTable(headers...)` — never raw `tabwriter`
- **Semantic colors**: `cs.Primary/Success/Warning/Error()` via `ios.ColorScheme()`
- **Icons**: `cs.SuccessIcon()`, `cs.WarningIcon()`, `cs.FailureIcon()`, `cs.InfoIcon()`
- **User info**: `ios.Out` (stdout) — tables, IDs, JSON, command results, status messages ("Created X", "Removed Y"), success confirmations, next steps guidance
- **Warnings**: `ios.ErrOut` (stderr) — always visible regardless of piping
- **Errors**: Return typed errors to Main() for centralized rendering — never print errors directly
  - `return fmt.Errorf("context: %w", err)` — default error
  - `return cmdutil.FlagErrorf("bad flag: %s", val)` — triggers usage display
  - `return cmdutil.SilentError` — error already displayed
- **`--format` flag**: Per-command machine-readable output (`json`, `table`, `TEMPLATE`); formatted data → stdout, status/progress → stderr
- Stream rules above apply to static output. Live-display and live-interactive scenarios delegate rendering to the TUI layer — see [.agents/docs/cli-output-style-guide.md](../docs/cli-output-style-guide.md) for per-scenario details

### Deprecated (do not use in new code)
- `cmdutil.HandleError`, `cmdutil.PrintNextSteps`, `cmdutil.PrintErrorf`
- `ios.PrintSuccess/Warning/Info/Failure()` — deleted
- `ios.RenderHeader/Divider/KeyValue/Status()` — deleted

## Cobra Commands
- Always use `PersistentPreRunE` (never `PersistentPreRun`)
- Always include `Example` field with indented examples
- Subpackages under `internal/cmd/<noun>/` are for subcommands only
- Exception: `shared/` package holds flag types, domain logic, and `CreateContainer()` — shared across subcommands

## CLI Guidelines Reference
- Follow conventions from https://clig.dev/ for CLI design patterns

## Config Package How-To

- Use only the `config.Config` interface in consumers; never reach into `internal/config` internals.
- Do not hardcode config file paths or constants in callers (`.clawker.yaml`, subdirs, label domains) when an interface method exists.
- Read paths/constants through methods (`ConfigDir()`, `Domain()`, `LabelDomain()`, `LogsSubdir()`, etc.).
- Reads are value-specific accessors (`BuildConfig()`, `LoggingConfig()`, …) — there is no whole-schema getter.
- Mutation: `ProjectStore().Set([]string{"build", "image"}, value)` / `SettingsStore().Set(key, value)` (and `Remove(key...)`) stage in-memory; `ProjectStore().Write()` / `SettingsStore().Write()` persists to disk. Keys are explicit segments, never dotted strings.
- In tests, prefer `configmocks.NewBlankConfig()`, `configmocks.NewFromString(projectYAML, settingsYAML)`, and `configmocks.NewIsolatedTestConfig(t)` from `internal/config/mocks/` (import as `configmocks "github.com/schmitthub/clawker/internal/config/mocks"`).

## No Hardcoded Strings

Every meaningful string is a const — cross-cutting → `internal/consts/`, package-local → that package's `consts.go`, config-derived → `config.Config` accessors. Code references the const; comments/docs never hard-spell its value (write "the clawker network", not `clawker-net`).

## Error Handling

Do not add `//nolint:` directives without explicit user approval. Correct the code to satisfy the lint checks. Keep `exhaustruct` checks active and initialize all required struct fields explicitly.

Never discard an `error` with `_` (`x, _ := fn()`) — handle it, wrap-and-return (`fmt.Errorf("ctx: %w", err)`), or `errors.Is` the one benign sentinel and surface the rest. The only exception is a genuinely unactionable error (e.g. deferred cleanup), which must carry a comment saying why.

## Context Management

**NEVER** store `context.Context` in struct fields. Pass as first parameter. Use `context.Background()` for cleanup in deferred functions.
