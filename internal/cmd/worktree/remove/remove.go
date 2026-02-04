// Package remove provides the worktree remove command.
package remove

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/spf13/cobra"
)

// RemoveOptions contains the options for the remove command.
type RemoveOptions struct {
	IOStreams  *iostreams.IOStreams
	GitManager func() (*git.GitManager, error)
	Config     func() *config.Config
	Prompter   func() *prompter.Prompter

	Force        bool
	DeleteBranch bool
	Branches     []string
}

// NewCmdRemove creates the worktree remove command.
func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams:  f.IOStreams,
		GitManager: f.GitManager,
		Config:     f.Config,
		Prompter:   f.Prompter,
	}

	cmd := &cobra.Command{
		Use:     "remove BRANCH [BRANCH...]",
		Aliases: []string{"rm"},
		Short:   "Remove one or more worktrees",
		Long: `Removes worktrees by their branch name.

This removes both the git worktree metadata and the filesystem directory.
The branch itself is preserved unless --delete-branch is specified.

If the worktree has uncommitted changes, the command will fail unless
--force is used.`,
		Example: `  # Remove a worktree
  clawker worktree remove feat-42

  # Remove multiple worktrees
  clawker worktree rm feat-42 feat-43

  # Remove worktree and delete the branch
  clawker worktree remove --delete-branch feat-42

  # Force remove a worktree with uncommitted changes
  clawker worktree remove --force feat-42`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Branches = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return removeRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force removal even if worktree has uncommitted changes")
	cmd.Flags().BoolVar(&opts.DeleteBranch, "delete-branch", false, "Also delete the branch after removing the worktree")

	return cmd
}

func removeRun(ctx context.Context, opts *RemoveOptions) error {
	cfg := opts.Config()

	// Check if we're in a registered project
	if !cfg.Project.Found() {
		return fmt.Errorf("not in a registered project directory")
	}

	// Get git manager
	gitMgr, err := opts.GitManager()
	if err != nil {
		return fmt.Errorf("initializing git: %w", err)
	}

	var removeErrors []error

	for _, branch := range opts.Branches {
		if err := removeSingleWorktree(ctx, opts, gitMgr, cfg.Project, branch); err != nil {
			removeErrors = append(removeErrors, fmt.Errorf("%s: %w", branch, err))
		}
	}

	// Report results
	successCount := len(opts.Branches) - len(removeErrors)
	if successCount > 0 {
		if successCount == 1 {
			fmt.Fprintf(opts.IOStreams.ErrOut, "Removed 1 worktree\n")
		} else {
			fmt.Fprintf(opts.IOStreams.ErrOut, "Removed %d worktrees\n", successCount)
		}
	}

	// Return first error if any
	if len(removeErrors) > 0 {
		if len(removeErrors) == 1 {
			return removeErrors[0]
		}
		// Multiple errors - report all
		for _, err := range removeErrors {
			fmt.Fprintf(opts.IOStreams.ErrOut, "Error: %v\n", err)
		}
		return fmt.Errorf("failed to remove %d worktree(s)", len(removeErrors))
	}

	return nil
}

func removeSingleWorktree(ctx context.Context, opts *RemoveOptions, gitMgr *git.GitManager, project *config.Project, branch string) error {
	// Check if worktree has uncommitted changes (if not forcing)
	if !opts.Force {
		// Get worktree path to check for changes
		wtPath, err := project.GetWorktreeDir(branch)
		if err != nil {
			return fmt.Errorf("looking up worktree: %w", err)
		}

		// Try to open the worktree to check for changes
		wt, err := gitMgr.Worktrees()
		if err != nil {
			return fmt.Errorf("initializing worktree manager: %w", err)
		}

		wtRepo, err := wt.Open(wtPath)
		if err != nil {
			// Can't open worktree - require --force to proceed
			return fmt.Errorf("cannot verify worktree status (use --force to remove anyway): %w", err)
		}

		worktree, err := wtRepo.Worktree()
		if err != nil {
			return fmt.Errorf("cannot verify worktree status (use --force to remove anyway): %w", err)
		}

		status, err := worktree.Status()
		if err != nil {
			return fmt.Errorf("cannot verify worktree status (use --force to remove anyway): %w", err)
		}

		if !status.IsClean() {
			return fmt.Errorf("worktree has uncommitted changes (use --force to override)")
		}
	}

	// Remove the worktree
	if err := gitMgr.RemoveWorktree(project, branch); err != nil {
		return err
	}

	// Optionally delete the branch
	if opts.DeleteBranch {
		repo := gitMgr.Repository()
		if err := repo.DeleteBranch(branch); err != nil {
			// Worktree was removed, but branch deletion failed - this is a partial failure
			// Return error so exit code reflects the failure
			return fmt.Errorf("worktree removed but failed to delete branch: %w", err)
		}
		fmt.Fprintf(opts.IOStreams.ErrOut, "Deleted branch %q\n", branch)
	}

	return nil
}
