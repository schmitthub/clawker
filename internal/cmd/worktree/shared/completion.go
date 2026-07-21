// Package shared holds helpers used across worktree subcommands and other
// commands that accept worktree branch arguments.
package shared

import (
	"context"
	"slices"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/project"
)

// BranchCompletions returns a cobra completion function that suggests the
// current project's worktree branch names for shell tab-completion. Branches
// already present in the command's positional args are excluded so multi-arg
// commands (e.g. worktree remove) don't re-suggest what was typed. Detached
// worktrees carry no branch name and are skipped. All failures degrade to no
// suggestions — completion must never surface errors.
func BranchCompletions(pmFn func() (project.ProjectManager, error)) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return branchSuggestions(cmd.Context(), pmFn, args), cobra.ShellCompDirectiveNoFileComp
	}
}

func branchSuggestions(
	ctx context.Context,
	pmFn func() (project.ProjectManager, error),
	typedArgs []string,
) []cobra.Completion {
	mgr, err := pmFn()
	if err != nil {
		return nil
	}

	proj, err := mgr.CurrentProject(ctx)
	if err != nil {
		return nil
	}

	states, err := proj.ListWorktrees(ctx)
	if err != nil {
		return nil
	}

	typed := make(map[string]bool, len(typedArgs))
	for _, a := range typedArgs {
		typed[a] = true
	}

	var completions []cobra.Completion
	for _, wt := range states {
		if wt.Branch != "" && !typed[wt.Branch] {
			completions = append(completions, wt.Branch)
		}
	}
	slices.Sort(completions)
	return completions
}
