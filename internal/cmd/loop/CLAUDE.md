# Loop Command Package

Autonomous Claude Code loops — repeated execution with circuit breaker protection.

## Files

| File | Purpose |
|------|---------|
| `loop.go` | `NewCmdLoop(f)` — parent command |
| `iterate/iterate.go` | `NewCmdIterate(f, runF)` — repeated-prompt loop |
| `tasks/tasks.go` | `NewCmdTasks(f, runF)` — task-file-driven loop |
| `status/status.go` | `NewCmdStatus(f, runF)` — show session status |
| `reset/reset.go` | `NewCmdReset(f, runF)` — reset circuit breaker |

## Subcommands

- `loop iterate --prompt "..."` — run an agent loop with a repeated prompt
- `loop tasks --tasks todo.md` — run an agent loop driven by a task file
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

## Loop Strategies

- **iterate**: Same prompt repeated fresh each invocation. Agent only sees codebase state from previous runs, no conversation context carried forward.
- **tasks**: Agent reads a task file, picks an open task, completes it, marks it done. Clawker is the "dumb loop" — the agent LLM handles task selection.

Container lifecycle is managed automatically: container created at start, destroyed on completion.
