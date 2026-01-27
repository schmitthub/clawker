// Package prune provides the volume prune command.
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
			return run(cmd, f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Do not prompt for confirmation")

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
		fmt.Fprintf(ios.ErrOut, "%s This will remove all unused clawker-managed volumes.\nAre you sure you want to continue? [y/N] ", cs.WarningIcon())
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

	// Prune all unused managed volumes (all=true to include named volumes)
	report, err := client.VolumesPrune(ctx, true)
	if err != nil {
		cmdutil2.HandleError(ios, err)
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
