// Package remove provides the worktree remove command.
package remove

import (
	"context"
	"errors"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/spf13/cobra"
)

// RemoveOptions contains the options for the remove command.
type RemoveOptions struct {
	IOStreams  *iostreams.IOStreams
	GitManager func() (*git.GitManager, error)
	ProjectManager func() (project.ProjectManager, error)
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
		ProjectManager: f.ProjectManager,
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
	projectManager, err := opts.ProjectManager()
	if err != nil {
		return fmt.Errorf("loading project manager: %w", err)
	}

	// Get git manager
	gitMgr, err := opts.GitManager()
	if err != nil {
		return fmt.Errorf("initializing git: %w", err)
	}

	proj, err := projectManager.FromCWD(ctx)
	if err != nil {
		if errors.Is(err, project.ErrProjectNotFound) {
			return fmt.Errorf("not in a registered project directory")
		}
		return err
	}

	var removeErrors []error

	for _, branch := range opts.Branches {
		if err := removeSingleWorktree(ctx, opts, proj, gitMgr, branch); err != nil {
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


func removeSingleWorktree(ctx context.Context, opts *RemoveOptions, proj *project.Project, gitMgr *git.GitManager, branch string) error {
	if err := proj.RemoveWorktree(ctx, branch); err != nil {
		if errors.Is(err, project.ErrNotInProjectPath) || errors.Is(err, project.ErrProjectNotRegistered) {
			return fmt.Errorf("not in a registered project directory")
		}
		return err
	}

	// Optionally delete the branch
	if opts.DeleteBranch {
		if err := handleBranchDelete(opts.IOStreams, gitMgr, branch); err != nil {
			return fmt.Errorf("worktree removed but %w", err)
		}
	}

	return nil
}

// handleBranchDelete handles branch deletion with user-friendly error reporting.
// Returns nil if the branch was deleted, was already gone, or had unmerged commits (warning printed).
func handleBranchDelete(ios *iostreams.IOStreams, gitMgr *git.GitManager, branch string) error {
	if err := gitMgr.DeleteBranch(branch); err != nil {
		if errors.Is(err, git.ErrBranchNotMerged) {
			cs := ios.ColorScheme()
			fmt.Fprintf(ios.ErrOut, "%s branch %q has unmerged commits\n",
				cs.WarningIcon(), branch)
			fmt.Fprintf(ios.ErrOut, "  To force delete: git branch -D %s\n", branch)
			return nil
		}
		if errors.Is(err, git.ErrBranchNotFound) {
			// Branch already deleted or never existed — not an error
			return nil
		}
		return fmt.Errorf("failed to delete branch: %w", err)
	}
	fmt.Fprintf(ios.ErrOut, "Deleted branch %q\n", branch)
	return nil
}
