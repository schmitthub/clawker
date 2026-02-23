// Package project provides the project management command and its subcommands.
package project

import (
	projectinit "github.com/schmitthub/clawker/internal/cmd/project/init"
	projectlist "github.com/schmitthub/clawker/internal/cmd/project/list"
	projectregister "github.com/schmitthub/clawker/internal/cmd/project/register"
	projectremove "github.com/schmitthub/clawker/internal/cmd/project/remove"
	projectshow "github.com/schmitthub/clawker/internal/cmd/project/show"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdProject creates the project management command.
// This is a parent command that groups project-related subcommands.
func NewCmdProject(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage clawker projects",
		Long: `Manage clawker projects.

This command provides project-level operations for clawker projects.
Use 'clawker project init' to set up a new project in the current directory.`,
		Example: `  # Initialize a new project
  clawker project init

  # Initialize with a specific project name
  clawker project init my-project

  # Initialize non-interactively with defaults
  clawker project init --yes

  # Register an existing project
  clawker project register

  # List all registered projects
  clawker project list

  # Show project details
  clawker project show my-project

  # Remove a project from registry
  clawker project remove my-project`,
		// No RunE - this is a parent command
	}

	// Add subcommands
	cmd.AddCommand(projectinit.NewCmdProjectInit(f, nil))
	cmd.AddCommand(projectregister.NewCmdProjectRegister(f, nil))
	cmd.AddCommand(projectlist.NewCmdList(f, nil))
	cmd.AddCommand(projectshow.NewCmdShow(f, nil))
	cmd.AddCommand(projectremove.NewCmdRemove(f, nil))

	return cmd
}
