# Loop Command Package

Autonomous Claude Code loops — repeated execution with circuit breaker protection.

## Files

| File | Purpose |
|------|---------|
| `loop.go` | `NewCmdLoop(f)` — parent command |
| `shared/options.go` | `LoopOptions`, `AddLoopFlags`, `MarkVerboseExclusive` — shared flag types |
| `shared/resolve.go` | `ResolvePrompt`, `ResolveTasksPrompt`, `BuildRunnerOptions` — prompt resolution + option building |
| `shared/result.go` | `ResultOutput`, `NewResultOutput`, `WriteResult` — result output formatting |
| `shared/lifecycle.go` | `SetupLoopContainer`, `InjectLoopHooks` — container lifecycle + hook injection |
| `iterate/iterate.go` | `NewCmdIterate(f, runF)` — repeated-prompt loop |
| `tasks/tasks.go` | `NewCmdTasks(f, runF)` — task-file-driven loop |
| `status/status.go` | `NewCmdStatus(f, runF)` — show session status |
| `reset/reset.go` | `NewCmdReset(f, runF)` — reset circuit breaker |

## Subcommands

- `loop iterate --prompt "..." | --prompt-file <path>` — run an agent loop with a repeated prompt (agent name auto-generated)
- `loop tasks --tasks <file> [--task-prompt "..." | --task-prompt-file <path>]` — run an agent loop driven by a task file (agent name auto-generated)
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

**Flag registration**: `AddLoopFlags(cmd, opts)` registers shared flags. Call before `AddFormatFlags`. `MarkVerboseExclusive(cmd)` marks `--verbose` as mutually exclusive with `--json`/`--quiet`/`--format`. Flag defaults use `loop.Default*` constants as single source of truth. The `Agent` field on `LoopOptions` is set programmatically by run functions via `loop.GenerateAgentName()` — not exposed as a CLI flag on iterate/tasks. Status and reset register their own `--agent` flag independently.

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

## Shared Lifecycle (`shared/lifecycle.go`)

Container lifecycle management for loop commands:

- `LoopContainerConfig` — all inputs: Client, Config, LoopOpts, Flags, Version, GitManager, HostProxy, SocketBridge, IOStreams
- `LoopContainerResult` — outputs: ContainerID, ContainerName, AgentName, Project
- `SetupLoopContainer(ctx, cfg) (*LoopContainerResult, func(), error)` — creates container via `container/shared.CreateContainer`, injects hooks, starts container, returns cleanup function
- `InjectLoopHooks(ctx, containerID, hooksFile, copyFn) error` — resolves hooks (default or custom), writes settings.json + hook scripts to container

**Container lifecycle flow**: Image resolution → CreateContainer (with spinner) → InjectLoopHooks (settings.json + scripts) → ContainerStart → SocketBridge setup. Cleanup function (deferred) stops and removes container with 30s timeout using `context.Background()`.

**Hook injection**: Writes `settings.json` to `/home/claude/.claude/` with hook config (overwrites any existing settings). Hook scripts (e.g., stop-check.js) written to absolute paths in container. Custom hooks (`--hooks-file`) replace defaults entirely with no script files.

## IterateOptions

Embeds `*shared.LoopOptions`. Adds `--prompt` / `-p` / `--prompt-file` (mutually exclusive, one required), `FormatFlags`, and captured `flags *pflag.FlagSet`. Factory DI: IOStreams, TUI, Client, Config, GitManager, HostProxy, SocketBridge, Prompter, Version.

**Run flow**: ResolvePrompt → GenerateAgentName → Config → Docker Client → SetupLoopContainer → NewRunner → BuildRunnerOptions → NewMonitor → Runner.Run → WriteResult → cleanup (deferred).

## TasksOptions

Embeds `*shared.LoopOptions`. Adds `--tasks` (required), `--task-prompt` / `--task-prompt-file` (mutually exclusive, optional), `FormatFlags`, and captured `flags *pflag.FlagSet`. Factory DI: same as IterateOptions.

**Run flow**: ResolveTasksPrompt → GenerateAgentName → Config → Docker Client → SetupLoopContainer → NewRunner → BuildRunnerOptions → NewMonitor → Runner.Run → WriteResult → cleanup (deferred).

## Loop Strategies

- **iterate**: Same prompt repeated fresh each invocation. Agent only sees codebase state from previous runs, no conversation context carried forward.
- **tasks**: Agent reads a task file, picks an open task, completes it, marks it done. Clawker is the "dumb loop" — the agent LLM handles task selection. Default template wraps tasks in `<tasks>` block; custom templates use `%s` placeholder or append.

Loop commands handle full container lifecycle: create → hooks → start → loop → cleanup. Containers are ephemeral — created at loop start, removed on exit.

## Testing

Tests in `iterate/iterate_test.go` and `tasks/tasks_test.go` cover flag parsing, mutual exclusivity, required flags, defaults, all-flags round-trip, output mode combinations, verbose exclusivity, no-agent-flag rejection, agent-empty-at-parse verification, Factory DI wiring (HostProxy, SocketBridge, Version), real-run Docker dependency check, and flags capture. Tests in `shared/resolve_test.go` cover prompt resolution (inline, file, empty, not found), tasks prompt resolution (default template, custom inline/file, placeholder substitution), and BuildRunnerOptions (basic mapping, config overrides, explicit flag wins, nil safety, boolean overrides). Tests in `shared/result_test.go` cover ResultOutput mapping, JSON output, and quiet output. Tests in `shared/lifecycle_test.go` cover hook injection (default hooks, custom hooks file, invalid hooks file, copy failures), settings.json tar building (content, ownership), and hook files tar building (content, directories, permissions). Per-package `testFactory`/`testFactoryWithConfig` helpers using `&cmdutil.Factory{}` struct literals with test doubles.
