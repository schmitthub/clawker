// Package prune provides the worktree prune command.
package prune

import (
	"context"
	"errors"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/spf13/cobra"
)

// PruneOptions contains the options for the prune command.
type PruneOptions struct {
	IOStreams *iostreams.IOStreams
	ProjectManager func() (project.ProjectManager, error)

	DryRun bool
}

// NewCmdPrune creates the worktree prune command.
func NewCmdPrune(f *cmdutil.Factory, runF func(context.Context, *PruneOptions) error) *cobra.Command {
	opts := &PruneOptions{
		IOStreams: f.IOStreams,
		ProjectManager: f.ProjectManager,
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
	projectManager, err := opts.ProjectManager()
	if err != nil {
		return fmt.Errorf("loading project manager: %w", err)
	}

	proj, err := projectManager.CurrentProject(ctx)
	if err != nil {
		if errors.Is(err, project.ErrProjectNotFound) {
			return fmt.Errorf("not in a registered project directory")
		}
		return err
	}

	result, err := proj.PruneStaleWorktrees(ctx, opts.DryRun)
	if err != nil {
		if errors.Is(err, project.ErrNotInProjectPath) {
			return fmt.Errorf("not in a registered project directory")
		}
		return err
	}

	if len(result.Prunable) == 0 {
		fmt.Fprintln(opts.IOStreams.Out, "No stale entries to prune.")
		return nil
	}

	// Process prunable entries
	for _, name := range result.Prunable {
		if opts.DryRun {
			fmt.Fprintf(opts.IOStreams.Out, "Would remove: %s\n", name)
		} else {
			if _, failed := result.Failed[name]; !failed {
				fmt.Fprintf(opts.IOStreams.Out, "Removed: %s\n", name)
			}
		}
	}

	// Summary
	if opts.DryRun {
		if len(result.Prunable) == 1 {
			fmt.Fprintln(opts.IOStreams.Out, "\n1 stale entry would be removed.")
		} else {
			fmt.Fprintf(opts.IOStreams.Out, "\n%d stale entries would be removed.\n", len(result.Prunable))
		}
	} else {
		successCount := len(result.Removed)
		if successCount == 1 {
			fmt.Fprintln(opts.IOStreams.Out, "\n1 stale entry removed.")
		} else if successCount > 0 {
			fmt.Fprintf(opts.IOStreams.Out, "\n%d stale entries removed.\n", successCount)
		}
		failedCount := len(result.Failed)
		if failedCount > 0 {
			for name, failedErr := range result.Failed {
				fmt.Fprintf(opts.IOStreams.ErrOut, "Failed to remove %s: %v\n", name, failedErr)
			}
			return fmt.Errorf("%d of %d entries failed to prune", failedCount, len(result.Prunable))
		}
	}

	return nil
}
