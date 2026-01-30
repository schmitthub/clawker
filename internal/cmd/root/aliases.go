package root

import (
	"fmt"

	containerAttach "github.com/schmitthub/clawker/internal/cmd/container/attach"
	containerCp "github.com/schmitthub/clawker/internal/cmd/container/cp"
	containerCreate "github.com/schmitthub/clawker/internal/cmd/container/create"
	containerExec "github.com/schmitthub/clawker/internal/cmd/container/exec"
	containerKill "github.com/schmitthub/clawker/internal/cmd/container/kill"
	containerlist "github.com/schmitthub/clawker/internal/cmd/container/list"
	containerLogs "github.com/schmitthub/clawker/internal/cmd/container/logs"
	containerPause "github.com/schmitthub/clawker/internal/cmd/container/pause"
	containerRemove "github.com/schmitthub/clawker/internal/cmd/container/remove"
	containerRename "github.com/schmitthub/clawker/internal/cmd/container/rename"
	containerRestart "github.com/schmitthub/clawker/internal/cmd/container/restart"
	containerrun "github.com/schmitthub/clawker/internal/cmd/container/run"
	containerstart "github.com/schmitthub/clawker/internal/cmd/container/start"
	containerStats "github.com/schmitthub/clawker/internal/cmd/container/stats"
	containerStop "github.com/schmitthub/clawker/internal/cmd/container/stop"
	containerTop "github.com/schmitthub/clawker/internal/cmd/container/top"
	containerUnpause "github.com/schmitthub/clawker/internal/cmd/container/unpause"
	containerWait "github.com/schmitthub/clawker/internal/cmd/container/wait"
	imagebuild "github.com/schmitthub/clawker/internal/cmd/image/build"
	imageRemove "github.com/schmitthub/clawker/internal/cmd/image/remove"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// Alias defines a top-level command alias to a subcommand.
// This follows Docker's pattern where `docker run` is an alias for `docker container run`.
// Each alias creates a new command instance from the factory, overriding only Use and
// optionally Example, while inheriting all other properties (flags, RunE, etc.).
type Alias struct {
	// Use sets the command's Use field (required)
	Use string
	// Example optionally replaces the command's Example field (empty preserves original)
	Example string
	// Command is a factory function that creates the target command
	Command func(*cmdutil.Factory) *cobra.Command
}

// topLevelAliases defines all top-level shortcuts to subcommands.
var topLevelAliases = []Alias{
	{
		Use:     "attach CONTAINER",
		Command: func(f *cmdutil.Factory) *cobra.Command { return containerAttach.NewCmdAttach(f, nil) },
	},
	{
		Use:     "build [OPTIONS]",
		Example: buildExample,
		Command: imagebuild.NewCmd,
	},
	{
		Use:     "create [OPTIONS] IMAGE [COMMAND] [ARG...]",
		Command: func(f *cmdutil.Factory) *cobra.Command { return containerCreate.NewCmdCreate(f, nil) },
	},
	{
		Use:     "cp [OPTIONS] CONTAINER:SRC_PATH DEST_PATH|-\ncp [OPTIONS] SRC_PATH|- CONTAINER:DEST_PATH",
		Command: func(f *cmdutil.Factory) *cobra.Command { return containerCp.NewCmdCp(f, nil) },
	},
	{
		Use:     "exec [OPTIONS] CONTAINER COMMAND [ARG...]",
		Command: func(f *cmdutil.Factory) *cobra.Command { return containerExec.NewCmdExec(f, nil) },
	},
	{
		Use:     "kill [OPTIONS] CONTAINER [CONTAINER...]",
		Command: func(f *cmdutil.Factory) *cobra.Command { return containerKill.NewCmdKill(f, nil) },
	},
	{
		Use:     "logs [OPTIONS] CONTAINER",
		Command: func(f *cmdutil.Factory) *cobra.Command { return containerLogs.NewCmdLogs(f, nil) },
	},
	{
		Use:     "pause [OPTIONS] CONTAINER [CONTAINER...]",
		Command: func(f *cmdutil.Factory) *cobra.Command { return containerPause.NewCmdPause(f, nil) },
	},
	{
		Use:     "ps [OPTIONS]",
		Command: func(f *cmdutil.Factory) *cobra.Command { return containerlist.NewCmdList(f, nil) },
	},
	{
		Use:     "rename CONTAINER NEW_NAME",
		Command: func(f *cmdutil.Factory) *cobra.Command { return containerRename.NewCmdRename(f, nil) },
	},
	{
		Use:     "restart [OPTIONS] CONTAINER [CONTAINER...]",
		Command: containerRestart.NewCmd,
	},
	{
		Use:     "rm [OPTIONS] CONTAINER [CONTAINER...]",
		Command: func(f *cmdutil.Factory) *cobra.Command { return containerRemove.NewCmdRemove(f, nil) },
	},
	{
		Use:     "run [OPTIONS] IMAGE [COMMAND] [ARG...]",
		Command: containerrun.NewCmd,
	},
	{
		Use:     "start [CONTAINER...]",
		Command: containerstart.NewCmdStart,
	},
	{
		Use:     "stats [OPTIONS] [CONTAINER...]",
		Command: containerStats.NewCmd,
	},
	{
		Use:     "stop [OPTIONS] CONTAINER [CONTAINER...]",
		Command: containerStop.NewCmdStop,
	},
	{
		Use:     "rmi [OPTIONS]",
		Command: imageRemove.NewCmd,
	},
	{
		Use:     "top [OPTIONS] CONTAINER",
		Command: containerTop.NewCmd,
	},
	{
		Use:     "unpause [OPTIONS] CONTAINER [CONTAINER...]",
		Command: containerUnpause.NewCmdUnpause,
	},
	{
		Use:     "wait CONTAINER [CONTAINER...]",
		Command: containerWait.NewCmd,
	},
}

// registerAliases adds all top-level aliases to the root command.
// Each alias gets a fresh command instance from its factory, ensuring
// flags, RunE, and other properties are inherited automatically.
func registerAliases(root *cobra.Command, f *cmdutil.Factory) {
	for _, alias := range topLevelAliases {
		if alias.Use == "" {
			panic("alias has empty Use field")
		}
		if alias.Command == nil {
			panic(fmt.Sprintf("alias %q has nil Command factory", alias.Use))
		}
		cmd := alias.Command(f)
		if cmd == nil {
			panic(fmt.Sprintf("alias %q factory returned nil command", alias.Use))
		}
		cmd.Use = alias.Use
		if alias.Example != "" {
			cmd.Example = alias.Example
		}
		root.AddCommand(cmd)
	}
}

const buildExample = `  # Build the project image
  clawker build

  # Build without Docker cache
  clawker build --no-cache

  # Build using a custom Dockerfile
  clawker build -f ./Dockerfile.dev

  # Build with multiple tags
  clawker build -t myapp:latest -t myapp:v1.0

  # Build with build arguments
  clawker build --build-arg NODE_VERSION=20

  # Build a specific target stage
  clawker build --target builder

  # Build quietly (suppress output)
  clawker build -q

  # Always pull base image
  clawker build --pull`
