// Package ralph provides the ralph command for autonomous Claude Code loops.
package ralph

import (
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdRalph returns the ralph parent command.
func NewCmdRalph(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ralph",
		Short: "Run Claude Code in autonomous loops",
		Long: `Commands for running Claude Code agents in autonomous loops.

The ralph command automates Claude Code execution using the "Ralph Wiggum"
technique: Claude runs repeatedly with --continue until signaling completion
via a RALPH_STATUS block in its output.

The agent must be configured to output a RALPH_STATUS block in its responses.
See the documentation for the expected format.

Available commands:
  run     Start the autonomous loop
  status  Show current session status
  reset   Reset the circuit breaker`,
		Example: `  # Start a ralph loop with an initial prompt
  clawker ralph run --agent dev --prompt "Fix all failing tests"

  # Check the status of a ralph session
  clawker ralph status --agent dev

  # Reset the circuit breaker after stagnation
  clawker ralph reset --agent dev`,
	}

	cmd.AddCommand(newCmdRun(f))
	cmd.AddCommand(newCmdStatus(f))
	cmd.AddCommand(newCmdReset(f))

	return cmd
}
