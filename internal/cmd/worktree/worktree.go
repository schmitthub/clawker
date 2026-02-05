// Package worktree provides commands for managing git worktrees.
package worktree

import (
	"github.com/schmitthub/clawker/internal/cmd/worktree/add"
	"github.com/schmitthub/clawker/internal/cmd/worktree/list"
	"github.com/schmitthub/clawker/internal/cmd/worktree/prune"
	"github.com/schmitthub/clawker/internal/cmd/worktree/remove"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdWorktree creates the worktree parent command.
func NewCmdWorktree(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Manage git worktrees for isolated branch development",
		Long: `Manage git worktrees used by clawker for isolated branch development.

Worktrees allow running containers against different branches simultaneously
without switching branches in your main repository. Each worktree is a
separate checkout of the repository at a specific branch.

Worktrees are created automatically when using 'clawker run --worktree <branch>'
and stored in $CLAWKER_HOME/projects/<project>/worktrees/.`,
		Example: `  # Create a worktree for a new branch
  clawker worktree add feat-42

  # Create a worktree from a specific base
  clawker worktree add feat-43 --base main

  # List all worktrees for the current project
  clawker worktree list

  # Remove a worktree by branch name
  clawker worktree remove feat-42

  # Remove a worktree and also delete the branch
  clawker worktree remove --delete-branch feat-42

  # Force remove a worktree with uncommitted changes
  clawker worktree remove --force feat-42

  # Preview stale entries that would be pruned
  clawker worktree prune --dry-run

  # Remove stale worktree entries from the registry
  clawker worktree prune`,
		// No RunE - this is a parent command
	}

	// Add subcommands
	cmd.AddCommand(add.NewCmdAdd(f, nil))
	cmd.AddCommand(list.NewCmdList(f, nil))
	cmd.AddCommand(prune.NewCmdPrune(f, nil))
	cmd.AddCommand(remove.NewCmdRemove(f, nil))

	return cmd
}
