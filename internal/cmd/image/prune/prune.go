// Package prune provides the image prune command.
package prune

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/spf13/cobra"
)

// PruneOptions holds options for the prune command.
type PruneOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)
	Prompter  func() *prompter.Prompter

	Force bool
	All   bool
}

// NewCmdPrune creates the image prune command.
func NewCmdPrune(f *cmdutil.Factory, runF func(context.Context, *PruneOptions) error) *cobra.Command {
	opts := &PruneOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
		Prompter:  f.Prompter,
	}

	cmd := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove unused images",
		Long: `Removes all unused clawker-managed images.

By default, only dangling images (untagged images) are removed.
Use --all to remove all images not used by any container.

Use with caution as this will permanently delete images.`,
		Example: `  # Remove unused (dangling) clawker images
  clawker image prune

  # Remove all unused clawker images
  clawker image prune --all

  # Remove without confirmation prompt
  clawker image prune --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return pruneRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Do not prompt for confirmation")
	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Remove all unused images, not just dangling ones")

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
		warning := "This will remove all dangling clawker-managed images."
		if opts.All {
			warning = "This will remove all unused clawker-managed images."
		}
		confirmed, err := opts.Prompter().Confirm(fmt.Sprintf("%s %s", cs.WarningIcon(), warning), false)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(ios.ErrOut, "Aborted.")
			return nil
		}
	}

	// Prune unused managed images
	// dangling=!opts.All: if --all is false, only prune dangling images
	report, err := client.ImagesPrune(ctx, !opts.All)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	if len(report.Report.ImagesDeleted) == 0 {
		fmt.Fprintln(ios.ErrOut, "No unused clawker images to remove.")
		return nil
	}

	for _, img := range report.Report.ImagesDeleted {
		if img.Untagged != "" {
			fmt.Fprintf(ios.ErrOut, "%s Untagged: %s\n", cs.SuccessIcon(), img.Untagged)
		}
		if img.Deleted != "" {
			fmt.Fprintf(ios.ErrOut, "%s Deleted: %s\n", cs.SuccessIcon(), img.Deleted)
		}
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
