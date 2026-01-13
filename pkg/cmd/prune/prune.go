package prune

import (
	"context"

	"github.com/schmitthub/clawker/pkg/cmd/remove"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type pruneOptions struct {
	all   bool
	force bool
}

// NewCmdPrune creates the prune command.
// This is an alias for 'remove --unused' for users familiar with docker prune.
func NewCmdPrune(f *cmdutil.Factory) *cobra.Command {
	opts := &pruneOptions{}

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove unused clawker resources (alias for 'remove --unused')",
		Long: `Remove unused clawker resources (stopped containers, dangling images).

This is an alias for 'clawker remove --unused'.

By default, prune removes:
  - Stopped clawker containers
  - Dangling clawker images

With --all, prune removes ALL clawker resources:
  - All clawker containers (stopped)
  - All clawker images
  - All clawker volumes
  - The clawker-net network (if unused)

WARNING: --all is destructive and will remove persistent data!`,
		Example: `  # Remove unused resources (stopped containers, dangling images)
  clawker prune

  # Remove ALL clawker resources (including volumes)
  clawker prune --all

  # Skip confirmation prompt
  clawker prune --all --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return remove.RunPrune(context.Background(), opts.all, opts.force)
		},
	}

	cmd.Flags().BoolVarP(&opts.all, "all", "a", false, "Remove ALL clawker resources (including volumes)")
	cmd.Flags().BoolVarP(&opts.force, "force", "f", false, "Skip confirmation prompt")

	return cmd
}
