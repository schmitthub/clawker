package shared_test

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"github.com/schmitthub/clawker/internal/cmd/worktree/shared"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
)

func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(context.Background())
	return cmd
}

func healthyWorktree(branch string) project.WorktreeState {
	return project.WorktreeState{
		Project:          "demo",
		Branch:           branch,
		Path:             "/worktrees/" + branch,
		Head:             "abc1234",
		IsDetached:       false,
		ExistsInRegistry: true,
		ExistsInGit:      true,
		Status:           project.WorktreeHealthy,
		IsLocked:         false,
		InspectError:     nil,
	}
}

func managerWithWorktrees(states []project.WorktreeState) func() (project.ProjectManager, error) {
	proj := projectmocks.NewMockProject("demo", "/repo")
	proj.ListWorktreesFunc = func(ctx context.Context) ([]project.WorktreeState, error) {
		return states, nil
	}
	mgr := projectmocks.NewMockProjectManager()
	mgr.CurrentProjectFunc = func(ctx context.Context) (project.Project, error) {
		return proj, nil
	}
	return func() (project.ProjectManager, error) { return mgr, nil }
}

func TestBranchCompletions_ReturnsSortedBranches(t *testing.T) {
	fn := shared.BranchCompletions(managerWithWorktrees([]project.WorktreeState{
		healthyWorktree("feat-b"),
		healthyWorktree("feat-a"),
		healthyWorktree("fix-1"),
	}))

	completions, directive := fn(newTestCmd(), nil, "")
	assert.Equal(t, []cobra.Completion{"feat-a", "feat-b", "fix-1"}, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestBranchCompletions_ExcludesAlreadyTypedArgs(t *testing.T) {
	fn := shared.BranchCompletions(managerWithWorktrees([]project.WorktreeState{
		healthyWorktree("feat-a"),
		healthyWorktree("feat-b"),
	}))

	completions, directive := fn(newTestCmd(), []string{"feat-a"}, "")
	assert.Equal(t, []cobra.Completion{"feat-b"}, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestBranchCompletions_IncludesUnhealthyWorktrees(t *testing.T) {
	detached := healthyWorktree("feat-detached")
	detached.IsDetached = true
	broken := healthyWorktree("feat-broken")
	broken.Status = project.WorktreeBroken
	prunable := healthyWorktree("feat-prunable")
	prunable.Status = project.WorktreeRegistryOnly

	fn := shared.BranchCompletions(managerWithWorktrees([]project.WorktreeState{
		healthyWorktree("feat-a"),
		detached,
		broken,
		prunable,
	}))

	completions, _ := fn(newTestCmd(), nil, "")
	assert.Equal(t, []cobra.Completion{"feat-a", "feat-broken", "feat-detached", "feat-prunable"}, completions)
}

func TestBranchCompletions_DegradesToNoSuggestions(t *testing.T) {
	tests := []struct {
		name string
		pmFn func() (project.ProjectManager, error)
	}{
		{
			name: "manager error",
			pmFn: func() (project.ProjectManager, error) { return nil, errors.New("boom") },
		},
		{
			name: "current project error",
			pmFn: func() (project.ProjectManager, error) {
				return projectmocks.NewMockProjectManager(), nil // CurrentProject returns ErrProjectNotFound
			},
		},
		{
			name: "list worktrees error",
			pmFn: func() (project.ProjectManager, error) {
				proj := projectmocks.NewMockProject("demo", "/repo")
				proj.ListWorktreesFunc = func(ctx context.Context) ([]project.WorktreeState, error) {
					return nil, errors.New("boom")
				}
				mgr := projectmocks.NewMockProjectManager()
				mgr.CurrentProjectFunc = func(ctx context.Context) (project.Project, error) {
					return proj, nil
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
			fn := shared.BranchCompletions(tt.pmFn)

			completions, directive := fn(newTestCmd(), nil, "")
			assert.Nil(t, completions)
			assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
		})
	}
}
