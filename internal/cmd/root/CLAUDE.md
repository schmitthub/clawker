# Root Command Package

Root CLI command, global flags, logger initialization, and top-level aliases.

## Files

| File | Purpose |
|------|---------|
| `root.go` | `NewCmdRoot(f, version, buildDate)` — root command with global flags and subcommand registration |
| `aliases.go` | `Alias` type, `registerBuiltinAliases()`, `topLevelAliases` — hardcoded top-level command shortcuts (Docker CLI pattern) |
| `useraliases.go` | `registerUserAliases()`, `expandAlias()`, `AnnotationAliasExpansion` — user-configured aliases from the merged project config |

## Key Symbols

```go
func NewCmdRoot(f *cmdutil.Factory, version, buildDate string) (*cobra.Command, error)
```

## Global Flags

- `--debug` / `-D` — enable debug logging

## PersistentPreRunE

Currently a no-op (`return nil`). Cobra error/usage output is silenced globally via `SilenceErrors`/`SilenceUsage`; error rendering is handled in `Main`.

## Registered Commands

- **Top-level:** `init` (alias for `project init`), `project`, `settings`, `plugin` (alias `skill`), `monitor`, `version`
- **Management:** `alias`, `auth`, `bundle`, `container`, `controlplane`, `firewall`, `harness`, `image`, `stack`, `volume`, `network`, `worktree`
- **Hidden internal:** `hostproxy`, `bridge`
- **User aliases:** registered last from `cfg.Project().Aliases` (merged across all project config layers; see below)

## Testing

No unit tests for `root.go` — it is straightforward wiring and regressions surface via downstream command tests and `make test`. Tests that need `NewCmdRoot` (e.g., `aliases_test.go`, `useraliases_test.go`) should pass empty strings for version and date.

## Builtin Aliases (`aliases.go`)

```go
type Alias struct { /* factory for aliasing subcommands to top level */ }
func registerBuiltinAliases(root *cobra.Command, f *cmdutil.Factory)
```

20 hardcoded top-level aliases following Docker CLI patterns:
- **Container shortcuts:** `attach`, `create`, `cp`, `exec`, `kill`, `logs`, `pause`, `ps`, `rename`, `restart`, `rm`, `run`, `start`, `stats`, `stop`, `top`, `unpause`, `wait`
- **Image shortcuts:** `build`, `rmi`

## User Aliases (`useraliases.go`)

User-configured aliases from the merged project config (`Project.Aliases` — walk-up files > user config-dir `clawker.yaml` > shipped defaults), gh-CLI-shaped:

```go
func registerUserAliases(root *cobra.Command, f *cmdutil.Factory)   // never fails root construction
func newUserAliasCmd(name, expansion string) *cobra.Command          // DisableFlagParsing + re-execute root
func expandAlias(expansion string, args []string) ([]string, error) // $1..$N substitution + shlex split
func builtinCommandExists(root *cobra.Command, name string) bool     // collision check, skips user-alias cmds
const AnnotationAliasExpansion                                       // cobra annotation marking user alias cmds
```

Behavior contract:

- Called LAST in `NewCmdRoot` — existing commands always win name collisions (skipped with a debug log).
- Each alias is a cobra command with `DisableFlagParsing: true`; RunE expands placeholders, shlex-splits, appends extra args, then `root.SetArgs(expanded); root.Execute()`.
- Empty/whitespace expansion = invalid entry, skipped like multiword names.
- Cyclic alias chains are detected at registration (first-token walk with a seen-set) and skipped.
- nil `f.Config` (gen-docs builds root with a bare Factory) or a config load error skips registration without failing root construction.
- Shipped defaults (default tag on `Project.Aliases`): `go` → `run --rm -it --agent $1 @`; `wt` → `run --rm -it --agent $1 --worktree $2 @` (`$2` is a `branch[:base]` worktree spec); `claude` → `run --rm -it --agent $1 @:claude --dangerously-skip-permissions`; `codex` → `run --rm -it --agent $1 @:codex --yolo`. `go`/`wt` run the default harness and so carry no harness-specific flags; only the per-harness aliases bake in that harness's auto-approve flag.

The `clawker alias` command group (`internal/cmd/alias/`) manages these; root wires its shadow-builtin validator as a closure over `builtinCommandExists`.
