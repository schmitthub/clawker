// Package network provides the network management command and its subcommands.
package network

import (
	"github.com/schmitthub/clawker/pkg/cmd/network/create"
	"github.com/schmitthub/clawker/pkg/cmd/network/inspect"
	"github.com/schmitthub/clawker/pkg/cmd/network/list"
	"github.com/schmitthub/clawker/pkg/cmd/network/prune"
	"github.com/schmitthub/clawker/pkg/cmd/network/remove"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdNetwork creates the network management command.
// This is a parent command that groups network-related subcommands.
func NewCmdNetwork(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Manage networks",
		Long: `Manage clawker networks.

Clawker uses a dedicated network (clawker-net) for container communication
and monitoring stack integration.`,
		Example: `  # List clawker networks
  clawker network ls

  # Inspect the clawker network
  clawker network inspect clawker-net

  # Create a new network
  clawker network create mynetwork

  # Remove a network
  clawker network rm mynetwork`,
		// No RunE - this is a parent command
	}

	// Add subcommands
	cmd.AddCommand(create.NewCmd(f))
	cmd.AddCommand(inspect.NewCmd(f))
	cmd.AddCommand(list.NewCmd(f))
	cmd.AddCommand(prune.NewCmd(f))
	cmd.AddCommand(remove.NewCmd(f))

	return cmd
}
