package git

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRepoOnDisk creates a real git repository in a temp directory.
// This is needed because go-git's worktree API requires filesystem operations.
func newTestRepoOnDisk(t *testing.T) (*gogit.Repository, string) {
	t.Helper()
	dir := t.TempDir()

	repo, err := gogit.PlainInit(dir, false)
	require.NoError(t, err, "init test repo")

	// Seed with initial commit so HEAD exists
	wt, err := repo.Worktree()
	require.NoError(t, err)

	readme := filepath.Join(dir, "README.md")
	err = os.WriteFile(readme, []byte("# Test Repo\n"), 0644)
	require.NoError(t, err)

	_, err = wt.Add("README.md")
	require.NoError(t, err)

	_, err = wt.Commit("initial commit", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "test",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	require.NoError(t, err)

	return repo, dir
}

// fakeWorktreeDirProvider implements WorktreeDirProvider for testing.
type fakeWorktreeDirProvider struct {
	baseDir   string
	worktrees map[string]string // name -> path
}

func newFakeWorktreeDirProvider(t *testing.T) *fakeWorktreeDirProvider {
	return &fakeWorktreeDirProvider{
		baseDir:   t.TempDir(),
		worktrees: make(map[string]string),
	}
}

func (f *fakeWorktreeDirProvider) GetOrCreateWorktreeDir(name string) (string, error) {
	if path, ok := f.worktrees[name]; ok {
		return path, nil
	}
	path := filepath.Join(f.baseDir, name)
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", err
	}
	f.worktrees[name] = path
	return path, nil
}

func (f *fakeWorktreeDirProvider) GetWorktreeDir(name string) (string, error) {
	if path, ok := f.worktrees[name]; ok {
		return path, nil
	}
	return "", errors.New("worktree not found: " + name)
}

func (f *fakeWorktreeDirProvider) DeleteWorktreeDir(name string) error {
	path, ok := f.worktrees[name]
	if !ok {
		return errors.New("worktree not found: " + name)
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	delete(f.worktrees, name)
	return nil
}

func TestNewGitManager(t *testing.T) {
	t.Run("opens repo from root", func(t *testing.T) {
		_, repoDir := newTestRepoOnDisk(t)

		mgr, err := NewGitManager(repoDir)
		require.NoError(t, err)
		assert.Equal(t, repoDir, mgr.RepoRoot())
		assert.NotNil(t, mgr.Repository())
	})

	t.Run("opens repo from subdirectory", func(t *testing.T) {
		_, repoDir := newTestRepoOnDisk(t)

		// Create a subdirectory
		subdir := filepath.Join(repoDir, "src", "pkg")
		err := os.MkdirAll(subdir, 0755)
		require.NoError(t, err)

		mgr, err := NewGitManager(subdir)
		require.NoError(t, err)
		assert.Equal(t, repoDir, mgr.RepoRoot())
	})

	t.Run("returns ErrNotRepository for non-git directory", func(t *testing.T) {
		notGitDir := t.TempDir()

		_, err := NewGitManager(notGitDir)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrNotRepository), "expected ErrNotRepository, got: %v", err)
	})

	t.Run("returns error for non-existent path", func(t *testing.T) {
		_, err := NewGitManager("/nonexistent/path/that/does/not/exist")
		require.Error(t, err)
	})
}

func TestGitManager_GetCurrentBranch(t *testing.T) {
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	// Default branch after init should be master or main
	branch, err := mgr.GetCurrentBranch()
	require.NoError(t, err)
	// go-git defaults to "master"
	assert.Contains(t, []string{"master", "main"}, branch)
}

func TestGitManager_BranchExists(t *testing.T) {
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	t.Run("existing branch returns true", func(t *testing.T) {
		exists, err := mgr.BranchExists("master")
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("non-existing branch returns false", func(t *testing.T) {
		exists, err := mgr.BranchExists("nonexistent-branch")
		require.NoError(t, err)
		assert.False(t, exists)
	})
}

func TestGitManager_ResolveRef(t *testing.T) {
	repo, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	head, err := repo.Head()
	require.NoError(t, err)

	t.Run("resolves HEAD", func(t *testing.T) {
		hash, err := mgr.ResolveRef("HEAD")
		require.NoError(t, err)
		assert.Equal(t, head.Hash(), hash)
	})

	t.Run("resolves branch name", func(t *testing.T) {
		hash, err := mgr.ResolveRef("master")
		require.NoError(t, err)
		assert.Equal(t, head.Hash(), hash)
	})

	t.Run("returns error for invalid ref", func(t *testing.T) {
		_, err := mgr.ResolveRef("nonexistent-ref")
		require.Error(t, err)
	})
}

func TestIsInsideWorktree(t *testing.T) {
	t.Run("main repo returns false", func(t *testing.T) {
		_, repoDir := newTestRepoOnDisk(t)

		isWT, err := IsInsideWorktree(repoDir)
		require.NoError(t, err)
		assert.False(t, isWT, "main repo should not be detected as worktree")
	})

	t.Run("non-git directory returns false", func(t *testing.T) {
		dir := t.TempDir()

		isWT, err := IsInsideWorktree(dir)
		require.NoError(t, err)
		assert.False(t, isWT)
	})
}

func TestWorktreeManager_Add(t *testing.T) {
	repo, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	head, err := repo.Head()
	require.NoError(t, err)

	t.Run("adds worktree at HEAD", func(t *testing.T) {
		wtPath := filepath.Join(t.TempDir(), "worktree1")
		err := os.MkdirAll(wtPath, 0755)
		require.NoError(t, err)

		err = wt.Add(wtPath, "worktree1", plumbing.ZeroHash)
		require.NoError(t, err)

		// Verify worktree was created
		names, err := wt.List()
		require.NoError(t, err)
		assert.Contains(t, names, "worktree1")
	})

	t.Run("adds worktree at specific commit", func(t *testing.T) {
		wtPath := filepath.Join(t.TempDir(), "worktree2")
		err := os.MkdirAll(wtPath, 0755)
		require.NoError(t, err)

		err = wt.Add(wtPath, "worktree2", head.Hash())
		require.NoError(t, err)

		names, err := wt.List()
		require.NoError(t, err)
		assert.Contains(t, names, "worktree2")
	})
}

func TestWorktreeManager_AddDetached(t *testing.T) {
	repo, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	head, err := repo.Head()
	require.NoError(t, err)

	wtPath := filepath.Join(t.TempDir(), "detached")
	err = os.MkdirAll(wtPath, 0755)
	require.NoError(t, err)

	err = wt.AddDetached(wtPath, "detached", head.Hash())
	require.NoError(t, err)

	names, err := wt.List()
	require.NoError(t, err)
	assert.Contains(t, names, "detached")
}

func TestWorktreeManager_Open(t *testing.T) {
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	wtPath := filepath.Join(t.TempDir(), "opentest")
	err = os.MkdirAll(wtPath, 0755)
	require.NoError(t, err)

	err = wt.Add(wtPath, "opentest", plumbing.ZeroHash)
	require.NoError(t, err)

	// Open the worktree
	wtRepo, err := wt.Open(wtPath)
	require.NoError(t, err)
	assert.NotNil(t, wtRepo)

	// Should be able to get HEAD from the worktree repo
	_, err = wtRepo.Head()
	require.NoError(t, err)
}

func TestWorktreeManager_Remove(t *testing.T) {
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	wtPath := filepath.Join(t.TempDir(), "toremove")
	err = os.MkdirAll(wtPath, 0755)
	require.NoError(t, err)

	err = wt.Add(wtPath, "toremove", plumbing.ZeroHash)
	require.NoError(t, err)

	// Verify it exists
	names, err := wt.List()
	require.NoError(t, err)
	assert.Contains(t, names, "toremove")

	// Remove it
	err = wt.Remove("toremove")
	require.NoError(t, err)

	// Verify it's gone
	names, err = wt.List()
	require.NoError(t, err)
	assert.NotContains(t, names, "toremove")
}

func TestWorktreeManager_AddWithNewBranch(t *testing.T) {
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	wtPath := filepath.Join(t.TempDir(), "feature-branch")
	err = os.MkdirAll(wtPath, 0755)
	require.NoError(t, err)

	branchRef := plumbing.NewBranchReferenceName("feature-test")
	err = wt.AddWithNewBranch(wtPath, "feature-test", branchRef, plumbing.ZeroHash)
	require.NoError(t, err)

	// Verify worktree was created
	names, err := wt.List()
	require.NoError(t, err)
	assert.Contains(t, names, "feature-test")

	// Verify branch was created
	exists, err := mgr.BranchExists("feature-test")
	require.NoError(t, err)
	assert.True(t, exists, "branch should exist after AddWithNewBranch")

	// Open worktree and verify it's on the right branch
	wtRepo, err := wt.Open(wtPath)
	require.NoError(t, err)

	head, err := wtRepo.Head()
	require.NoError(t, err)
	assert.Equal(t, "feature-test", head.Name().Short())
}

func TestGitManager_SetupWorktree(t *testing.T) {
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	provider := newFakeWorktreeDirProvider(t)

	t.Run("creates new worktree", func(t *testing.T) {
		path, err := mgr.SetupWorktree(provider, "setup-test", "")
		require.NoError(t, err)
		assert.NotEmpty(t, path)
		assert.DirExists(t, path)

		// Verify worktree is in git
		names, err := wt.List()
		require.NoError(t, err)
		assert.Contains(t, names, "setup-test")
	})

	t.Run("returns existing worktree", func(t *testing.T) {
		// Setup called twice with same branch should return same path
		path1, err := mgr.SetupWorktree(provider, "reuse-test", "")
		require.NoError(t, err)

		path2, err := mgr.SetupWorktree(provider, "reuse-test", "")
		require.NoError(t, err)

		assert.Equal(t, path1, path2)
	})

	t.Run("creates from specific base", func(t *testing.T) {
		// Get current HEAD hash
		headHash, err := mgr.ResolveRef("HEAD")
		require.NoError(t, err)

		path, err := mgr.SetupWorktree(provider, "from-head", "HEAD")
		require.NoError(t, err)
		assert.NotEmpty(t, path)

		// Open and verify HEAD matches
		wtRepo, err := wt.Open(path)
		require.NoError(t, err)

		wtHead, err := wtRepo.Head()
		require.NoError(t, err)
		assert.Equal(t, headHash, wtHead.Hash())
	})
}

func TestGitManager_RemoveWorktree(t *testing.T) {
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	provider := newFakeWorktreeDirProvider(t)

	// Setup a worktree first
	path, err := mgr.SetupWorktree(provider, "to-remove", "")
	require.NoError(t, err)
	assert.DirExists(t, path)

	// Remove it
	err = mgr.RemoveWorktree(provider, "to-remove")
	require.NoError(t, err)

	// Verify it's gone from git
	names, err := wt.List()
	require.NoError(t, err)
	assert.NotContains(t, names, "to-remove")

	// Verify directory is removed
	assert.NoDirExists(t, path)
}

func TestGitManager_ListWorktrees(t *testing.T) {
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	provider := newFakeWorktreeDirProvider(t)

	// Create a couple worktrees
	_, err = mgr.SetupWorktree(provider, "list-test-1", "")
	require.NoError(t, err)

	_, err = mgr.SetupWorktree(provider, "list-test-2", "")
	require.NoError(t, err)

	infos, err := mgr.ListWorktrees(provider)
	require.NoError(t, err)

	// Should have at least our two worktrees
	assert.GreaterOrEqual(t, len(infos), 2)

	// Check that our worktrees are in the list
	names := make([]string, len(infos))
	for i, info := range infos {
		names[i] = info.Name
		assert.NotEmpty(t, info.Path)
	}
	assert.Contains(t, names, "list-test-1")
	assert.Contains(t, names, "list-test-2")
}
