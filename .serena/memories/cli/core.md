# CLI and Docker boundaries

- `cmd/clawker/` is the binary entry point; `internal/clawker/` owns the CLI process entry logic.
- `internal/cmd/root/` registers commands and Docker-style aliases. Subcommand packages live under `internal/cmd/<noun>/`.
- `internal/cmdutil/factory.go` declares Factory. `internal/cmd/factory/default.go` wires its lazy closure fields.
- Follow `NewCmd(f, runF)`: construction tests capture options through `runF`; execution tests use the same command with injected dependencies.
- Shared container operations belong in `internal/cmd/container/shared/`. A noun's subdirectories otherwise represent subcommands.
- Docker calls pass through `internal/docker` and `pkg/whail`. Only `pkg/whail` imports the Moby `APIClient` connector and only `internal/docker` imports `pkg/whail`. Typed Moby imports (`client.Filters`, `container.Config`, ...) are fine anywhere. Standalone daemon packages (`internal/hostproxy`, `internal/cmd/bridge`, `cmd/clawkercp`, `internal/controlplane`) may import the connector directly; they need lightweight Docker access without whail's label isolation.
- Cobra: always `PersistentPreRunE`, never `PersistentPreRun`. Every command sets `Example` with indented examples. Follow https://clig.dev/ mem:conventions.
- Managed labels are authoritative. A matching container name does not establish ownership.
- A project agent has three name segments. A global agent has two and omits the project label; an empty project label is not equivalent.
- CP command operations use `f.AdminClient(ctx)`. Do not call the firewall handler directly from a CLI command.

## Presentation

- `internal/term` owns low-level terminal access: only it imports `golang.org/x/term`; it is a leaf (stdlib + `x/term`). Use `term.IsTerminalFd(fd)` and `term.GetTerminalSize(fd)`.
- `internal/iostreams` owns streams, colors, and Lip Gloss access; no other package imports `lipgloss`.
- `internal/prompter` owns simple questions. `internal/tui` owns Bubble Tea/Bubbles and live views; no other package imports `bubbletea` or `bubbles`.
- Four output scenarios: static (`iostreams` + `fmt`), static-interactive (+ `prompter`), live-display and live-interactive (+ `tui`). Every command imports `iostreams`; `tui` is additive. Commands reach the TUI through `f.TUI`, never tui package functions. Missing views become generic `tui` components; `tui` never holds consumer-specific logic.
- Output: `f.TUI.NewTable(headers...)` for tables, never raw `tabwriter`. Colors and icons through `ios.ColorScheme()`. Errors return to `Main()`: `fmt.Errorf("ctx: %w", err)`, `cmdutil.FlagErrorf` for usage errors, `cmdutil.SilentError` when already displayed. Deprecated, do not use: `cmdutil.HandleError`, `cmdutil.PrintNextSteps`, `cmdutil.PrintErrorf`, `ios.PrintSuccess/Warning/Info/Failure`, `ios.RenderHeader/Divider/KeyValue/Status`.
- Detailed output recipes: `.agents/skills/cli-output/SKILL.md`.
- Package contracts: `internal/cmdutil/AGENTS.md`, `internal/cmd/factory/AGENTS.md`, `internal/docker/AGENTS.md`, `pkg/whail/AGENTS.md`.
- For container creation and terminal cleanup: `mem:runtime/core`.
- For output streams and error rules: `mem:conventions`.
