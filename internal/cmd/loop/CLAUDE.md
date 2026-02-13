# Loop Command Package

Autonomous Claude Code loops — repeated execution with circuit breaker protection.

## Files

| File | Purpose |
|------|---------|
| `loop.go` | `NewCmdLoop(f)` — parent command |
| `shared/options.go` | `LoopOptions`, `AddLoopFlags`, `MarkVerboseExclusive` — shared flag types |
| `iterate/iterate.go` | `NewCmdIterate(f, runF)` — repeated-prompt loop |
| `tasks/tasks.go` | `NewCmdTasks(f, runF)` — task-file-driven loop |
| `status/status.go` | `NewCmdStatus(f, runF)` — show session status |
| `reset/reset.go` | `NewCmdReset(f, runF)` — reset circuit breaker |

## Subcommands

- `loop iterate --prompt "..." | --prompt-file <path>` — run an agent loop with a repeated prompt
- `loop tasks --tasks <file> [--task-prompt "..." | --task-prompt-file <path>]` — run an agent loop driven by a task file
- `loop status --agent <name>` — show session status
- `loop reset --agent <name>` — reset circuit breaker after stagnation

## Key Symbols

```go
func NewCmdLoop(f *cmdutil.Factory) *cobra.Command
func NewCmdIterate(f *cmdutil.Factory, runF func(context.Context, *IterateOptions) error) *cobra.Command
func NewCmdTasks(f *cmdutil.Factory, runF func(context.Context, *TasksOptions) error) *cobra.Command
func NewCmdStatus(f *cmdutil.Factory, runF func(context.Context, *StatusOptions) error) *cobra.Command
func NewCmdReset(f *cmdutil.Factory, runF func(context.Context, *ResetOptions) error) *cobra.Command
```

Parent command only (no RunE). Aggregates subcommands from dedicated packages. Circuit breaker logic (max loops, stagnation threshold, timeouts) is configurable in `clawker.yaml` under the `loop` key. Agent signals completion via `LOOP_STATUS` block in output.

## Shared Options (`shared/options.go`)

`LoopOptions` holds flags shared between iterate and tasks:

| Category | Flags |
|----------|-------|
| Loop control | `--max-loops`, `--stagnation-threshold`, `--timeout`, `--loop-delay` |
| Circuit breaker | `--same-error-threshold`, `--output-decline-threshold`, `--max-test-loops`, `--safety-completion-threshold`, `--completion-threshold`, `--strict-completion` |
| Execution | `--skip-permissions`, `--calls-per-hour`, `--reset-circuit` |
| Hooks | `--hooks-file` |
| System prompt | `--append-system-prompt` |
| Container | `--worktree`, `--image` |
| Output | `-v`/`--verbose` (plus `FormatFlags` for `--json`/`--quiet`/`--format`) |

**Flag registration**: `AddLoopFlags(cmd, opts)` registers shared flags. Call before `AddFormatFlags`. `MarkVerboseExclusive(cmd)` marks `--verbose` as mutually exclusive with `--json`/`--quiet`/`--format`. Flag defaults use `loop.Default*` constants as single source of truth.

## IterateOptions

Embeds `*shared.LoopOptions`. Adds `--prompt` / `--prompt-file` (mutually exclusive, one required) and `FormatFlags`. Factory DI: IOStreams, TUI, Client, Config, GitManager, Prompter.

## TasksOptions

Embeds `*shared.LoopOptions`. Adds `--tasks` (required), `--task-prompt` / `--task-prompt-file` (mutually exclusive, optional), and `FormatFlags`. Factory DI: same as IterateOptions.

## Loop Strategies

- **iterate**: Same prompt repeated fresh each invocation. Agent only sees codebase state from previous runs, no conversation context carried forward.
- **tasks**: Agent reads a task file, picks an open task, completes it, marks it done. Clawker is the "dumb loop" — the agent LLM handles task selection.

Container lifecycle is managed automatically: container created at start, destroyed on completion.

## Testing

Tests in `iterate/iterate_test.go` and `tasks/tasks_test.go` cover flag parsing, mutual exclusivity, required flags, defaults, all-flags round-trip, output mode combinations, and verbose exclusivity. Per-package `testFactory` helpers using `&cmdutil.Factory{}` struct literals with test doubles.
