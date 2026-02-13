// Package loop provides the loop command for autonomous Claude Code loops.
package loop

import (
	"github.com/schmitthub/clawker/internal/cmd/loop/reset"
	"github.com/schmitthub/clawker/internal/cmd/loop/run"
	"github.com/schmitthub/clawker/internal/cmd/loop/status"
	"github.com/schmitthub/clawker/internal/cmd/loop/tui"
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

The agent must be configured to output a LOOP_STATUS block in its responses.
See the documentation for the expected format.

Available commands:
  run     Start the autonomous loop
  status  Show current session status
  reset   Reset the circuit breaker
  tui     Launch interactive dashboard`,
		Example: `  # Start a loop with an initial prompt
  clawker loop run --agent dev --prompt "Fix all failing tests"

  # Check the status of a loop session
  clawker loop status --agent dev

  # Reset the circuit breaker after stagnation
  clawker loop reset --agent dev`,
	}

	cmd.AddCommand(run.NewCmdRun(f, nil))
	cmd.AddCommand(status.NewCmdStatus(f, nil))
	cmd.AddCommand(reset.NewCmdReset(f, nil))
	cmd.AddCommand(tui.NewCmdTUI(f, nil))

	return cmd
}
