// Package container provides the container management command and its subcommands.
package container

import (
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdContainer creates the container management command.
// This is a parent command that groups container-related subcommands.
func NewCmdContainer(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "container",
		Short: "Manage containers",
		Long: `Manage clawker containers.

This command provides container management operations similar to Docker's
container management commands.`,
		Example: `  # List running containers
  clawker container ls

  # List all containers (including stopped)
  clawker container ls -a

  # Remove a container
  clawker container rm clawker.myapp.ralph

  # Stop a running container
  clawker container stop clawker.myapp.ralph`,
		// No RunE - this is a parent command
	}

	// Add subcommands
	// Note: Subcommands will be added in Task 3.3
	// cmd.AddCommand(NewCmdLs(f))
	// cmd.AddCommand(NewCmdRm(f))
	// cmd.AddCommand(NewCmdStart(f))
	// cmd.AddCommand(NewCmdStop(f))
	// etc.

	return cmd
}
