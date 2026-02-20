package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/git/gittest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWorktreeGitManager struct {
	setupFn  func(dirs git.WorktreeDirProvider, branch, base string) (string, error)
	removeFn func(dirs git.WorktreeDirProvider, branch string) error
}

func (f *fakeWorktreeGitManager) SetupWorktree(dirs git.WorktreeDirProvider, branch, base string) (string, error) {
	if f.setupFn != nil {
		return f.setupFn(dirs, branch, base)
	}
	return "", nil
}

func (f *fakeWorktreeGitManager) RemoveWorktree(dirs git.WorktreeDirProvider, branch string) error {
	if f.removeFn != nil {
		return f.removeFn(dirs, branch)
	}
	return nil
}

func (f *fakeWorktreeGitManager) Worktrees() (*git.WorktreeManager, error) {
	return nil, errors.New("not implemented")
}

func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})
}

func TestWorktreeService_AddWorktree_RegistersWorktree(t *testing.T) {
	cfg, registryPath, _ := newFSConfigFromProjectTestdata(t)
	mgr := NewProjectManager(cfg, nil)
	projectRoot, err := os.Getwd()
	require.NoError(t, err)

	project, err := mgr.Register(context.Background(), "Demo Project", projectRoot)
	require.NoError(t, err)
	require.NotNil(t, project)

	resolvedRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		resolvedRoot = projectRoot
	}
	chdirForTest(t, resolvedRoot)

	svc := newWorktreeService(cfg, nil)
	svc.newManager = func(_ string) (gitManager, error) {
		return &fakeWorktreeGitManager{
			setupFn: func(dirs git.WorktreeDirProvider, branch, _ string) (string, error) {
				return dirs.GetOrCreateWorktreeDir(branch)
			},
		}, nil
	}

	worktreePath, err := svc.AddWorktree(context.Background(), "feature/demo", "")
	require.NoError(t, err)
	assert.NotEmpty(t, worktreePath)

	contents, err := os.ReadFile(registryPath)
	require.NoError(t, err)
	assert.Contains(t, string(contents), "feature/demo")
	assert.Contains(t, string(contents), filepath.Base(worktreePath))
}

func TestWorktreeService_RemoveWorktree_UnregistersWorktree(t *testing.T) {
	cfg, registryPath, _ := newFSConfigFromProjectTestdata(t)
	mgr := NewProjectManager(cfg, nil)
	projectRoot, err := os.Getwd()
	require.NoError(t, err)

	project, err := mgr.Register(context.Background(), "Demo Project", projectRoot)
	require.NoError(t, err)
	require.NotNil(t, project)
	record, err := project.Record()
	require.NoError(t, err)

	resolvedRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		resolvedRoot = projectRoot
	}
	chdirForTest(t, resolvedRoot)

	entry, ok, err := newRegistry(cfg).ProjectByRoot(record.Root)
	require.NoError(t, err)
	require.True(t, ok)
	entry.Worktrees = map[string]config.WorktreeEntry{
		"feature/demo": {
			Path: filepath.Join(config.ConfigDir(), "worktrees", "test", "feature-demo"),
		},
	}
	_, err = newRegistry(cfg).Update(entry)
	require.NoError(t, err)
	require.NoError(t, newRegistry(cfg).Save())

	svc := newWorktreeService(cfg, nil)
	svc.newManager = func(_ string) (gitManager, error) {
		return &fakeWorktreeGitManager{removeFn: func(_ git.WorktreeDirProvider, _ string) error { return nil }}, nil
	}

	err = svc.RemoveWorktree(context.Background(), "feature/demo")
	require.NoError(t, err)

	contents, err := os.ReadFile(registryPath)
	require.NoError(t, err)
	assert.NotContains(t, string(contents), "feature/demo")
}

func TestWorktreeService_CurrentProject_NotRegistered(t *testing.T) {
	cfg, _, _ := newFSConfigFromProjectTestdata(t)
	svc := newWorktreeService(cfg, nil)

	_, err := svc.CurrentProject()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotInProjectPath)
}

func TestWorktreeService_AddWorktree_UsesInMemoryGitManager(t *testing.T) {
	cfg, registryPath, _ := newFSConfigFromProjectTestdata(t)
	mgr := NewProjectManager(cfg, nil)

	projectRoot, err := os.Getwd()
	require.NoError(t, err)

	_, err = mgr.Register(context.Background(), "Demo Project", projectRoot)
	require.NoError(t, err)

	inMem := gittest.NewInMemoryGitManager(t, projectRoot)
	svc := newWorktreeService(cfg, nil)
	svc.newManager = func(_ string) (gitManager, error) {
		return inMem.GitManager, nil
	}

	worktreePath, err := svc.AddWorktree(context.Background(), "feature/demo", "")
	require.NoError(t, err)
	assert.NotEmpty(t, worktreePath)

	exists, err := inMem.BranchExists("feature/demo")
	require.NoError(t, err)
	assert.True(t, exists)

	wtMgr, err := inMem.Worktrees()
	require.NoError(t, err)
	names, err := wtMgr.List()
	require.NoError(t, err)
	assert.Contains(t, names, filepath.Base(worktreePath))

	contents, err := os.ReadFile(registryPath)
	require.NoError(t, err)
	assert.Contains(t, string(contents), "feature/demo")
	assert.Contains(t, string(contents), filepath.Base(worktreePath))
}

func TestWorktreeService_RemoveWorktree_UsesInMemoryGitManager(t *testing.T) {
	cfg, registryPath, _ := newFSConfigFromProjectTestdata(t)
	mgr := NewProjectManager(cfg, nil)

	projectRoot, err := os.Getwd()
	require.NoError(t, err)

	_, err = mgr.Register(context.Background(), "Demo Project", projectRoot)
	require.NoError(t, err)

	inMem := gittest.NewInMemoryGitManager(t, projectRoot)
	svc := newWorktreeService(cfg, nil)
	svc.newManager = func(_ string) (gitManager, error) {
		return inMem.GitManager, nil
	}

	worktreePath, err := svc.AddWorktree(context.Background(), "feature/demo", "")
	require.NoError(t, err)

	err = svc.RemoveWorktree(context.Background(), "feature/demo")
	require.NoError(t, err)

	wtMgr, err := inMem.Worktrees()
	require.NoError(t, err)
	names, err := wtMgr.List()
	require.NoError(t, err)
	assert.NotContains(t, names, filepath.Base(worktreePath))

	contents, err := os.ReadFile(registryPath)
	require.NoError(t, err)
	assert.NotContains(t, string(contents), "feature/demo")
}
