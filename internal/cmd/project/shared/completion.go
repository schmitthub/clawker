package shared

import (
	"context"
	"slices"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/project"
)

// NameCompletions returns a cobra completion function that suggests registered
// project names for shell tab-completion. Use it as ValidArgsFunction on
// commands that take project names as positional args, or register it for
// flags that take a project name. Names already present in the command's
// positional args are excluded so multi-arg commands (e.g. project remove)
// don't re-suggest what was typed. Registry entries are read via
// ProjectManager.List — the cheap path with no filesystem health checks — so
// completion stays fast. All failures degrade to no suggestions — completion
// must never surface errors.
func NameCompletions(pmFn func() (project.ProjectManager, error)) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return nameSuggestions(cmd.Context(), pmFn, args), cobra.ShellCompDirectiveNoFileComp
	}
}

func nameSuggestions(
	ctx context.Context,
	pmFn func() (project.ProjectManager, error),
	typedArgs []string,
) []cobra.Completion {
	if pmFn == nil {
		return nil
	}

	mgr, err := pmFn()
	if err != nil {
		cobra.CompDebugln("clawker project completion: project manager: "+err.Error(), false)
		return nil
	}

	entries, err := mgr.List(ctx)
	if err != nil {
		cobra.CompDebugln("clawker project completion: list projects: "+err.Error(), false)
		return nil
	}

	typed := make(map[string]bool, len(typedArgs))
	for _, a := range typedArgs {
		typed[a] = true
	}

	var completions []cobra.Completion
	for _, e := range entries {
		if e.Name != "" && !typed[e.Name] {
			completions = append(completions, e.Name)
		}
	}
	slices.Sort(completions)
	return completions
}
