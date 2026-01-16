// Package container provides the container management command and its subcommands.
package container

import (
	"github.com/schmitthub/clawker/pkg/cmd/container/attach"
	"github.com/schmitthub/clawker/pkg/cmd/container/cp"
	"github.com/schmitthub/clawker/pkg/cmd/container/create"
	"github.com/schmitthub/clawker/pkg/cmd/container/exec"
	"github.com/schmitthub/clawker/pkg/cmd/container/rename"
	"github.com/schmitthub/clawker/pkg/cmd/container/restart"
	containerrun "github.com/schmitthub/clawker/pkg/cmd/container/run"
	"github.com/schmitthub/clawker/pkg/cmd/container/start"
	"github.com/schmitthub/clawker/pkg/cmd/container/stats"
	"github.com/schmitthub/clawker/pkg/cmd/container/top"
	"github.com/schmitthub/clawker/pkg/cmd/container/update"
	"github.com/schmitthub/clawker/pkg/cmd/container/wait"
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
	cmd.AddCommand(attach.NewCmd(f))
	cmd.AddCommand(cp.NewCmd(f))
	cmd.AddCommand(create.NewCmd(f))
	cmd.AddCommand(exec.NewCmd(f))
	cmd.AddCommand(NewCmdInspect(f))
	cmd.AddCommand(NewCmdKill(f))
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdLogs(f))
	cmd.AddCommand(NewCmdPause(f))
	cmd.AddCommand(NewCmdRemove(f))
	cmd.AddCommand(rename.NewCmd(f))
	cmd.AddCommand(restart.NewCmd(f))
	cmd.AddCommand(containerrun.NewCmd(f))
	cmd.AddCommand(start.NewCmdStart(f))
	cmd.AddCommand(stats.NewCmd(f))
	cmd.AddCommand(NewCmdStop(f))
	cmd.AddCommand(top.NewCmd(f))
	cmd.AddCommand(NewCmdUnpause(f))
	cmd.AddCommand(update.NewCmd(f))
	cmd.AddCommand(wait.NewCmd(f))

	return cmd
}
