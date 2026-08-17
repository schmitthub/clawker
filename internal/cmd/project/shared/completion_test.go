package shared_test

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"github.com/schmitthub/clawker/internal/cmd/project/shared"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
)

func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(context.Background())
	return cmd
}

func managerWithProjects(names ...string) func() (project.ProjectManager, error) {
	entries := make([]project.ProjectEntry, 0, len(names))
	for _, n := range names {
		entries = append(entries, project.ProjectEntry{Name: n, Root: "/repos/" + n, Worktrees: nil})
	}
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(ctx context.Context) ([]project.ProjectEntry, error) {
		return entries, nil
	}
	return func() (project.ProjectManager, error) { return mgr, nil }
}

func TestNameCompletions_ReturnsSortedNames(t *testing.T) {
	fn := shared.NameCompletions(managerWithProjects("zeta", "alpha", "mid"))

	completions, directive := fn(newTestCmd(), nil, "")
	assert.Equal(t, []cobra.Completion{"alpha", "mid", "zeta"}, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestNameCompletions_ExcludesAlreadyTypedArgs(t *testing.T) {
	fn := shared.NameCompletions(managerWithProjects("alpha", "beta"))

	completions, directive := fn(newTestCmd(), []string{"alpha"}, "")
	assert.Equal(t, []cobra.Completion{"beta"}, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestNameCompletions_SkipsEmptyNames(t *testing.T) {
	fn := shared.NameCompletions(managerWithProjects("alpha", ""))

	completions, _ := fn(newTestCmd(), nil, "")
	assert.Equal(t, []cobra.Completion{"alpha"}, completions)
}

func TestNameCompletions_DegradesToNoSuggestions(t *testing.T) {
	tests := []struct {
		name string
		pmFn func() (project.ProjectManager, error)
	}{
		{
			name: "manager error",
			pmFn: func() (project.ProjectManager, error) { return nil, errors.New("boom") },
		},
		{
			name: "list error",
			pmFn: func() (project.ProjectManager, error) {
				mgr := projectmocks.NewMockProjectManager()
				mgr.ListFunc = func(ctx context.Context) ([]project.ProjectEntry, error) {
					return nil, errors.New("boom")
				}
				return mgr, nil
			},
		},
		{
			name: "nil manager func",
			pmFn: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := shared.NameCompletions(tt.pmFn)

			completions, directive := fn(newTestCmd(), nil, "")
			assert.Nil(t, completions)
			assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
		})
	}
}
