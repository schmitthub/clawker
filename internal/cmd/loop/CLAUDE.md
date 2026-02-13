# Loop Command Package

Autonomous Claude Code loops — repeated execution with circuit breaker protection.

## Files

| File | Purpose |
|------|---------|
| `loop.go` | `NewCmdLoop(f)` — parent command |
| `shared/options.go` | `LoopOptions`, `AddLoopFlags`, `MarkVerboseExclusive` — shared flag types |
| `shared/resolve.go` | `ResolvePrompt`, `ResolveTasksPrompt`, `BuildRunnerOptions` — prompt resolution + option building |
| `shared/result.go` | `ResultOutput`, `NewResultOutput`, `WriteResult` — result output formatting |
| `iterate/iterate.go` | `NewCmdIterate(f, runF)` — repeated-prompt loop |
| `tasks/tasks.go` | `NewCmdTasks(f, runF)` — task-file-driven loop |
| `status/status.go` | `NewCmdStatus(f, runF)` — show session status |
| `reset/reset.go` | `NewCmdReset(f, runF)` — reset circuit breaker |

## Subcommands

- `loop iterate --agent <name> --prompt "..." | --prompt-file <path>` — run an agent loop with a repeated prompt
- `loop tasks --agent <name> --tasks <file> [--task-prompt "..." | --task-prompt-file <path>]` — run an agent loop driven by a task file
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
| Container | `--agent` (required), `--worktree`, `--image` |
| Output | `-v`/`--verbose` (plus `FormatFlags` for `--json`/`--quiet`/`--format`) |

**Flag registration**: `AddLoopFlags(cmd, opts)` registers shared flags. Call before `AddFormatFlags`. `MarkVerboseExclusive(cmd)` marks `--verbose` as mutually exclusive with `--json`/`--quiet`/`--format`. Flag defaults use `loop.Default*` constants as single source of truth. `--agent` is registered in `AddLoopFlags` but marked required in each subcommand.

## Shared Resolve (`shared/resolve.go`)

Prompt resolution and option building helpers:

- `ResolvePrompt(prompt, promptFile string) (string, error)` — returns inline prompt or reads from file, trims whitespace, errors on empty
- `ResolveTasksPrompt(tasksFile, taskPrompt, taskPromptFile string) (string, error)` — reads tasks file, wraps in default template or applies custom template (`%s` placeholder or appended)
- `BuildRunnerOptions(loopOpts, project, agent, containerName, prompt, flags, loopCfg) loop.Options` — maps CLI options to runner options with config override support (`flags.Changed()` pattern)

**Config override pattern**: For each configurable field, if the CLI flag was not explicitly set (`!flags.Changed(flagName)`) and the config value is non-zero/true, the config value wins. Explicit CLI flags always take precedence.

## Shared Result (`shared/result.go`)

Result output formatting:

- `ResultOutput` — JSON-serializable struct: LoopsCompleted, ExitReason, Success, Error, TotalTasksCompleted, TotalFilesModified, FinalStatus, RateLimitHit
- `NewResultOutput(result *loop.Result) *ResultOutput` — maps loop.Result to output struct
- `WriteResult(out, errOut io.Writer, result *loop.Result, format *cmdutil.FormatFlags) error` — writes JSON (`--json`), exit reason (`--quiet`), or nothing (default, monitor handles it)

## IterateOptions

Embeds `*shared.LoopOptions`. Adds `--prompt` / `-p` / `--prompt-file` (mutually exclusive, one required), `FormatFlags`, and captured `flags *pflag.FlagSet`. Factory DI: IOStreams, TUI, Client, Config, GitManager, Prompter.

**Run flow**: ResolvePrompt → Config → Docker Client → FindContainerByAgent → NewRunner → BuildRunnerOptions → NewMonitor → Runner.Run → WriteResult.

## TasksOptions

Embeds `*shared.LoopOptions`. Adds `--tasks` (required), `--task-prompt` / `--task-prompt-file` (mutually exclusive, optional), `FormatFlags`, and captured `flags *pflag.FlagSet`. Factory DI: same as IterateOptions.

**Run flow**: ResolveTasksPrompt → Config → Docker Client → FindContainerByAgent → NewRunner → BuildRunnerOptions → NewMonitor → Runner.Run → WriteResult.

## Loop Strategies

- **iterate**: Same prompt repeated fresh each invocation. Agent only sees codebase state from previous runs, no conversation context carried forward.
- **tasks**: Agent reads a task file, picks an open task, completes it, marks it done. Clawker is the "dumb loop" — the agent LLM handles task selection. Default template wraps tasks in `<tasks>` block; custom templates use `%s` placeholder or append.

Container must exist and be running before loop starts. Commands verify state via `FindContainerByAgent` and provide actionable error messages.

## Testing

Tests in `iterate/iterate_test.go` and `tasks/tasks_test.go` cover flag parsing, mutual exclusivity, required flags (including `--agent`), defaults, all-flags round-trip, output mode combinations, verbose exclusivity, agent flag wiring, real-run Docker dependency check, and flags capture. Tests in `shared/resolve_test.go` cover prompt resolution (inline, file, empty, not found), tasks prompt resolution (default template, custom inline/file, placeholder substitution), and BuildRunnerOptions (basic mapping, config overrides, explicit flag wins, nil safety, boolean overrides). Tests in `shared/result_test.go` cover ResultOutput mapping, JSON output, and quiet output. Per-package `testFactory`/`testFactoryWithConfig` helpers using `&cmdutil.Factory{}` struct literals with test doubles.
