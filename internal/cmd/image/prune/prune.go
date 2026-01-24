// Package prune provides the image prune command.
package prune

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/output"
	"github.com/spf13/cobra"
)

// Options holds options for the prune command.
type Options struct {
	Force bool
	All   bool
}

// NewCmd creates the image prune command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

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
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Do not prompt for confirmation")
	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Remove all unused images, not just dangling ones")

	return cmd
}

func run(cmd *cobra.Command, f *cmdutil.Factory, opts *Options) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		output.HandleError(err)
		return err
	}

	// Prompt for confirmation if not forced
	if !opts.Force {
		warning := "WARNING! This will remove all dangling clawker-managed images."
		if opts.All {
			warning = "WARNING! This will remove all unused clawker-managed images."
		}
		fmt.Fprint(os.Stderr, warning+"\nAre you sure you want to continue? [y/N] ")
		reader := bufio.NewReader(cmd.InOrStdin())
		response, err := reader.ReadString('\n')
		if err != nil {
			// Treat read errors (EOF, etc.) as "no"
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
		response = strings.TrimSpace(response)
		if response != "y" && response != "Y" {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
	}

	// Prune unused managed images
	// dangling=!opts.All: if --all is false, only prune dangling images
	report, err := client.ImagesPrune(ctx, !opts.All)
	if err != nil {
		output.HandleError(err)
		return err
	}

	if len(report.Report.ImagesDeleted) == 0 {
		fmt.Fprintln(os.Stderr, "No unused clawker images to remove.")
		return nil
	}

	for _, img := range report.Report.ImagesDeleted {
		if img.Untagged != "" {
			fmt.Fprintf(os.Stderr, "Untagged: %s\n", img.Untagged)
		}
		if img.Deleted != "" {
			fmt.Fprintf(os.Stderr, "Deleted: %s\n", img.Deleted)
		}
	}

	fmt.Fprintf(os.Stderr, "\nTotal reclaimed space: %s\n", formatBytes(int64(report.Report.SpaceReclaimed)))

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
