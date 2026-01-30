// Package image provides the image management command and its subcommands.
package image

import (
	"github.com/schmitthub/clawker/internal/cmd/image/build"
	"github.com/schmitthub/clawker/internal/cmd/image/inspect"
	"github.com/schmitthub/clawker/internal/cmd/image/list"
	"github.com/schmitthub/clawker/internal/cmd/image/prune"
	"github.com/schmitthub/clawker/internal/cmd/image/remove"
	"github.com/schmitthub/clawker/internal/cmdutil"
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
  clawker image rm clawker-myapp:latest

  # Inspect an image
  clawker image inspect clawker-myapp:latest

  # Remove unused images
  clawker image prune`,
		// No RunE - this is a parent command
	}

	// Add subcommands
	cmd.AddCommand(build.NewCmdBuild(f, nil))
	cmd.AddCommand(inspect.NewCmdInspect(f, nil))
	cmd.AddCommand(list.NewCmdList(f, nil))
	cmd.AddCommand(prune.NewCmd(f))
	cmd.AddCommand(remove.NewCmd(f))

	return cmd
}
