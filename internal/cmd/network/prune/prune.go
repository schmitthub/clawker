// Package prune provides the network prune command.
package prune

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompts"
	"github.com/spf13/cobra"
)

// PruneOptions holds options for the prune command.
type PruneOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)
	Prompter  func() *prompts.Prompter

	Force bool
}

// NewCmdPrune creates the network prune command.
func NewCmdPrune(f *cmdutil.Factory, runF func(context.Context, *PruneOptions) error) *cobra.Command {
	opts := &PruneOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
		Prompter:  f.Prompter,
	}

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
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return pruneRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Do not prompt for confirmation")

	return cmd
}

func pruneRun(ctx context.Context, opts *PruneOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// Prompt for confirmation if not forced
	if !opts.Force {
		confirmed, err := opts.Prompter().Confirm(fmt.Sprintf("%s This will remove all unused clawker-managed networks.", cs.WarningIcon()), false)
		if err != nil {
			fmt.Fprintln(ios.ErrOut, "Aborted.")
			return nil
		}
		if !confirmed {
			fmt.Fprintln(ios.ErrOut, "Aborted.")
			return nil
		}
	}

	// Prune all unused managed networks
	report, err := client.NetworksPrune(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	if len(report.Report.NetworksDeleted) == 0 {
		fmt.Fprintln(ios.ErrOut, "No unused clawker networks to remove.")
		return nil
	}

	for _, name := range report.Report.NetworksDeleted {
		fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.SuccessIcon(), name)
	}

	return nil
}
