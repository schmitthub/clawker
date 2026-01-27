// Package prune provides the image prune command.
package prune

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	cmdutil2 "github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the prune command.
type Options struct {
	Force bool
	All   bool
}

// NewCmd creates the image prune command.
func NewCmd(f *cmdutil2.Factory) *cobra.Command {
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
			cmdutil2.AnnotationRequiresProject: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Do not prompt for confirmation")
	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Remove all unused images, not just dangling ones")

	return cmd
}

func run(cmd *cobra.Command, f *cmdutil2.Factory, opts *Options) error {
	ctx := context.Background()
	ios := f.IOStreams
	cs := ios.ColorScheme()

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil2.HandleError(ios, err)
		return err
	}

	// Prompt for confirmation if not forced
	if !opts.Force {
		warning := "This will remove all dangling clawker-managed images."
		if opts.All {
			warning = "This will remove all unused clawker-managed images."
		}
		fmt.Fprintf(ios.ErrOut, "%s %s\nAre you sure you want to continue? [y/N] ", cs.WarningIcon(), warning)
		reader := bufio.NewReader(cmd.InOrStdin())
		response, err := reader.ReadString('\n')
		if err != nil {
			// Treat read errors (EOF, etc.) as "no"
			fmt.Fprintln(ios.ErrOut, "Aborted.")
			return nil
		}
		response = strings.TrimSpace(response)
		if response != "y" && response != "Y" {
			fmt.Fprintln(ios.ErrOut, "Aborted.")
			return nil
		}
	}

	// Prune unused managed images
	// dangling=!opts.All: if --all is false, only prune dangling images
	report, err := client.ImagesPrune(ctx, !opts.All)
	if err != nil {
		cmdutil2.HandleError(ios, err)
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
