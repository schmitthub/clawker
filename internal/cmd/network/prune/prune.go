// Package prune provides the network prune command.
package prune

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/cmdutil"
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
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Do not prompt for confirmation")

	return cmd
}

func run(cmd *cobra.Command, f *cmdutil.Factory, opts *Options) error {
	ctx := context.Background()
	ios := f.IOStreams
	cs := ios.ColorScheme()

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// Prompt for confirmation if not forced
	if !opts.Force {
		fmt.Fprintf(ios.ErrOut, "%s This will remove all unused clawker-managed networks.\nAre you sure you want to continue? [y/N] ", cs.WarningIcon())
		reader := bufio.NewReader(cmd.InOrStdin())
		response, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintln(ios.ErrOut, "Aborted.")
			return nil
		}
		response = strings.TrimSpace(response)
		if response != "y" && response != "Y" {
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
