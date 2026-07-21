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

func TestBranchCompletions_SkipsDetachedWorktrees(t *testing.T) {
	detached := healthyWorktree("")
	detached.IsDetached = true

	fn := shared.BranchCompletions(managerWithWorktrees([]project.WorktreeState{
		healthyWorktree("feat-a"),
		detached,
	}))

	completions, _ := fn(newTestCmd(), nil, "")
	assert.Equal(t, []cobra.Completion{"feat-a"}, completions)
}

func TestBranchCompletions_ManagerError(t *testing.T) {
	fn := shared.BranchCompletions(func() (project.ProjectManager, error) {
		return nil, errors.New("boom")
	})

	completions, directive := fn(newTestCmd(), nil, "")
	assert.Nil(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestBranchCompletions_CurrentProjectError(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager() // CurrentProject returns ErrProjectNotFound
	fn := shared.BranchCompletions(func() (project.ProjectManager, error) { return mgr, nil })

	completions, directive := fn(newTestCmd(), nil, "")
	assert.Nil(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestBranchCompletions_ListWorktreesError(t *testing.T) {
	proj := projectmocks.NewMockProject("demo", "/repo")
	proj.ListWorktreesFunc = func(ctx context.Context) ([]project.WorktreeState, error) {
		return nil, errors.New("boom")
	}
	mgr := projectmocks.NewMockProjectManager()
	mgr.CurrentProjectFunc = func(ctx context.Context) (project.Project, error) {
		return proj, nil
	}
	fn := shared.BranchCompletions(func() (project.ProjectManager, error) { return mgr, nil })

	completions, directive := fn(newTestCmd(), nil, "")
	assert.Nil(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}
