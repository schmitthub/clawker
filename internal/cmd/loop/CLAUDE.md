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
| `shared/concurrency.go` | `CheckConcurrency` — detect concurrent sessions, prompt for worktree |
| `shared/dashboard.go` | `WireLoopDashboard` — bridge Runner callbacks to TUI dashboard channel |
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
- `BuildRunnerOptions(loopOpts, project, agent, containerName, prompt, workDir, flags, loopCfg) Options` — maps CLI options to runner options with config override support (`flags.Changed()` pattern)
- `ApplyLoopConfigDefaults(loopOpts, flags, loopCfg)` — applies config overrides to `LoopOptions` for pre-runner fields (`hooks_file`, `append_system_prompt`). Called in iterate/tasks run functions after loading config but before `SetupLoopContainer` and `BuildRunnerOptions`.

**Config override pattern**: Two layers of config-to-flag override:
1. **Pre-runner** (`ApplyLoopConfigDefaults`): Applies `hooks_file` and `append_system_prompt` from config to `LoopOptions`. These fields are consumed before the runner is created (by `SetupLoopContainer` and `BuildRunnerOptions` respectively).
2. **Runner-level** (`applyConfigOverrides`, called within `BuildRunnerOptions`): Applies numeric/boolean tuning fields from config to `Options`.
Both use the `!flags.Changed(flagName)` pattern — explicit CLI flags always take precedence over config values.

## Shared Dashboard (`shared/dashboard.go`)

Output mode selection, event bridge, and TUI detach handling for loop commands:

- `RunLoopConfig` — all inputs for `RunLoop`: Runner, RunnerOpts, TUI, IOStreams, Setup, Format, Verbose, CommandName
- `RunLoop(ctx context.Context, cfg RunLoopConfig) (*Result, error)` — consolidated loop execution with output mode selection. Context passed as first parameter (not stored in struct). If stderr is a TTY and not verbose/quiet/json, uses TUI dashboard; otherwise falls back to text Monitor (verbose/non-TTY default) or silent execution (quiet/json). Shared by iterate and tasks commands. Handles three TUI exit paths: normal completion, detach (q/Esc), and interrupt (Ctrl+C).
- `WireLoopDashboard(opts *Options, ch chan<- LoopDashEvent, setup *LoopContainerResult, maxLoops int)` — sets `OnLoopStart`, `OnLoopEnd`, `OnStreamEvent` callbacks on Runner options to send `LoopDashEvent` values on the channel. `OnStreamEvent` sends `OutputToolStart` for tool starts and `OutputText` for text deltas. Sends an initial `LoopDashEventStart` event. Sets `opts.Monitor = nil` to disable text monitor. Extracts cost/token data from `*ResultEvent` into `IterCostUSD`/`IterTokens`/`IterTurns` fields. Does NOT close the channel — the caller's goroutine does that.
- `drainLoopEventsAsText(w io.Writer, cs *ColorScheme, ch <-chan LoopDashEvent)` — consumes remaining events after TUI detach and renders as minimal text status lines using semantic icon methods (`cs.InfoIcon()`, `cs.SuccessIcon()`, `cs.FailureIcon()`, `cs.WarningIcon()`). Returns when the channel is closed (runner finished).
- `formatMinimalDuration(d time.Duration) string` — formats duration for minimal text output.
- `sendEvent(ch, ev)` — non-blocking send: drops events if channel is full to prevent deadlocking the runner goroutine. Dropped events are logged via `logger.Warn` with the event kind name.

**TUI detach flow**: When the user presses q/Esc in the TUI, `RunLoop` prints a transition message and calls `drainLoopEventsAsText` to continue consuming events as minimal text. The runner goroutine keeps running — the channel close signals completion. Ctrl+C cancels the runner context (via `context.WithCancel`) and drains the channel to let the goroutine exit cleanly. Dashboard errors also cancel the runner and drain the channel to prevent goroutine leaks.

**Concurrency model**: The runner goroutine writes `result`/`runErr`, then `close(ch)` (deferred). The main goroutine reads from `ch` until closed, then reads `result`/`runErr`. The channel close provides the happens-before guarantee.

**Output mode suppression**: Quiet (`--quiet`) and JSON (`--json`) modes suppress the text Monitor and "Starting loop..." start message — only the final result matters. The `showProgress` guard in the non-TUI branch checks `!cfg.Format.Quiet && !cfg.Format.IsJSON()`. When suppressed, `runnerOpts.Monitor` remains nil (which the Runner handles gracefully via nil-checks on all Monitor calls). Verbose output (`OnOutput` callback) is still gated independently by `cfg.Verbose`, though in practice `--verbose` is mutually exclusive with `--json`/`--quiet`.

**Session totals**: Accumulated across iterations (TasksCompleted, FilesModified) and sent on each `IterEnd` event as TotalTasks/TotalFiles.

## Loop Dashboard (`shared/loopdash.go`)

Real-time BubbleTea dashboard for `loop iterate` and `loop tasks` commands. Implements `tui.DashboardRenderer` — the generic dashboard handles BubbleTea lifecycle; this package provides loop-specific rendering.

**Event types**: `LoopDashEventKind` (`LoopDashEventStart/IterStart/IterEnd/Output/RateLimit/Complete`). `String()` method returns human-readable name for logging.

**OutputKind**: `OutputText` (text deltas) and `OutputToolStart` (tool activity indicators like `[Using Bash...]`).

**LoopDashEvent**: Channel event with Kind, Iteration, MaxIterations, AgentName, Project, StatusText, TasksCompleted, FilesModified, TestsStatus, ExitSignal, CircuitProgress/Threshold/Tripped, RateRemaining/RateLimit, IterDuration, ExitReason, Error, TotalTasks, TotalFiles, IterCostUSD, IterTokens, IterTurns, OutputChunk, OutputKind.

**Entry point**: `RunLoopDashboard(ios, cfg, ch)` — creates `loopDashRenderer`, bridges typed `chan LoopDashEvent` to generic `chan any`, delegates to `tui.RunDashboard`.

**Layout**: Header bar → info line (agent/project/elapsed) → counters (iteration/circuit/rate) → cost/token line (after first iteration) → status section → activity log (newest first, last 10) → help line. Running entries show streaming output lines (max 5) with `⎿` tree connectors.

**Activity log**: Ring buffer of `activityEntry` (max 10). Running entries show `● [Loop N] Running...` with output lines, completed entries show `✓ [Loop N] STATUS — tasks, files, $cost (duration)`.

**Streaming output**: `processOutputEvent` handles `OutputText` (line-buffered, pushed on `\n`) and `OutputToolStart` (pushed directly). Output lines cleared on `IterStart` and `IterEnd`.

## Shared Result (`shared/result.go`)

Result output formatting:

- `ResultOutput` — JSON-serializable struct: LoopsCompleted, ExitReason, Success, Error, TotalTasksCompleted, TotalFilesModified, FinalStatus, RateLimitHit
- `NewResultOutput(result *Result) *ResultOutput` — maps Result to output struct
- `WriteResult(out, errOut io.Writer, result *Result, format *cmdutil.FormatFlags) error` — writes JSON (`--json`), exit reason (`--quiet`), or nothing (default, monitor handles it)

## Shared Lifecycle (`shared/lifecycle.go`)

Container lifecycle management for loop commands:

- `LoopContainerConfig` — all inputs: Client, Config, LoopOpts, Flags, Version, GitManager, HostProxy, SocketBridge, IOStreams
- `LoopContainerResult` — outputs: ContainerID, ContainerName, AgentName, Project, WorkDir
- `SetupLoopContainer(ctx, cfg) (*LoopContainerResult, func(), error)` — creates container via `container/shared.CreateContainer`, injects hooks, starts container, returns cleanup function
- `InjectLoopHooks(ctx, containerID, hooksFile, copyFn) error` — resolves hooks (default or custom), writes settings.json + hook scripts to container

**Container lifecycle flow**: Image resolution → CreateContainer (with spinner) → InjectLoopHooks (settings.json + scripts) → ContainerStart → SocketBridge setup. Cleanup function (deferred) stops and removes container with 30s timeout using `context.Background()`. Socket bridge failures and cleanup failures are logged to file AND surfaced to stderr so users see them.

**Hook injection**: Writes `settings.json` to `/home/claude/.claude/` with hook config (overwrites any existing settings). Hook scripts (e.g., stop-check.js) written to absolute paths in container. Custom hooks (`--hooks-file`) replace defaults entirely with no script files.

## Shared Concurrency (`shared/concurrency.go`)

Session concurrency detection using Docker container labels:

- `ConcurrencyAction` — int const: `ActionProceed`, `ActionWorktree`, `ActionAbort`
- `ConcurrencyCheckConfig` — Client, Project, WorkDir, IOStreams, Prompter
- `CheckConcurrency(ctx, cfg) (ConcurrencyAction, error)` — lists running containers for the project, filters by workdir match. If conflict found: non-interactive → warn and proceed; interactive → prompt with 3 choices (worktree/proceed/abort)

**Detection method**: Uses `docker.Client.ListContainersByProject()` with `includeAll=false` (running only). Compares `Container.Workdir` label against current working directory. Docker labels are ground truth — no stale session file risk.

## IterateOptions

Embeds `*shared.LoopOptions`. Adds `--prompt` / `-p` / `--prompt-file` (mutually exclusive, one required), `FormatFlags`, and captured `flags *pflag.FlagSet`. Factory DI: IOStreams, TUI, Client, Config, GitManager, HostProxy, SocketBridge, Prompter, Version.

**Run flow**: ResolvePrompt → GenerateAgentName → Config → Docker Client → CheckConcurrency → SetupLoopContainer → NewRunner → BuildRunnerOptions → RunLoop (output mode selection + detach handling) → WriteResult → cleanup (deferred).

**Output mode selection**: Delegated to `shared.RunLoop`. If stderr is a TTY and not verbose/quiet/json, uses the TUI dashboard. Otherwise falls back to the text Monitor. When the user presses q/Esc in the TUI, the loop continues in the foreground with minimal text output (detach mode).

## TasksOptions

Embeds `*shared.LoopOptions`. Adds `--tasks` (required), `--task-prompt` / `--task-prompt-file` (mutually exclusive, optional), `FormatFlags`, and captured `flags *pflag.FlagSet`. Factory DI: same as IterateOptions.

**Run flow**: ResolveTasksPrompt → GenerateAgentName → Config → Docker Client → CheckConcurrency → SetupLoopContainer → NewRunner → BuildRunnerOptions → RunLoop (output mode selection + detach handling) → WriteResult → cleanup (deferred).

**Output mode selection**: Same as IterateOptions — TTY dashboard (with detach support) vs text Monitor.

## Loop Strategies

- **iterate**: Same prompt repeated fresh each invocation. Agent only sees codebase state from previous runs, no conversation context carried forward.
- **tasks**: Agent reads a task file, picks an open task, completes it, marks it done. Clawker is the "dumb loop" — the agent LLM handles task selection. Default template wraps tasks in `<tasks>` block; custom templates use `%s` placeholder or append.

Loop commands handle full container lifecycle: create → hooks → start → loop → cleanup. Containers are ephemeral — created at loop start, removed on exit.

## Testing

Tests in `iterate/iterate_test.go` and `tasks/tasks_test.go` cover flag parsing, mutual exclusivity, required flags, defaults, all-flags round-trip, output mode combinations, verbose exclusivity, no-agent-flag rejection, agent-empty-at-parse verification, Factory DI wiring (HostProxy, SocketBridge, Version), real-run Docker dependency check, and flags capture. Tests in `shared/resolve_test.go` cover prompt resolution (inline, file, empty, not found), tasks prompt resolution (default template, custom inline/file, placeholder substitution), and BuildRunnerOptions (basic mapping, config overrides, explicit flag wins, nil safety, boolean overrides, workDir mapping). Tests in `shared/result_test.go` cover ResultOutput mapping, JSON output, quiet output, and default mode (nil return, no output). Tests in `shared/lifecycle_test.go` cover hook injection (default hooks, custom hooks file, invalid hooks file, copy failures), settings.json tar building (content, ownership), and hook files tar building (content, directories, permissions). Tests in `shared/concurrency_test.go` cover concurrency detection (no containers, different workdir, same workdir non-interactive warning, same workdir interactive with all 3 actions, Docker list error, multiple running containers). Tests in `shared/dashboard_test.go` cover WireLoopDashboard (start event, callback wiring, OnLoopStart/OnLoopEnd events, nil status, total accumulation, output forwarding, error propagation, full channel non-blocking), RunLoop mode selection (TTY verbose/json/quiet, non-TTY default/json/quiet — verifies start message presence/absence and monitor suppression), drainLoopEventsAsText (iter start/end, error, no status, rate limit, complete, complete with error, multiple events, ignored output events), and formatMinimalDuration. Per-package `testFactory`/`testFactoryWithConfig` helpers using `&cmdutil.Factory{}` struct literals with test doubles.
