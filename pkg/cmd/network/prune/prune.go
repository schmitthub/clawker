// Package prune provides the network prune command.
package prune

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/docker/docker/api/types/network"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the prune command.
type Options struct {
	Force bool
}

// NewCmd creates the network prune command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove unused networks",
		Long: `Removes all clawker-managed networks that are not currently in use.

This command removes networks that have no connected containers.
Use with caution as this may affect container communication.

Note: The built-in clawker-net network will be preserved if containers
are using it for the monitoring stack.`,
		Example: `  # Remove all unused clawker networks
  clawker network prune

  # Remove without confirmation prompt
  clawker network prune --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Do not prompt for confirmation")

	return cmd
}

func run(_ *cmdutil.Factory, opts *Options) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := docker.NewClient(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer client.Close()

	// Prompt for confirmation if not forced
	if !opts.Force {
		fmt.Fprint(os.Stderr, "WARNING! This will remove all unused clawker-managed networks.\nAre you sure you want to continue? [y/N] ")
		var response string
		if _, err := fmt.Scanln(&response); err != nil {
			// Treat read errors (EOF, etc.) as "no"
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
		if response != "y" && response != "Y" {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
	}

	// TODO: implement NetworksPrune in pkg/whail/network.go
	// For now, list networks and check if they have containers connected
	networks, err := client.NetworkList(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	if len(networks) == 0 {
		fmt.Fprintln(os.Stderr, "No unused clawker networks to remove.")
		return nil
	}

	var removed int
	for _, n := range networks {
		// Inspect network to check for connected containers
		info, err := client.NetworkInspect(ctx, n.Name, network.InspectOptions{})
		if err != nil {
			// If we can't inspect, skip it
			fmt.Fprintf(os.Stderr, "Warning: failed to inspect network %s: %v\n", n.Name, err)
			continue
		}

		// Skip networks with connected containers
		if len(info.Containers) > 0 {
			continue
		}

		// Try to remove the network
		if err := client.NetworkRemove(ctx, n.Name); err != nil {
			// Check if it's an "in use" error vs unexpected error
			if strings.Contains(err.Error(), "has active endpoints") {
				continue
			}
			// Log unexpected errors but continue with other networks
			fmt.Fprintf(os.Stderr, "Warning: failed to remove network %s: %v\n", n.Name, err)
			continue
		}
		removed++
		fmt.Fprintf(os.Stderr, "Deleted: %s\n", n.Name)
	}

	if removed == 0 {
		fmt.Fprintln(os.Stderr, "No unused clawker networks to remove.")
	}

	return nil
}
