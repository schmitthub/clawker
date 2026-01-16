// Package prune provides the network prune command.
package prune

import (
	"context"
	"fmt"
	"os"

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

	// Prune all unused managed networks
	report, err := client.NetworksPrune(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	if len(report.NetworksDeleted) == 0 {
		fmt.Fprintln(os.Stderr, "No unused clawker networks to remove.")
		return nil
	}

	for _, name := range report.NetworksDeleted {
		fmt.Fprintf(os.Stderr, "Deleted: %s\n", name)
	}

	return nil
}
