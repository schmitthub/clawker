package project_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/git/gittest"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// debugDirs walks a directory tree, logs every entry, and prints file contents inline.
func debugDirs(t *testing.T, label, root string) {
	t.Helper()
	t.Logf("=== %s: %s ===", label, root)
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			t.Logf("  [walk error] %s: %v", path, err)
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		if info.IsDir() {
			t.Logf("  d %s/", rel)
			return nil
		}
		t.Logf("  f %s (%d bytes)", rel, info.Size())
		if info.Size() > 0 && info.Size() < 4096 {
			data, readErr := os.ReadFile(path)
			if readErr == nil {
				t.Logf("    contents:\n%s", string(data))
			}
		}
		return nil
	})
}

// dumpConfigFiles reads config files via the reader callback and logs their contents.
func dumpConfigFiles(t *testing.T, label string, readFn func(s, u, r, reg *bytes.Buffer)) {
	t.Helper()
	var settings, userProj, repoProj, registry bytes.Buffer
	readFn(&settings, &userProj, &repoProj, &registry)
	t.Logf("=== %s: config file contents ===", label)
	t.Logf("--- settings.yaml ---\n%s", settings.String())
	t.Logf("--- user clawker.yaml ---\n%s", userProj.String())
	t.Logf("--- repo clawker.yaml ---\n%s", repoProj.String())
	t.Logf("--- projects.yaml (registry) ---\n%s", registry.String())
}

func TestProjectManager_FullLifecycle(t *testing.T) {
	t.Run("register then add worktree", func(t *testing.T) {
		cfg, readFiles := configmocks.NewIsolatedTestConfig(t)
		root := os.Getenv(cfg.TestRepoDirEnvVar())
		resolvedRoot, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)

		// Debug: show env vars and directory layout
		t.Logf("CLAWKER_CONFIG_DIR = %s", os.Getenv(cfg.ConfigDirEnvVar()))
		t.Logf("CLAWKER_DATA_DIR   = %s", os.Getenv(cfg.DataDirEnvVar()))
		t.Logf("CLAWKER_STATE_DIR  = %s", os.Getenv(cfg.StateDirEnvVar()))
		t.Logf("CLAWKER_TEST_REPO_DIR = %s", root)
		t.Logf("resolvedRoot       = %s", resolvedRoot)
		t.Logf("config.ConfigDir() = %s", config.ConfigDir())
		t.Logf("config.DataDir()   = %s", config.DataDir())
		t.Logf("config.StateDir()  = %s", config.StateDir())
		wtDir, wtErr := cfg.WorktreesSubdir()
		t.Logf("cfg.WorktreesSubdir() = %s (err=%v)", wtDir, wtErr)

		base := filepath.Dir(os.Getenv(cfg.ConfigDirEnvVar())) // parent of config/
		debugDirs(t, "initial temp layout", base)

		// In-memory git repo — memfs storer, osfs worktree checkout (cross-FS).
		inMemGit := gittest.NewInMemoryGitManager(t, resolvedRoot)
		factory := func(_ string) (*git.GitManager, error) {
			return inMemGit.GitManager, nil
		}

		mgr := project.NewProjectManager(cfg, factory)
		ctx := context.Background()

		// Register
		proj, err := mgr.Register(ctx, "my-app", resolvedRoot)
		require.NoError(t, err)
		assert.Equal(t, "my-app", proj.Name())

		t.Log("\n--- after Register ---")
		debugDirs(t, "post-register layout", base)
		dumpConfigFiles(t, "post-register", func(s, u, r, reg *bytes.Buffer) {
			readFiles(s, u, r, reg)
		})

		// chdir so GetProjectRoot() resolves against the registry
		oldWd, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(resolvedRoot))
		t.Cleanup(func() { _ = os.Chdir(oldWd) })
		t.Logf("cwd changed to: %s", resolvedRoot)

		// Add worktree
		state, err := proj.AddWorktree(ctx, "feature/test-branch", "")
		require.NoError(t, err)
		assert.Equal(t, "feature/test-branch", state.Branch)
		assert.NotEmpty(t, state.Path)
		assert.True(t, state.ExistsInGit)
		assert.True(t, state.ExistsInRegistry)

		t.Log("\n--- after AddWorktree ---")
		t.Logf("state.Branch         = %s", state.Branch)
		t.Logf("state.Path           = %s", state.Path)
		t.Logf("state.ExistsInGit    = %v", state.ExistsInGit)
		t.Logf("state.ExistsInRegistry = %v", state.ExistsInRegistry)
		debugDirs(t, "post-worktree layout", base)
		dumpConfigFiles(t, "post-worktree", func(s, u, r, reg *bytes.Buffer) {
			readFiles(s, u, r, reg)
		})

		// Worktree directory exists on disk
		_, err = os.Stat(state.Path)
		assert.NoError(t, err, "worktree directory should exist on disk")
		if err == nil {
			debugDirs(t, "worktree checkout contents", state.Path)
		}

		// Record reflects the worktree
		record, err := proj.Record()
		require.NoError(t, err)
		t.Logf("record.Name = %s, record.Root = %s", record.Name, record.Root)
		t.Logf("record.Worktrees = %+v", record.Worktrees)
		assert.Equal(t, "my-app", record.Name)
		require.Contains(t, record.Worktrees, "feature/test-branch")
		assert.Equal(t, state.Path, record.Worktrees["feature/test-branch"].Path)
	})

	t.Run("remove worktree preserves branch", func(t *testing.T) {
		cfg, _ := configmocks.NewIsolatedTestConfig(t)
		root := os.Getenv(cfg.TestRepoDirEnvVar())
		resolvedRoot, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)

		inMemGit := gittest.NewInMemoryGitManager(t, resolvedRoot)
		factory := func(_ string) (*git.GitManager, error) {
			return inMemGit.GitManager, nil
		}

		mgr := project.NewProjectManager(cfg, factory)
		ctx := context.Background()

		proj, err := mgr.Register(ctx, "my-app", resolvedRoot)
		require.NoError(t, err)

		oldWd, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(resolvedRoot))
		t.Cleanup(func() { _ = os.Chdir(oldWd) })

		state, err := proj.AddWorktree(ctx, "feature/keep-branch", "")
		require.NoError(t, err)

		// Remove worktree WITHOUT deleting branch
		err = proj.RemoveWorktree(ctx, "feature/keep-branch", false)
		require.NoError(t, err)

		// Worktree directory gone
		_, err = os.Stat(state.Path)
		assert.True(t, os.IsNotExist(err), "worktree directory should be removed")

		// Record no longer contains worktree
		record, err := proj.Record()
		require.NoError(t, err)
		assert.Empty(t, record.Worktrees)

		// Branch still exists in git
		exists, err := inMemGit.GitManager.BranchExists("feature/keep-branch")
		require.NoError(t, err)
		assert.True(t, exists, "branch should survive worktree removal")
	})

	t.Run("remove worktree with delete-branch", func(t *testing.T) {
		cfg, _ := configmocks.NewIsolatedTestConfig(t)
		root := os.Getenv(cfg.TestRepoDirEnvVar())
		resolvedRoot, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)

		inMemGit := gittest.NewInMemoryGitManager(t, resolvedRoot)
		factory := func(_ string) (*git.GitManager, error) {
			return inMemGit.GitManager, nil
		}

		mgr := project.NewProjectManager(cfg, factory)
		ctx := context.Background()

		proj, err := mgr.Register(ctx, "my-app", resolvedRoot)
		require.NoError(t, err)

		oldWd, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(resolvedRoot))
		t.Cleanup(func() { _ = os.Chdir(oldWd) })

		state, err := proj.AddWorktree(ctx, "feature/delete-me", "")
		require.NoError(t, err)

		// Remove worktree AND delete branch
		err = proj.RemoveWorktree(ctx, "feature/delete-me", true)
		require.NoError(t, err)

		// Worktree directory gone
		_, err = os.Stat(state.Path)
		assert.True(t, os.IsNotExist(err), "worktree directory should be removed")

		// Record no longer contains worktree
		record, err := proj.Record()
		require.NoError(t, err)
		assert.Empty(t, record.Worktrees)

		// Branch deleted from git
		exists, err := inMemGit.GitManager.BranchExists("feature/delete-me")
		require.NoError(t, err)
		assert.False(t, exists, "branch should be deleted")
	})

	t.Run("duplicate worktree errors then remove project preserves worktree dir", func(t *testing.T) {
		cfg, _ := configmocks.NewIsolatedTestConfig(t)
		root := os.Getenv(cfg.TestRepoDirEnvVar())
		resolvedRoot, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)

		inMemGit := gittest.NewInMemoryGitManager(t, resolvedRoot)
		factory := func(_ string) (*git.GitManager, error) {
			return inMemGit.GitManager, nil
		}

		mgr := project.NewProjectManager(cfg, factory)
		ctx := context.Background()

		proj, err := mgr.Register(ctx, "my-app", resolvedRoot)
		require.NoError(t, err)

		oldWd, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(resolvedRoot))
		t.Cleanup(func() { _ = os.Chdir(oldWd) })

		// Create first worktree
		state1, err := proj.AddWorktree(ctx, "feature/dup-test", "")
		require.NoError(t, err)
		assert.NotEmpty(t, state1.Path)

		// Second AddWorktree with same branch — should error
		_, err = proj.AddWorktree(ctx, "feature/dup-test", "")
		require.Error(t, err, "duplicate worktree should be rejected")

		// Original worktree dir still exists
		_, err = os.Stat(state1.Path)
		require.NoError(t, err, "worktree directory should exist")

		// Remove the project (registry-only removal)
		err = mgr.Remove(ctx, resolvedRoot)
		require.NoError(t, err)

		// Project is gone from registry
		_, err = mgr.Get(ctx, resolvedRoot)
		assert.ErrorIs(t, err, project.ErrProjectNotFound)

		// But the worktree directory still exists on disk
		_, err = os.Stat(state1.Path)
		assert.NoError(t, err, "worktree directory should survive project removal")
	})

	t.Run("add worktree checks out existing branch", func(t *testing.T) {
		cfg, _ := configmocks.NewIsolatedTestConfig(t)
		root := os.Getenv(cfg.TestRepoDirEnvVar())
		resolvedRoot, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)

		inMemGit := gittest.NewInMemoryGitManager(t, resolvedRoot)
		factory := func(_ string) (*git.GitManager, error) {
			return inMemGit.GitManager, nil
		}

		mgr := project.NewProjectManager(cfg, factory)
		ctx := context.Background()

		proj, err := mgr.Register(ctx, "my-app", resolvedRoot)
		require.NoError(t, err)

		oldWd, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(resolvedRoot))
		t.Cleanup(func() { _ = os.Chdir(oldWd) })

		// Pre-create the branch at HEAD — simulates a branch that exists
		// but isn't checked out in any worktree.
		require.NoError(t, inMemGit.CreateBranch("feature/existing", ""))

		exists, err := inMemGit.BranchExists("feature/existing")
		require.NoError(t, err)
		require.True(t, exists, "branch should exist before AddWorktree")

		// AddWorktree should check out the existing branch, not fail
		state, err := proj.AddWorktree(ctx, "feature/existing", "")
		require.NoError(t, err)
		assert.Equal(t, "feature/existing", state.Branch)
		assert.NotEmpty(t, state.Path)
		assert.True(t, state.ExistsInGit)
		assert.True(t, state.ExistsInRegistry)

		// Worktree directory exists on disk
		_, err = os.Stat(state.Path)
		assert.NoError(t, err, "worktree directory should exist")

		// Registry recorded the worktree
		record, err := proj.Record()
		require.NoError(t, err)
		require.Contains(t, record.Worktrees, "feature/existing")
		assert.Equal(t, state.Path, record.Worktrees["feature/existing"].Path)
	})

	t.Run("add worktree rejects branch already in a worktree", func(t *testing.T) {
		cfg, _ := configmocks.NewIsolatedTestConfig(t)
		root := os.Getenv(cfg.TestRepoDirEnvVar())
		resolvedRoot, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)

		inMemGit := gittest.NewInMemoryGitManager(t, resolvedRoot)
		factory := func(_ string) (*git.GitManager, error) {
			return inMemGit.GitManager, nil
		}

		mgr := project.NewProjectManager(cfg, factory)
		ctx := context.Background()

		proj, err := mgr.Register(ctx, "my-app", resolvedRoot)
		require.NoError(t, err)

		oldWd, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(resolvedRoot))
		t.Cleanup(func() { _ = os.Chdir(oldWd) })

		// Create a worktree for a branch
		state, err := proj.AddWorktree(ctx, "feature/locked", "")
		require.NoError(t, err)
		assert.NotEmpty(t, state.Path)

		// Try to add another worktree for the same branch — registry rejects it
		_, err = proj.AddWorktree(ctx, "feature/locked", "")
		require.Error(t, err)
		assert.ErrorIs(t, err, project.ErrWorktreeExists,
			"should reject duplicate worktree for same branch")

		// Original worktree should be unaffected
		_, err = os.Stat(state.Path)
		assert.NoError(t, err, "original worktree directory should survive duplicate rejection")
	})

	t.Run("list worktrees reports unhealthy statuses", func(t *testing.T) {
		cfg, _ := configmocks.NewIsolatedTestConfig(t)
		root := os.Getenv(cfg.TestRepoDirEnvVar())
		resolvedRoot, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)

		inMemGit := gittest.NewInMemoryGitManager(t, resolvedRoot)
		factory := func(_ string) (*git.GitManager, error) {
			return inMemGit.GitManager, nil
		}

		mgr := project.NewProjectManager(cfg, factory)
		ctx := context.Background()

		proj, err := mgr.Register(ctx, "my-app", resolvedRoot)
		require.NoError(t, err)

		oldWd, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(resolvedRoot))
		t.Cleanup(func() { _ = os.Chdir(oldWd) })

		// --- Create healthy worktree that should survive prune ---
		_, err = proj.AddWorktree(ctx, "feature/healthy", "")
		require.NoError(t, err)

		states, err := proj.ListWorktrees(ctx)
		require.NoError(t, err)
		require.Len(t, states, 1)
		assert.Equal(t, "feature/healthy", states[0].Branch)
		assert.Equal(t, project.WorktreeHealthy, states[0].Status)

		// --- Create worktree 1, verify both show up healthy ---
		state1, err := proj.AddWorktree(ctx, "feature/dir-deleted", "")
		require.NoError(t, err)

		states, err = proj.ListWorktrees(ctx)
		require.NoError(t, err)
		require.Len(t, states, 2)
		for _, s := range states {
			assert.Equal(t, project.WorktreeHealthy, s.Status,
				"worktree %s should be healthy", s.Branch)
		}

		// --- Create worktree 2, verify all three show up healthy ---
		_, err = proj.AddWorktree(ctx, "feature/branch-deleted", "")
		require.NoError(t, err)

		states, err = proj.ListWorktrees(ctx)
		require.NoError(t, err)
		require.Len(t, states, 3)
		for _, s := range states {
			assert.Equal(t, project.WorktreeHealthy, s.Status,
				"worktree %s should be healthy before sabotage", s.Branch)
		}

		// --- Sabotage 1: delete worktree directory directly ---
		require.NoError(t, os.RemoveAll(state1.Path))

		// List after sabotage 1: dir-deleted unhealthy, others healthy
		states, err = proj.ListWorktrees(ctx)
		require.NoError(t, err)
		require.Len(t, states, 3)

		stateMap := map[string]project.WorktreeState{}
		for _, s := range states {
			stateMap[s.Branch] = s
		}

		assert.NotEqual(t, project.WorktreeHealthy, stateMap["feature/dir-deleted"].Status,
			"worktree with deleted directory should not be healthy")
		assert.Equal(t, project.WorktreeHealthy, stateMap["feature/branch-deleted"].Status,
			"worktree with intact branch should still be healthy")
		assert.Equal(t, project.WorktreeHealthy, stateMap["feature/healthy"].Status,
			"healthy worktree should still be healthy")

		// --- Sabotage 2: delete branch directly via git ---
		require.NoError(t, inMemGit.GitManager.DeleteBranch("feature/branch-deleted"))

		// List after sabotage 2: both sabotaged unhealthy, healthy still healthy
		states, err = proj.ListWorktrees(ctx)
		require.NoError(t, err)
		require.Len(t, states, 3)

		stateMap = map[string]project.WorktreeState{}
		for _, s := range states {
			stateMap[s.Branch] = s
		}

		assert.NotEqual(t, project.WorktreeHealthy, stateMap["feature/dir-deleted"].Status,
			"worktree with deleted directory should not be healthy")
		assert.NotEqual(t, project.WorktreeHealthy, stateMap["feature/branch-deleted"].Status,
			"worktree with deleted branch should not be healthy")
		assert.Equal(t, project.WorktreeHealthy, stateMap["feature/healthy"].Status,
			"healthy worktree should still be healthy")

		// --- Prune stale entries ---
		pruneResult, err := proj.PruneStaleWorktrees(ctx, false)
		require.NoError(t, err)
		t.Logf("pruneResult: Prunable=%v Removed=%v Failed=%v",
			pruneResult.Prunable, pruneResult.Removed, pruneResult.Failed)

		// --- After prune, only the healthy worktree should remain ---
		states, err = proj.ListWorktrees(ctx)
		require.NoError(t, err)
		require.Len(t, states, 1, "only healthy worktree should survive prune")
		assert.Equal(t, "feature/healthy", states[0].Branch)
		assert.Equal(t, project.WorktreeHealthy, states[0].Status)
	})

	t.Run("rejects duplicate root", func(t *testing.T) {
		mgr := projectmocks.NewTestProjectManager(t, nil)
		ctx := context.Background()
		root := t.TempDir()

		_, err := mgr.Register(ctx, "first", root)
		require.NoError(t, err)

		_, err = mgr.Register(ctx, "second", root)
		assert.ErrorIs(t, err, project.ErrProjectExists)
	})

	t.Run("persists across List calls", func(t *testing.T) {
		mgr := projectmocks.NewTestProjectManager(t, nil)
		ctx := context.Background()

		root := t.TempDir()
		_, err := mgr.Register(ctx, "persisted", root)
		require.NoError(t, err)

		entries, err := mgr.List(ctx)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "persisted", entries[0].Name)
		assert.Equal(t, root, entries[0].Root)
	})
}

func TestList(t *testing.T) {
	t.Run("empty registry", func(t *testing.T) {
		mgr := projectmocks.NewTestProjectManager(t, nil)
		ctx := context.Background()

		entries, err := mgr.List(ctx)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("multiple projects", func(t *testing.T) {
		mgr := projectmocks.NewTestProjectManager(t, nil)
		ctx := context.Background()

		rootA := t.TempDir()
		rootB := t.TempDir()

		_, err := mgr.Register(ctx, "alpha", rootA)
		require.NoError(t, err)
		_, err = mgr.Register(ctx, "beta", rootB)
		require.NoError(t, err)

		entries, err := mgr.List(ctx)
		require.NoError(t, err)
		assert.Len(t, entries, 2)
	})
}

func TestGet(t *testing.T) {
	t.Run("returns registered project", func(t *testing.T) {
		mgr := projectmocks.NewTestProjectManager(t, nil)
		ctx := context.Background()
		root := t.TempDir()

		_, err := mgr.Register(ctx, "my-app", root)
		require.NoError(t, err)

		proj, err := mgr.Get(ctx, root)
		require.NoError(t, err)
		assert.Equal(t, "my-app", proj.Name())
		assert.Equal(t, root, proj.RepoPath())
	})

	t.Run("error for unknown root", func(t *testing.T) {
		mgr := projectmocks.NewTestProjectManager(t, nil)
		ctx := context.Background()

		_, err := mgr.Get(ctx, "/nonexistent/path")
		assert.ErrorIs(t, err, project.ErrProjectNotFound)
	})
}

func TestRemove(t *testing.T) {
	t.Run("removes registered project", func(t *testing.T) {
		mgr := projectmocks.NewTestProjectManager(t, nil)
		ctx := context.Background()
		root := t.TempDir()

		_, err := mgr.Register(ctx, "doomed", root)
		require.NoError(t, err)

		err = mgr.Remove(ctx, root)
		require.NoError(t, err)

		entries, err := mgr.List(ctx)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("error for unknown root", func(t *testing.T) {
		mgr := projectmocks.NewTestProjectManager(t, nil)
		ctx := context.Background()

		err := mgr.Remove(ctx, "/nonexistent/path")
		assert.ErrorIs(t, err, project.ErrProjectNotFound)
	})

	t.Run("other projects survive removal", func(t *testing.T) {
		mgr := projectmocks.NewTestProjectManager(t, nil)
		ctx := context.Background()
		rootA := t.TempDir()
		rootB := t.TempDir()

		_, err := mgr.Register(ctx, "keep", rootA)
		require.NoError(t, err)
		_, err = mgr.Register(ctx, "remove", rootB)
		require.NoError(t, err)

		err = mgr.Remove(ctx, rootB)
		require.NoError(t, err)

		entries, err := mgr.List(ctx)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "keep", entries[0].Name)
	})
}

func TestUpdate(t *testing.T) {
	t.Run("updates project name", func(t *testing.T) {
		mgr := projectmocks.NewTestProjectManager(t, nil)
		ctx := context.Background()
		root := t.TempDir()

		_, err := mgr.Register(ctx, "old-name", root)
		require.NoError(t, err)

		updated, err := mgr.Update(ctx, config.ProjectEntry{
			Name: "new-name",
			Root: root,
		})
		require.NoError(t, err)
		assert.Equal(t, "new-name", updated.Name())

		got, err := mgr.Get(ctx, root)
		require.NoError(t, err)
		assert.Equal(t, "new-name", got.Name())
	})

	t.Run("error for unregistered project", func(t *testing.T) {
		mgr := projectmocks.NewTestProjectManager(t, nil)
		ctx := context.Background()

		_, err := mgr.Update(ctx, config.ProjectEntry{
			Name: "ghost",
			Root: "/nonexistent",
		})
		assert.ErrorIs(t, err, project.ErrProjectNotFound)
	})
}

func TestResolvePath(t *testing.T) {
	t.Run("resolves registered root", func(t *testing.T) {
		mgr := projectmocks.NewTestProjectManager(t, nil)
		ctx := context.Background()
		root := t.TempDir()

		_, err := mgr.Register(ctx, "my-app", root)
		require.NoError(t, err)

		proj, err := mgr.ResolvePath(ctx, root)
		require.NoError(t, err)
		assert.Equal(t, "my-app", proj.Name())
	})

	t.Run("error for unregistered path", func(t *testing.T) {
		mgr := projectmocks.NewTestProjectManager(t, nil)
		ctx := context.Background()

		_, err := mgr.ResolvePath(ctx, "/some/random/path")
		assert.ErrorIs(t, err, project.ErrProjectNotFound)
	})
}

func TestRecord(t *testing.T) {
	t.Run("returns record with empty worktrees", func(t *testing.T) {
		mgr := projectmocks.NewTestProjectManager(t, nil)
		ctx := context.Background()
		root := t.TempDir()

		proj, err := mgr.Register(ctx, "my-app", root)
		require.NoError(t, err)

		record, err := proj.Record()
		require.NoError(t, err)
		assert.Equal(t, "my-app", record.Name)
		assert.Equal(t, root, record.Root)
		assert.Empty(t, record.Worktrees)
	})
}
