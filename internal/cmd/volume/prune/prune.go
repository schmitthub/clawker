// Package prune provides the volume prune command.
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

// NewCmdPrune creates the volume prune command.
func NewCmdPrune(f *cmdutil.Factory, runF func(context.Context, *PruneOptions) error) *cobra.Command {
	opts := &PruneOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
		Prompter:  f.Prompter,
	}

	cmd := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove unused local volumes",
		Long: `Removes all clawker-managed volumes that are not currently in use.

This command removes volumes that are not attached to any container.
Use with caution as this will permanently delete data.`,
		Example: `  # Remove all unused clawker volumes
  clawker volume prune

  # Remove without confirmation prompt
  clawker volume prune --force`,
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
		confirmed, err := opts.Prompter().Confirm(fmt.Sprintf("%s This will remove all unused clawker-managed volumes.", cs.WarningIcon()), false)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(ios.ErrOut, "Aborted.")
			return nil
		}
	}

	// Prune all unused managed volumes (all=true to include named volumes)
	report, err := client.VolumesPrune(ctx, true)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	if len(report.Report.VolumesDeleted) == 0 {
		fmt.Fprintln(ios.ErrOut, "No unused clawker volumes to remove.")
		return nil
	}

	for _, name := range report.Report.VolumesDeleted {
		fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.SuccessIcon(), name)
	}

	fmt.Fprintf(ios.ErrOut, "\nTotal reclaimed space: %s\n", formatBytes(int64(report.Report.SpaceReclaimed)))

	return nil
}

// formatBytes formats bytes into a human-readable string.
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2fGB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2fMB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.2fKB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
