// Package prune provides the volume prune command.
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
	All   bool
	Force bool
}

// NewCmd creates the volume prune command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
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
  clawker volume prune --force

  # Remove all unused volumes (including named volumes)
  clawker volume prune --all`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Remove all unused volumes, not just anonymous ones")
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
		fmt.Fprint(os.Stderr, "WARNING! This will remove all unused clawker-managed volumes.\nAre you sure you want to continue? [y/N] ")
		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
	}

	// TODO: implement VolumesPrune in pkg/whail/volume.go
	// For now, list and remove volumes one by one as a workaround
	resp, err := client.VolumeList(ctx)
	if err != nil {
		return fmt.Errorf("failed to list volumes: %w", err)
	}

	if len(resp.Volumes) == 0 {
		fmt.Fprintln(os.Stderr, "No unused clawker volumes to remove.")
		return nil
	}

	var removed int
	var reclaimedSpace int64
	for _, v := range resp.Volumes {
		// Try to remove the volume (will fail if in use)
		if err := client.VolumeRemove(ctx, v.Name, false); err != nil {
			// Volume is likely in use, skip it
			continue
		}
		removed++
		reclaimedSpace += v.UsageData.Size
		fmt.Fprintf(os.Stderr, "Deleted: %s\n", v.Name)
	}

	if removed == 0 {
		fmt.Fprintln(os.Stderr, "No unused clawker volumes to remove.")
	} else {
		fmt.Fprintf(os.Stderr, "\nTotal reclaimed space: %s\n", formatBytes(reclaimedSpace))
	}

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
