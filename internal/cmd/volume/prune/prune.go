// Package prune provides the volume prune command.
package prune

import (
	"context"
	"fmt"
	"os"

	cmdutil2 "github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the prune command.
type Options struct {
	Force bool
}

// NewCmd creates the volume prune command.
func NewCmd(f *cmdutil2.Factory) *cobra.Command {
	opts := &Options{}

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
		Annotations: map[string]string{
			cmdutil2.AnnotationRequiresProject: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Do not prompt for confirmation")

	return cmd
}

func run(f *cmdutil2.Factory, opts *Options) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil2.HandleError(err)
		return err
	}

	// Prompt for confirmation if not forced
	if !opts.Force {
		fmt.Fprint(os.Stderr, "WARNING! This will remove all unused clawker-managed volumes.\nAre you sure you want to continue? [y/N] ")
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

	// Prune all unused managed volumes (all=true to include named volumes)
	report, err := client.VolumesPrune(ctx, true)
	if err != nil {
		cmdutil2.HandleError(err)
		return err
	}

	if len(report.Report.VolumesDeleted) == 0 {
		fmt.Fprintln(os.Stderr, "No unused clawker volumes to remove.")
		return nil
	}

	for _, name := range report.Report.VolumesDeleted {
		fmt.Fprintf(os.Stderr, "Deleted: %s\n", name)
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
