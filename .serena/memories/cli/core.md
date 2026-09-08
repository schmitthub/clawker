# CLI and Docker boundaries

- `cmd/clawker/` is the binary entry point; `internal/clawker/` owns the CLI process entry logic.
- `internal/cmd/root/` registers commands and Docker-style aliases. Subcommand packages live under `internal/cmd/<noun>/`.
- `internal/cmdutil/factory.go` declares Factory. `internal/cmd/factory/default.go` wires its lazy closure fields.
- Follow `NewCmd(f, runF)`: construction tests capture options through `runF`; execution tests use the same command with injected dependencies.
- Shared container operations belong in `internal/cmd/container/shared/`. A noun's subdirectories otherwise represent subcommands.
- Docker calls pass through `internal/docker` and `pkg/whail`. Only whail owns the Moby API client. Moby API value types can cross this boundary.
- Managed labels are authoritative. A matching container name does not establish ownership.
- A project agent has three name segments. A global agent has two and omits the project label; an empty project label is not equivalent.
- CP command operations use `f.AdminClient(ctx)`. Do not call the firewall handler directly from a CLI command.

## Presentation

- `internal/term` owns low-level terminal access.
- `internal/iostreams` owns streams, colors, and Lip Gloss access.
- `internal/prompter` owns simple questions. `internal/tui` owns Bubble Tea/Bubbles and live views.
- Detailed output recipes: `.agents/skills/cli-output/SKILL.md`.
- Package contracts: `internal/cmdutil/AGENTS.md`, `internal/cmd/factory/AGENTS.md`, `internal/docker/AGENTS.md`, `pkg/whail/AGENTS.md`.
- For container creation and terminal cleanup: `mem:runtime/core`.
- For output streams and error rules: `mem:conventions`.
