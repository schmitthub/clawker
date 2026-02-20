package project_test

import (
	"context"
	"testing"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectManager_List_SortedByRoot(t *testing.T) {
	cfg := configmocks.NewFromString(`
projects:
  - name: Zeta
    root: /tmp/zeta
  - name: Alpha
    root: /tmp/alpha
`)

	mgr := project.NewProjectManager(cfg)
	projects, err := mgr.List(context.Background())
	require.NoError(t, err)
	require.Len(t, projects, 2)
	assert.Equal(t, "/tmp/alpha", projects[0].Root)
	assert.Equal(t, "/tmp/zeta", projects[1].Root)
}

func TestProjectManager_Get_ProjectNotFound(t *testing.T) {
	mgr := project.NewProjectManager(configmocks.NewBlankConfig())

	projectValue, err := mgr.Get(context.Background(), "/missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, project.ErrProjectNotFound)
	assert.Nil(t, projectValue)
}

func TestProjectManager_Remove_ProjectNotFound(t *testing.T) {
	mgr := project.NewProjectManager(configmocks.NewBlankConfig())

	err := mgr.Remove(context.Background(), "/missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, project.ErrProjectNotFound)
}

func TestProject_NilHandleGuards(t *testing.T) {
	var projectValue *project.ProjectHandleForTest

	_, err := projectValue.Record()
	require.Error(t, err)
	assert.ErrorIs(t, err, project.ErrProjectHandleNotInitialized)

	_, err = projectValue.ListWorktrees(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, project.ErrProjectHandleNotInitialized)

	_, err = projectValue.GetWorktree(context.Background(), "feature/x")
	require.Error(t, err)
	assert.ErrorIs(t, err, project.ErrProjectHandleNotInitialized)
}

func TestProject_GetWorktree_FromRecord(t *testing.T) {
	projectValue := project.NewProjectHandleForTest(project.ProjectRecord{
		Name: "Demo",
		Root: "/tmp/demo",
		Worktrees: map[string]project.WorktreeRecord{
			"feature/x": {Path: "/tmp/wt", Branch: "feature/x"},
		},
	})

	state, err := projectValue.GetWorktree(context.Background(), "feature/x")
	require.NoError(t, err)
	assert.Equal(t, "feature/x", state.Branch)
	assert.Equal(t, "/tmp/wt", state.Path)
	assert.Equal(t, project.WorktreeHealthy, state.Status)
	assert.True(t, state.ExistsInRegistry)
}
