// Package network provides the network management command and its subcommands.
package network

import (
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
  clawker network inspect clawker-net`,
		// No RunE - this is a parent command
	}

	// Add subcommands
	// Note: Subcommands will be added in a future task
	// cmd.AddCommand(NewCmdLs(f))
	// cmd.AddCommand(NewCmdInspect(f))

	return cmd
}
