package project

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProjectManagerMock_DefaultsArePanicSafe(t *testing.T) {
	mgr := NewProjectManagerMock()
	require.NotNil(t, mgr)

	ctx := context.Background()

	_, err := mgr.Register(ctx, "demo", "/tmp/demo")
	require.NoError(t, err)

	_, err = mgr.Update(ctx, config.ProjectEntry{Name: "demo", Root: "/tmp/demo"})
	require.NoError(t, err)

	projects, err := mgr.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, projects)

	require.NoError(t, mgr.Remove(ctx, "/tmp/demo"))

	project, err := mgr.Get(ctx, "/tmp/demo")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProjectNotFound)
	assert.Nil(t, project)

	project, err = mgr.ResolvePath(ctx, "/tmp/demo")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProjectNotFound)
	assert.Nil(t, project)

	project, err = mgr.CurrentProject(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProjectNotFound)
	assert.Nil(t, project)
}

func TestNewProjectManagerMock_AllowsOverrides(t *testing.T) {
	mgr := NewProjectManagerMock()
	mgr.GetFunc = func(_ context.Context, root string) (Project, error) {
		return NewProjectMockFromRecord(ProjectRecord{Name: "override", Root: root}), nil
	}

	project, err := mgr.Get(context.Background(), "/tmp/override")
	require.NoError(t, err)
	require.NotNil(t, project)
	assert.Equal(t, "override", project.Name())
	assert.Equal(t, "/tmp/override", project.RepoPath())
}

func TestNewProjectMock_DefaultsArePanicSafe(t *testing.T) {
	project := NewProjectMock()
	require.NotNil(t, project)

	assert.Equal(t, "test-project", project.Name())
	assert.Equal(t, "/tmp/test-project", project.RepoPath())

	record, err := project.Record()
	require.NoError(t, err)
	assert.Equal(t, "test-project", record.Name)
	assert.Equal(t, "/tmp/test-project", record.Root)
	assert.Empty(t, record.Worktrees)

	worktrees, err := project.ListWorktrees(context.Background())
	require.NoError(t, err)
	assert.Empty(t, worktrees)

	_, err = project.GetWorktree(context.Background(), "missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWorktreeNotFound)

	_, err = project.CreateWorktree(context.Background(), "missing", "main")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWorktreeNotFound)

	_, err = project.AddWorktree(context.Background(), "missing", "main")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWorktreeNotFound)

	err = project.RemoveWorktree(context.Background(), "missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWorktreeNotFound)

	result, err := project.PruneStaleWorktrees(context.Background(), true)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Failed)
}

func TestNewProjectMockFromRecord_SeedsWorktreeState(t *testing.T) {
	project := NewProjectMockFromRecord(ProjectRecord{
		Name: "demo",
		Root: "/tmp/demo",
		Worktrees: map[string]WorktreeRecord{
			"feature/x": {Path: "/tmp/wt-x", Branch: "feature/x"},
		},
	})

	state, err := project.GetWorktree(context.Background(), "feature/x")
	require.NoError(t, err)
	assert.Equal(t, "feature/x", state.Branch)
	assert.Equal(t, "/tmp/wt-x", state.Path)
	assert.Equal(t, WorktreeHealthy, state.Status)

	path, err := project.CreateWorktree(context.Background(), "feature/x", "main")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/wt-x", path)

	err = project.RemoveWorktree(context.Background(), "feature/x")
	require.NoError(t, err)
}

func TestNewReadOnlyTestManager_CouplesConfigAndGit(t *testing.T) {
	harness := NewReadOnlyTestManager(t, `
projects:
  - name: Demo
    root: /tmp/demo
`)

	require.NotNil(t, harness)
	require.NotNil(t, harness.Manager)
	require.NotNil(t, harness.Config)
	require.NotNil(t, harness.Git)

	ctx := context.Background()

	projects, err := harness.Manager.List(ctx)
	require.NoError(t, err)
	require.Len(t, projects, 1)
	assert.Equal(t, "Demo", projects[0].Name)

	project, err := harness.Manager.Get(ctx, "/tmp/demo")
	require.NoError(t, err)
	assert.Equal(t, "Demo", project.Name())

	_, err = harness.Manager.Register(ctx, "another", "/tmp/another")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrReadOnlyTestManager)

	_, err = harness.Git.BranchExists("main")
	require.NoError(t, err)
}

func TestNewIsolatedTestManager_ConfigWritesAreIsolated(t *testing.T) {
	harness := NewIsolatedTestManager(t)

	require.NotNil(t, harness)
	require.NotNil(t, harness.Manager)
	require.NotNil(t, harness.Config)
	require.NotNil(t, harness.Git)
	require.NotNil(t, harness.ReadConfigFiles)

	projectRoot := t.TempDir()
	chdirForTest(t, projectRoot)

	_, err := harness.Manager.Register(context.Background(), "Demo", projectRoot)
	require.NoError(t, err)

	var settingsBuf, projectBuf, registryBuf bytes.Buffer
	harness.ReadConfigFiles(&settingsBuf, &projectBuf, &registryBuf)
	assert.Contains(t, registryBuf.String(), "name: Demo")
	assert.Contains(t, registryBuf.String(), "root: "+projectRoot)

	_, err = harness.Git.BranchExists("main")
	require.NoError(t, err)
}
