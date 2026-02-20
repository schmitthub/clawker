// Package add provides the worktree add command.
package add

import (
	"context"
	"errors"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/spf13/cobra"
)

// AddOptions contains the options for the add command.
type AddOptions struct {
	IOStreams *iostreams.IOStreams
	ProjectManager func() (project.ProjectManager, error)

	Branch string
	Base   string
}

// NewCmdAdd creates the worktree add command.
func NewCmdAdd(f *cmdutil.Factory, runF func(context.Context, *AddOptions) error) *cobra.Command {
	opts := &AddOptions{
		IOStreams: f.IOStreams,
		ProjectManager: f.ProjectManager,
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
	projectManager, err := opts.ProjectManager()
	if err != nil {
		return fmt.Errorf("loading project manager: %w", err)
	}

	proj, err := projectManager.FromCWD(context.Background())
	if err != nil {
		if errors.Is(err, project.ErrProjectNotFound) {
			return fmt.Errorf("not in a registered project directory")
		}
		return err
	}

	wtPath, err := proj.CreateWorktree(context.Background(), opts.Branch, opts.Base)
	if err != nil {
		if errors.Is(err, project.ErrNotInProjectPath) || errors.Is(err, project.ErrProjectNotRegistered) {
			return fmt.Errorf("not in a registered project directory")
		}
		return err
	}

	fmt.Fprintf(opts.IOStreams.ErrOut, "Worktree ready at %s\n", wtPath)
	return nil
}
