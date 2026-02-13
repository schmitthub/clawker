// Package loop provides the loop command for autonomous Claude Code loops.
package loop

import (
	"github.com/schmitthub/clawker/internal/cmd/loop/iterate"
	"github.com/schmitthub/clawker/internal/cmd/loop/reset"
	"github.com/schmitthub/clawker/internal/cmd/loop/status"
	"github.com/schmitthub/clawker/internal/cmd/loop/tasks"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdLoop returns the loop parent command.
func NewCmdLoop(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "loop",
		Short: "Run Claude Code in autonomous loops",
		Long: `Commands for running Claude Code agents in autonomous loops.

The loop command automates Claude Code execution: Claude runs repeatedly
until signaling completion via a LOOP_STATUS block in its output.

Two loop strategies are available:
  iterate  Same prompt repeated fresh each invocation
  tasks    Agent reads a task file, picks an open task, does it, marks it done

Container lifecycle is managed automatically — a container is created at the
start of each loop and destroyed on completion.

Available commands:
  iterate  Run an agent loop with a repeated prompt
  tasks    Run an agent loop driven by a task file
  status   Show current session status
  reset    Reset the circuit breaker`,
		Example: `  # Run a loop with a repeated prompt
  clawker loop iterate --prompt "Fix all failing tests"

  # Run a task-driven loop
  clawker loop tasks --tasks todo.md

  # Check the status of a loop session
  clawker loop status --agent dev

  # Reset the circuit breaker after stagnation
  clawker loop reset --agent dev`,
	}

	cmd.AddCommand(iterate.NewCmdIterate(f, nil))
	cmd.AddCommand(tasks.NewCmdTasks(f, nil))
	cmd.AddCommand(status.NewCmdStatus(f, nil))
	cmd.AddCommand(reset.NewCmdReset(f, nil))

	return cmd
}
