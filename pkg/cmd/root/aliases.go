package root

import (
	"fmt"

	containerlist "github.com/schmitthub/clawker/pkg/cmd/container/list"
	containerrun "github.com/schmitthub/clawker/pkg/cmd/container/run"
	containerstart "github.com/schmitthub/clawker/pkg/cmd/container/start"
	imagebuild "github.com/schmitthub/clawker/pkg/cmd/image/build"
	"github.com/schmitthub/clawker/pkg/cmdutil"
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
		Use:     "build [OPTIONS]",
		Example: buildExample,
		Command: imagebuild.NewCmd,
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
		Use:     "ps [OPTIONS]",
		Command: containerlist.NewCmdList,
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
