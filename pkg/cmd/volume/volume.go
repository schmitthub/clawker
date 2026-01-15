// Package volume provides the volume management command and its subcommands.
package volume

import (
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdVolume creates the volume management command.
// This is a parent command that groups volume-related subcommands.
func NewCmdVolume(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volume",
		Short: "Manage volumes",
		Long: `Manage clawker volumes.

Clawker uses volumes to persist workspace data (in snapshot mode),
configuration, and command history between container runs.`,
		Example: `  # List clawker volumes
  clawker volume ls

  # Remove a volume
  clawker volume rm clawker.myapp.ralph-workspace

  # Inspect a volume
  clawker volume inspect clawker.myapp.ralph-workspace`,
		// No RunE - this is a parent command
	}

	// Add subcommands
	// Note: Subcommands will be added in a future task
	// cmd.AddCommand(NewCmdLs(f))
	// cmd.AddCommand(NewCmdRm(f))
	// cmd.AddCommand(NewCmdInspect(f))

	return cmd
}
