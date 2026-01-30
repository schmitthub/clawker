// Package container provides the container management command and its subcommands.
package container

import (
	"github.com/schmitthub/clawker/internal/cmd/container/attach"
	"github.com/schmitthub/clawker/internal/cmd/container/cp"
	"github.com/schmitthub/clawker/internal/cmd/container/create"
	"github.com/schmitthub/clawker/internal/cmd/container/exec"
	"github.com/schmitthub/clawker/internal/cmd/container/inspect"
	"github.com/schmitthub/clawker/internal/cmd/container/kill"
	"github.com/schmitthub/clawker/internal/cmd/container/list"
	"github.com/schmitthub/clawker/internal/cmd/container/logs"
	"github.com/schmitthub/clawker/internal/cmd/container/pause"
	"github.com/schmitthub/clawker/internal/cmd/container/remove"
	"github.com/schmitthub/clawker/internal/cmd/container/rename"
	"github.com/schmitthub/clawker/internal/cmd/container/restart"
	"github.com/schmitthub/clawker/internal/cmd/container/run"
	"github.com/schmitthub/clawker/internal/cmd/container/start"
	"github.com/schmitthub/clawker/internal/cmd/container/stats"
	"github.com/schmitthub/clawker/internal/cmd/container/stop"
	"github.com/schmitthub/clawker/internal/cmd/container/top"
	"github.com/schmitthub/clawker/internal/cmd/container/unpause"
	"github.com/schmitthub/clawker/internal/cmd/container/update"
	"github.com/schmitthub/clawker/internal/cmd/container/wait"
	"github.com/schmitthub/clawker/internal/cmdutil"
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
	cmd.AddCommand(attach.NewCmdAttach(f, nil))
	cmd.AddCommand(cp.NewCmdCp(f, nil))
	cmd.AddCommand(create.NewCmdCreate(f, nil))
	cmd.AddCommand(exec.NewCmdExec(f, nil))
	cmd.AddCommand(inspect.NewCmdInspect(f, nil))
	cmd.AddCommand(kill.NewCmdKill(f, nil))
	cmd.AddCommand(list.NewCmdList(f, nil))
	cmd.AddCommand(logs.NewCmdLogs(f, nil))
	cmd.AddCommand(pause.NewCmdPause(f, nil))
	cmd.AddCommand(remove.NewCmdRemove(f, nil))
	cmd.AddCommand(rename.NewCmdRename(f, nil))
	cmd.AddCommand(restart.NewCmdRestart(f, nil))
	cmd.AddCommand(run.NewCmdRun(f, nil))
	cmd.AddCommand(start.NewCmdStart(f, nil))
	cmd.AddCommand(stats.NewCmdStats(f, nil))
	cmd.AddCommand(stop.NewCmdStop(f, nil))
	cmd.AddCommand(top.NewCmdTop(f, nil))
	cmd.AddCommand(unpause.NewCmdUnpause(f))
	cmd.AddCommand(update.NewCmd(f))
	cmd.AddCommand(wait.NewCmd(f))

	return cmd
}
