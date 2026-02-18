// Package prune provides the worktree prune command.
package prune

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// PruneOptions contains the options for the prune command.
type PruneOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() config.Provider

	DryRun bool
}

// NewCmdPrune creates the worktree prune command.
func NewCmdPrune(f *cmdutil.Factory, runF func(context.Context, *PruneOptions) error) *cobra.Command {
	opts := &PruneOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove stale worktree entries from the registry",
		Long: `Removes worktree entries from the project registry when both the worktree
directory and git metadata no longer exist.

This can happen when:
- Native 'git worktree remove' was used (bypasses clawker registry)
- 'clawker worktree remove' failed partway through
- Manual deletion of worktree directory

Use 'clawker worktree list' to see which entries are stale before pruning.`,
		Example: `  # Preview what would be pruned
  clawker worktree prune --dry-run

  # Remove all stale worktree entries
  clawker worktree prune`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return pruneRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Show what would be pruned without removing")

	return cmd
}

func pruneRun(ctx context.Context, opts *PruneOptions) error {
	cfg := opts.Config()
	projectCfg := cfg.ProjectCfg()

	// Check if we're in a registered project
	if !cfg.ProjectFound() {
		return fmt.Errorf("not in a registered project directory")
	}

	// Check if registry is available
	registry, err := cfg.ProjectRegistry()
	if err != nil {
		return fmt.Errorf("loading project registry: %w", err)
	}
	if registry == nil {
		return fmt.Errorf("registry not available")
	}

	// Get project handle
	projectHandle := registry.Project(projectCfg.Key())

	// Get worktree handles
	handles, err := projectHandle.ListWorktrees()
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}

	if len(handles) == 0 {
		fmt.Fprintln(opts.IOStreams.Out, "No worktrees registered for this project.")
		return nil
	}

	// Find prunable entries
	var prunable []config.WorktreeHandle
	for _, h := range handles {
		status := h.Status()
		if status.IsPrunable() {
			prunable = append(prunable, h)
		}
	}

	if len(prunable) == 0 {
		fmt.Fprintln(opts.IOStreams.Out, "No stale entries to prune.")
		return nil
	}

	// Process prunable entries
	var failedCount int
	for _, h := range prunable {
		if opts.DryRun {
			fmt.Fprintf(opts.IOStreams.Out, "Would remove: %s\n", h.Name())
		} else {
			if err := h.Delete(); err != nil {
				fmt.Fprintf(opts.IOStreams.ErrOut, "Failed to remove %s: %v\n", h.Name(), err)
				failedCount++
				continue
			}
			fmt.Fprintf(opts.IOStreams.Out, "Removed: %s\n", h.Name())
		}
	}

	// Summary
	if opts.DryRun {
		if len(prunable) == 1 {
			fmt.Fprintln(opts.IOStreams.Out, "\n1 stale entry would be removed.")
		} else {
			fmt.Fprintf(opts.IOStreams.Out, "\n%d stale entries would be removed.\n", len(prunable))
		}
	} else {
		successCount := len(prunable) - failedCount
		if successCount == 1 {
			fmt.Fprintln(opts.IOStreams.Out, "\n1 stale entry removed.")
		} else if successCount > 0 {
			fmt.Fprintf(opts.IOStreams.Out, "\n%d stale entries removed.\n", successCount)
		}
		if failedCount > 0 {
			return fmt.Errorf("%d of %d entries failed to prune", failedCount, len(prunable))
		}
	}

	return nil
}
