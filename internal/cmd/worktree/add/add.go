// Package add provides the worktree add command.
package add

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// AddOptions contains the options for the add command.
type AddOptions struct {
	IOStreams  *iostreams.IOStreams
	GitManager func() (*git.GitManager, error)
	Config     func() *config.Config

	Branch string
	Base   string
}

// NewCmdAdd creates the worktree add command.
func NewCmdAdd(f *cmdutil.Factory, runF func(context.Context, *AddOptions) error) *cobra.Command {
	opts := &AddOptions{
		IOStreams:  f.IOStreams,
		GitManager: f.GitManager,
		Config:     f.Config,
	}

	cmd := &cobra.Command{
		Use:   "add BRANCH",
		Short: "Create a worktree for a branch",
		Long: `Creates a git worktree for the specified branch.

If the worktree already exists, the command succeeds (idempotent).
If the branch exists but isn't checked out elsewhere, it's checked out in the new worktree.
If the branch doesn't exist, it's created from the base ref (default: HEAD).`,
		Example: `  # Create a worktree for a new branch
  clawker worktree add feat-42

  # Create a worktree from a specific base
  clawker worktree add feat-43 --base main

  # Create a worktree for a branch with slashes
  clawker worktree add feature/new-login`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Branch = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return addRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Base, "base", "", "Base ref to create branch from (default: HEAD)")

	return cmd
}

func addRun(_ context.Context, opts *AddOptions) error {
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

	// SetupWorktree handles all the logic:
	// - If worktree exists, returns the existing path
	// - If branch exists, checks it out in new worktree
	// - If branch doesn't exist, creates it from base
	wtPath, err := gitMgr.SetupWorktree(cfg.Project, opts.Branch, opts.Base)
	if err != nil {
		return fmt.Errorf("creating worktree: %w", err)
	}

	fmt.Fprintf(opts.IOStreams.ErrOut, "Worktree ready at %s\n", wtPath)
	return nil
}
