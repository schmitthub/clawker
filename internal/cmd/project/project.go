package project

import (
	projectedit "github.com/schmitthub/clawker/internal/cmd/project/edit"
	projectinfo "github.com/schmitthub/clawker/internal/cmd/project/info"
	projectinit "github.com/schmitthub/clawker/internal/cmd/project/init"
	projectlist "github.com/schmitthub/clawker/internal/cmd/project/list"
	projectregister "github.com/schmitthub/clawker/internal/cmd/project/register"
	projectremove "github.com/schmitthub/clawker/internal/cmd/project/remove"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

func NewCmdProject(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage clawker projects",
		Long: `Manage clawker projects.

This command provides project-level operations for clawker projects.
Use 'clawker project init' to set up a new project in the current directory.`,
		Example: `  # Initialize a new project
  clawker project init

  # Register an existing project
  clawker project register

  # List all registered projects
  clawker project list

  # Show project details
  clawker project info my-project

  # Remove a project from registry
  clawker project remove my-project

  # Interactively edit project configuration
  clawker project edit`,
	}

	cmd.AddCommand(projectinit.NewCmdProjectInit(f, nil))
	cmd.AddCommand(projectedit.NewCmdProjectEdit(f, nil))
	cmd.AddCommand(projectregister.NewCmdProjectRegister(f, nil))
	cmd.AddCommand(projectlist.NewCmdList(f, nil))
	cmd.AddCommand(projectinfo.NewCmdInfo(f, nil))
	cmd.AddCommand(projectremove.NewCmdRemove(f, nil))

	return cmd
}
