// Package image provides the image management command and its subcommands.
package image

import (
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdImage creates the image management command.
// This is a parent command that groups image-related subcommands.
func NewCmdImage(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Manage images",
		Long: `Manage clawker images.

This command provides image management operations similar to Docker's
image management commands.`,
		Example: `  # List clawker images
  clawker image ls

  # Build an image
  clawker image build

  # Remove an image
  clawker image rm clawker-myapp:latest`,
		// No RunE - this is a parent command
	}

	// Add subcommands
	// Note: Subcommands will be added in Task 3.4
	// cmd.AddCommand(NewCmdLs(f))
	// cmd.AddCommand(NewCmdBuild(f))
	// cmd.AddCommand(NewCmdRm(f))
	// etc.

	return cmd
}
