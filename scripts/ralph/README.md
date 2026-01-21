# Ralph Wiggum Loop Scripts

Scripts for running clawker agents in autonomous loops using the Ralph Wiggum pattern.

## What is the Ralph Wiggum Pattern?

The Ralph Wiggum pattern runs the same prompt in a loop until task completion:

```bash
while :; do cat PROMPT.md | claude; done
```

Each iteration sees the modified codebase from previous attempts, creating a self-correcting feedback loop. The agent learns from git history and file changes to eventually complete the task.

## Scripts

| Script | Purpose |
|--------|---------|
| `ralph-setup.sh` | Create and authenticate a new agent |
| `ralph-loop.sh` | Run a single task in a loop until completion |
| `ralph-all.sh` | Run all tasks sequentially |
| `ralph-status.sh` | Show progress of all tasks |

## Quick Start

```bash
# 1. Setup the agent (one-time, authenticate interactively)
./scripts/ralph/ralph-setup.sh ralph

# Authenticate in browser, then Ctrl+P, Ctrl+Q to detach

# 2. Run individual tasks
./scripts/ralph/ralph-loop.sh 1 ralph    # Task 1
./scripts/ralph/ralph-loop.sh 2 ralph    # Task 2
# ... etc

# 3. Check progress anytime
./scripts/ralph/ralph-status.sh
```

## Usage

### ralph-setup.sh

Create and authenticate a new agent for autonomous work.

```bash
./scripts/ralph/ralph-setup.sh [agent_name]
```

**Arguments:**
- `agent_name` - Name for the agent (default: `ralph`)

**Example:**
```bash
./scripts/ralph/ralph-setup.sh worker
```

**Notes:**
- Subscription users must complete browser authentication
- Detach with `Ctrl+P, Ctrl+Q` (not `Ctrl+C`) to keep container running

### ralph-loop.sh

Run a task in a loop until it outputs `<promise>DONE</promise>`.

```bash
./scripts/ralph/ralph-loop.sh <task_number> [agent_name] [max_iterations] [--force]
```

**Arguments:**
- `task_number` - Task to run (1-5, required)
- `agent_name` - Agent to use (default: `ralph`)
- `max_iterations` - Maximum loop iterations (default: `10`)
- `--force` - Re-run even if task was already completed

**Tasks:**
1. Create GoReleaser Configuration
2. Create GitHub Actions Release Workflow
3. Test Release Flow with Test Tag
4. Update Documentation
5. Create First Official Release

**Examples:**
```bash
# Run task 1 with default agent
./scripts/ralph/ralph-loop.sh 1

# Run task 2 with custom agent and more iterations
./scripts/ralph/ralph-loop.sh 2 myagent 20

# Re-run a completed task
./scripts/ralph/ralph-loop.sh 1 ralph 10 --force
```

### ralph-all.sh

Run all tasks sequentially. Stops if any task fails.

```bash
./scripts/ralph/ralph-all.sh [agent_name] [max_iterations_per_task]
```

**Arguments:**
- `agent_name` - Agent to use (default: `ralph`)
- `max_iterations_per_task` - Max iterations per task (default: `10`)

**Example:**
```bash
# Run all tasks with default settings
./scripts/ralph/ralph-all.sh

# Run with custom agent and 20 iterations per task
./scripts/ralph/ralph-all.sh worker 20
```

**Notes:**
- Completed tasks are automatically skipped
- Resume from where you left off by re-running the command

### ralph-status.sh

Display progress of all tasks.

```bash
./scripts/ralph/ralph-status.sh
```

**Output includes:**
- Task completion status (completed, in progress, pending, failed)
- Iteration counts per task
- Completion timestamps
- Recent log files
- Suggested next action

**Example output:**
```
+------------------------------------------------------------+
|              RALPH LOOP - TASK PROGRESS                    |
+------------------------------------------------------------+

Agent: ralph
Started: 2026-01-20T10:00:00Z

Summary:
  Completed: 2/5
  In Progress: 1
  Failed: 0
  Pending: 2
  Total Iterations: 5

Tasks:
  [OK]  1. Create GoReleaser Configuration           completed (2 iter) @ 10:15
  [OK]  2. Create GitHub Actions Release Workflow    completed (1 iter) @ 10:30
  [..]  3. Test Release Flow with Test Tag           in progress (2 iter)
  [  ]  4. Update Documentation                      pending
  [  ]  5. Create First Official Release             pending

Next action:
  Continue task: ./scripts/ralph/ralph-loop.sh 3 ralph
```

## Working Directory

Scripts use `.ralph/` for working files:

```
.ralph/
├── progress.json       # Task progress tracking
├── task-1.md          # Generated prompt for task 1
├── task-2.md          # Generated prompt for task 2
└── logs/
    ├── task-1_iter-1_2026-01-20_10-00-00.log
    ├── task-1_iter-2_2026-01-20_10-05-00.log
    └── ...
```

This directory is gitignored.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PROMPT_DIR` | `.ralph` | Directory for prompts and progress |
| `LOG_DIR` | `.ralph/logs` | Directory for iteration logs |

## Troubleshooting

### Task keeps failing

1. Check the latest log file in `.ralph/logs/`
2. Attach to the agent interactively: `clawker attach --agent ralph`
3. Increase max iterations: `./scripts/ralph/ralph-loop.sh 1 ralph 20`

### Agent not found

Run setup first:
```bash
./scripts/ralph/ralph-setup.sh ralph
```

### Authentication issues

Subscription users must authenticate interactively before scripted usage:
```bash
clawker run -it --agent ralph -- --dangerously-skip-permissions
# Complete browser auth, then Ctrl+P, Ctrl+Q to detach
```

### Bash version errors

Scripts require bash 4+ for associative arrays. On macOS, install newer bash:
```bash
brew install bash
```

The scripts use `#!/usr/bin/env bash` to pick up the Homebrew version.
