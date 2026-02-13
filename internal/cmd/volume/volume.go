// Package volume provides the volume management command and its subcommands.
package volume

import (
	"github.com/schmitthub/clawker/internal/cmd/volume/create"
	"github.com/schmitthub/clawker/internal/cmd/volume/inspect"
	"github.com/schmitthub/clawker/internal/cmd/volume/list"
	"github.com/schmitthub/clawker/internal/cmd/volume/prune"
	"github.com/schmitthub/clawker/internal/cmd/volume/remove"
	"github.com/schmitthub/clawker/internal/cmdutil"
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
  clawker volume rm clawker.myapp.dev-workspace

  # Inspect a volume
  clawker volume inspect clawker.myapp.dev-workspace`,
		// No RunE - this is a parent command
	}

	// Add subcommands
	cmd.AddCommand(create.NewCmdCreate(f, nil))
	cmd.AddCommand(inspect.NewCmdInspect(f, nil))
	cmd.AddCommand(list.NewCmdList(f, nil))
	cmd.AddCommand(prune.NewCmdPrune(f, nil))
	cmd.AddCommand(remove.NewCmdRemove(f, nil))

	return cmd
}
