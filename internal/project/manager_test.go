package project

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectManager_List_SortedByRoot(t *testing.T) {
	cfg := config.NewFromString(`
projects:
  - name: Zeta
    root: /tmp/zeta
  - name: Alpha
    root: /tmp/alpha
`)

	mgr := NewProjectManager(cfg)
	projects, err := mgr.List(context.Background())
	require.NoError(t, err)
	require.Len(t, projects, 2)
	assert.Equal(t, "/tmp/alpha", projects[0].Root)
	assert.Equal(t, "/tmp/zeta", projects[1].Root)
}

func TestProjectManager_Get_ProjectNotFound(t *testing.T) {
	mgr := NewProjectManager(config.NewBlankConfig())

	project, err := mgr.Get(context.Background(), "/missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProjectNotFound)
	assert.Nil(t, project)
}

func TestProjectManager_Remove_ProjectNotFound(t *testing.T) {
	mgr := NewProjectManager(config.NewBlankConfig())

	err := mgr.Remove(context.Background(), "/missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProjectNotFound)
}

func TestProject_NilHandleGuards(t *testing.T) {
	var project *projectHandle

	_, err := project.Record()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProjectHandleNotInitialized)

	_, err = project.ListWorktrees(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProjectHandleNotInitialized)

	_, err = project.GetWorktree(context.Background(), "feature/x")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProjectHandleNotInitialized)
}

func TestProject_GetWorktree_FromRecord(t *testing.T) {
	project := &projectHandle{
		record: ProjectRecord{
			Name: "Demo",
			Root: "/tmp/demo",
			Worktrees: map[string]WorktreeRecord{
				"feature/x": {Path: "/tmp/wt", Branch: "feature/x"},
			},
		},
	}

	state, err := project.GetWorktree(context.Background(), "feature/x")
	require.NoError(t, err)
	assert.Equal(t, "feature/x", state.Branch)
	assert.Equal(t, "/tmp/wt", state.Path)
	assert.Equal(t, WorktreeHealthy, state.Status)
	assert.True(t, state.ExistsInRegistry)
}
