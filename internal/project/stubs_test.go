package project_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func chdirForTestProjectExternal(t *testing.T, dir string) {
	t.Helper()
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})
}

func TestNewProjectManagerMock_DefaultsArePanicSafe(t *testing.T) {
	mgr := projectmocks.NewProjectManagerMock()
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

	projectValue, err := mgr.Get(ctx, "/tmp/demo")
	require.Error(t, err)
	assert.ErrorIs(t, err, project.ErrProjectNotFound)
	assert.Nil(t, projectValue)

	projectValue, err = mgr.ResolvePath(ctx, "/tmp/demo")
	require.Error(t, err)
	assert.ErrorIs(t, err, project.ErrProjectNotFound)
	assert.Nil(t, projectValue)

	projectValue, err = mgr.CurrentProject(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, project.ErrProjectNotFound)
	assert.Nil(t, projectValue)
}

func TestNewProjectManagerMock_AllowsOverrides(t *testing.T) {
	mgr := projectmocks.NewProjectManagerMock()
	mgr.GetFunc = func(_ context.Context, root string) (project.Project, error) {
		return projectmocks.NewProjectMockFromRecord(project.ProjectRecord{Name: "override", Root: root}), nil
	}

	project, err := mgr.Get(context.Background(), "/tmp/override")
	require.NoError(t, err)
	require.NotNil(t, project)
	assert.Equal(t, "override", project.Name())
	assert.Equal(t, "/tmp/override", project.RepoPath())
}

func TestNewProjectMock_DefaultsArePanicSafe(t *testing.T) {
	projectValue := projectmocks.NewProjectMock()
	require.NotNil(t, projectValue)

	assert.Equal(t, "test-project", projectValue.Name())
	assert.Equal(t, filepath.Join(os.TempDir(), "clawker-test-repo"), projectValue.RepoPath())

	record, err := projectValue.Record()
	require.NoError(t, err)
	assert.Equal(t, "test-project", record.Name)
	assert.Equal(t, filepath.Join(os.TempDir(), "clawker-test-repo"), record.Root)
	assert.Empty(t, record.Worktrees)

	worktrees, err := projectValue.ListWorktrees(context.Background())
	require.NoError(t, err)
	assert.Empty(t, worktrees)

	_, err = projectValue.GetWorktree(context.Background(), "missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, project.ErrWorktreeNotFound)

	_, err = projectValue.CreateWorktree(context.Background(), "missing", "main")
	require.Error(t, err)
	assert.ErrorIs(t, err, project.ErrWorktreeNotFound)

	_, err = projectValue.AddWorktree(context.Background(), "missing", "main")
	require.Error(t, err)
	assert.ErrorIs(t, err, project.ErrWorktreeNotFound)

	err = projectValue.RemoveWorktree(context.Background(), "missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, project.ErrWorktreeNotFound)

	result, err := projectValue.PruneStaleWorktrees(context.Background(), true)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Failed)
}

func TestNewProjectMockFromRecord_SeedsWorktreeState(t *testing.T) {
	projectValue := projectmocks.NewProjectMockFromRecord(project.ProjectRecord{
		Name: "demo",
		Root: "/tmp/demo",
		Worktrees: map[string]project.WorktreeRecord{
			"feature/x": {Path: "/tmp/wt-x", Branch: "feature/x"},
		},
	})

	state, err := projectValue.GetWorktree(context.Background(), "feature/x")
	require.NoError(t, err)
	assert.Equal(t, "feature/x", state.Branch)
	assert.Equal(t, "/tmp/wt-x", state.Path)
	assert.Equal(t, project.WorktreeHealthy, state.Status)

	path, err := projectValue.CreateWorktree(context.Background(), "feature/x", "main")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/wt-x", path)

	err = projectValue.RemoveWorktree(context.Background(), "feature/x")
	require.NoError(t, err)
}

func TestNewReadOnlyTestManager_CouplesConfigAndGit(t *testing.T) {
	harness := projectmocks.NewReadOnlyTestManager(t, `
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
	assert.ErrorIs(t, err, projectmocks.ErrReadOnlyTestManager)

	_, err = harness.Git.BranchExists("main")
	require.NoError(t, err)
}

func TestNewIsolatedTestManager_ConfigWritesAreIsolated(t *testing.T) {
	harness := projectmocks.NewIsolatedTestManager(t)

	require.NotNil(t, harness)
	require.NotNil(t, harness.Manager)
	require.NotNil(t, harness.Config)
	require.NotNil(t, harness.Git)
	require.NotNil(t, harness.ReadConfigFiles)

	projectRoot := t.TempDir()
	chdirForTestProjectExternal(t, projectRoot)

	_, err := harness.Manager.Register(context.Background(), "Demo", projectRoot)
	require.NoError(t, err)

	var settingsBuf, projectBuf, registryBuf bytes.Buffer
	harness.ReadConfigFiles(&settingsBuf, &projectBuf, &registryBuf)
	assert.Contains(t, registryBuf.String(), "name: Demo")
	assert.Contains(t, registryBuf.String(), "root: "+projectRoot)

	_, err = harness.Git.BranchExists("main")
	require.NoError(t, err)
}
