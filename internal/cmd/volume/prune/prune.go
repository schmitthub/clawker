// Package prune provides the volume prune command.
package prune

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
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

// NewCmdPrune creates the volume prune command.
func NewCmdPrune(f *cmdutil.Factory, runF func(context.Context, *PruneOptions) error) *cobra.Command {
	opts := &PruneOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
		Prompter:  f.Prompter,
	}

	cmd := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove unused agent volumes",
		Long: `Removes unused clawker-managed agent volumes (volumes labeled with purpose=agent).

By default only agent volumes are pruned. Other clawker-managed volumes
(monitoring, firewall, control plane, etc.) are preserved unless --all is set.
Use with caution as this will permanently delete data.`,
		Example: `  # Remove unused agent volumes
  clawker volume prune

  # Remove all unused clawker-managed volumes (agent, monitoring, etc.)
  clawker volume prune --all

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
	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Remove all clawker-managed volumes (default: only agent volumes)")

	return cmd
}

func pruneRun(ctx context.Context, opts *PruneOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connect to docker: %w", err)
	}

	scope := "unused agent volumes"
	emptyMsg := "No unused agent volumes to remove."
	if opts.All {
		scope = "all unused clawker-managed volumes"
		emptyMsg = "No unused clawker volumes to remove."
	}

	if !opts.Force {
		confirmed, err := opts.Prompter().Confirm(fmt.Sprintf("%s This will remove %s.", cs.WarningIcon(), scope), false)
		if err != nil {
			return fmt.Errorf("confirm prune: %w", err)
		}
		if !confirmed {
			fmt.Fprintln(ios.ErrOut, "Aborted.")
			return nil
		}
	}

	var extraFilters []map[string]string
	if !opts.All {
		extraFilters = append(extraFilters, map[string]string{consts.LabelPurpose: consts.PurposeAgent})
	}

	report, err := client.VolumesPrune(ctx, true, extraFilters...)
	if err != nil {
		return fmt.Errorf("prune volumes: %w", err)
	}

	if len(report.Report.VolumesDeleted) == 0 {
		fmt.Fprintln(ios.ErrOut, emptyMsg)
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
