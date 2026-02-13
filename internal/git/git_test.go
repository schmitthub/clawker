package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	// Slugify the name like the real config does (replace slashes with hyphens)
	slug := strings.ReplaceAll(name, "/", "-")
	path := filepath.Join(f.baseDir, slug)
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

// entries returns all worktrees as WorktreeDirEntry slice for ListWorktrees tests.
func (f *fakeWorktreeDirProvider) entries() []WorktreeDirEntry {
	result := make([]WorktreeDirEntry, 0, len(f.worktrees))
	for name, path := range f.worktrees {
		// Slug is the path basename (slugified version of name)
		result = append(result, WorktreeDirEntry{
			Name: name,
			Slug: filepath.Base(path),
			Path: path,
		})
	}
	return result
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

func TestWorktreeManager_Exists(t *testing.T) {
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	t.Run("returns false for non-existent worktree", func(t *testing.T) {
		exists, err := wt.Exists("nonexistent")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("returns true for existing worktree", func(t *testing.T) {
		// Create a worktree
		wtPath := filepath.Join(t.TempDir(), "exists-test")
		err := os.MkdirAll(wtPath, 0755)
		require.NoError(t, err)

		err = wt.Add(wtPath, "exists-test", plumbing.ZeroHash)
		require.NoError(t, err)

		// Check it exists
		exists, err := wt.Exists("exists-test")
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("returns false after worktree removed", func(t *testing.T) {
		// Create and remove a worktree
		wtPath := filepath.Join(t.TempDir(), "removed-test")
		err := os.MkdirAll(wtPath, 0755)
		require.NoError(t, err)

		err = wt.Add(wtPath, "removed-test", plumbing.ZeroHash)
		require.NoError(t, err)

		// Remove it
		err = wt.Remove("removed-test")
		require.NoError(t, err)

		// Should no longer exist
		exists, err := wt.Exists("removed-test")
		require.NoError(t, err)
		assert.False(t, exists)
	})
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

	t.Run("handles branch names with slashes", func(t *testing.T) {
		// This was Bug #3: branch names like "a/output-styling" would fail
		// because go-git rejects slashes in worktree names.
		// The fix uses filepath.Base(wtPath) as the worktree name (already slugified).
		path, err := mgr.SetupWorktree(provider, "feature/test-slash", "")
		require.NoError(t, err)
		assert.NotEmpty(t, path)
		assert.DirExists(t, path)

		// Verify worktree is in git (name should be slugified)
		names, err := wt.List()
		require.NoError(t, err)
		// The worktree name should be the path basename (slugified by the provider)
		assert.Contains(t, names, filepath.Base(path))

		// Verify the branch has the original name with slashes
		wtRepo, err := wt.Open(path)
		require.NoError(t, err)

		head, err := wtRepo.Head()
		require.NoError(t, err)
		assert.Equal(t, "feature/test-slash", head.Name().Short())
	})

	t.Run("handles deeply nested branch names", func(t *testing.T) {
		// Test a more complex branch name with multiple slashes
		path, err := mgr.SetupWorktree(provider, "a/b/c/deep-branch", "")
		require.NoError(t, err)
		assert.NotEmpty(t, path)
		assert.DirExists(t, path)

		// Verify the branch name is preserved
		wtRepo, err := wt.Open(path)
		require.NoError(t, err)

		head, err := wtRepo.Head()
		require.NoError(t, err)
		assert.Equal(t, "a/b/c/deep-branch", head.Name().Short())
	})
}

func TestGitManager_SetupWorktree_DetectsPreExistingGitWorktree(t *testing.T) {
	// This test verifies that SetupWorktree handles the case where git
	// already has worktree metadata, but clawker's directory might be empty.
	// This can happen if someone manually creates a worktree or if previous
	// cleanup failed.

	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	// Create a worktree directly using the low-level API
	wtPath := filepath.Join(t.TempDir(), "pre-existing")
	err = os.MkdirAll(wtPath, 0755)
	require.NoError(t, err)

	err = wt.Add(wtPath, "pre-existing", plumbing.ZeroHash)
	require.NoError(t, err)

	// Create a provider that returns the same path
	provider := &fakeWorktreeDirProvider{
		baseDir:   filepath.Dir(wtPath),
		worktrees: map[string]string{"pre-existing": wtPath},
	}

	// SetupWorktree should detect the existing worktree and return it (idempotent)
	path, err := mgr.SetupWorktree(provider, "pre-existing", "")
	require.NoError(t, err)
	assert.Equal(t, wtPath, path)
}

func TestGitManager_SetupWorktree_RecoversOrphanedGitWorktree(t *testing.T) {
	// This test verifies that SetupWorktree handles orphaned git metadata.
	// An orphan is when git has worktree metadata but the directory is empty
	// (no .git file, which means Open() will fail).

	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	// Create a worktree directory
	wtPath := filepath.Join(t.TempDir(), "orphaned")
	err = os.MkdirAll(wtPath, 0755)
	require.NoError(t, err)

	// Create the worktree properly first
	err = wt.Add(wtPath, "orphaned", plumbing.ZeroHash)
	require.NoError(t, err)

	// Verify it was created
	exists, err := wt.Exists("orphaned")
	require.NoError(t, err)
	require.True(t, exists)

	// Now simulate an orphan: delete the directory contents but leave git metadata
	err = os.RemoveAll(wtPath)
	require.NoError(t, err)
	err = os.MkdirAll(wtPath, 0755)
	require.NoError(t, err)

	// Verify the worktree still exists in git metadata
	exists, err = wt.Exists("orphaned")
	require.NoError(t, err)
	require.True(t, exists, "git should still have the worktree metadata")

	// Create a provider that returns the empty directory
	provider := &fakeWorktreeDirProvider{
		baseDir:   filepath.Dir(wtPath),
		worktrees: map[string]string{"orphaned": wtPath},
	}

	// SetupWorktree should detect orphaned metadata, clean it up, and recreate
	path, err := mgr.SetupWorktree(provider, "orphaned", "")
	require.NoError(t, err)
	assert.Equal(t, wtPath, path)

	// Verify the worktree is now valid
	wtRepo, err := wt.Open(path)
	require.NoError(t, err)
	assert.NotNil(t, wtRepo)
}

func TestGitManager_RemoveWorktree(t *testing.T) {
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	provider := newFakeWorktreeDirProvider(t)

	t.Run("removes simple branch worktree", func(t *testing.T) {
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
	})

	t.Run("removes slashed branch worktree", func(t *testing.T) {
		// Setup a worktree with slashes in the branch name
		path, err := mgr.SetupWorktree(provider, "feature/to-remove", "")
		require.NoError(t, err)
		assert.DirExists(t, path)

		// Remove it using the original branch name (with slashes)
		err = mgr.RemoveWorktree(provider, "feature/to-remove")
		require.NoError(t, err)

		// Verify it's gone from git (the name in git is the slugified version)
		names, err := wt.List()
		require.NoError(t, err)
		slugifiedName := filepath.Base(path)
		assert.NotContains(t, names, slugifiedName)

		// Verify directory is removed
		assert.NoDirExists(t, path)
	})
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

	// List worktrees using entries from the provider
	infos, err := mgr.ListWorktrees(provider.entries())
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

func TestGitManager_ListWorktrees_SlashedBranchNames(t *testing.T) {
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	provider := newFakeWorktreeDirProvider(t)

	// Create worktrees with slashed branch names
	path1, err := mgr.SetupWorktree(provider, "feature/foo", "")
	require.NoError(t, err)

	path2, err := mgr.SetupWorktree(provider, "bugfix/bar/baz", "")
	require.NoError(t, err)

	// List worktrees using entries from the provider
	infos, err := mgr.ListWorktrees(provider.entries())
	require.NoError(t, err)

	// Build map for easier assertions
	infoByName := make(map[string]WorktreeInfo)
	for _, info := range infos {
		infoByName[info.Name] = info
	}

	// Verify slashed names are preserved in Name field
	info1, ok := infoByName["feature/foo"]
	require.True(t, ok, "expected worktree with name 'feature/foo'")
	assert.Equal(t, path1, info1.Path)
	assert.Equal(t, "feature/foo", info1.Branch)

	info2, ok := infoByName["bugfix/bar/baz"]
	require.True(t, ok, "expected worktree with name 'bugfix/bar/baz'")
	assert.Equal(t, path2, info2.Path)
	assert.Equal(t, "bugfix/bar/baz", info2.Branch)
}

func TestGitManager_ListWorktrees_OrphanedDirectory(t *testing.T) {
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	provider := newFakeWorktreeDirProvider(t)

	// Create a real worktree
	_, err = mgr.SetupWorktree(provider, "real-worktree", "")
	require.NoError(t, err)

	// Create an orphaned directory (exists in config but not in git)
	orphanDir := filepath.Join(provider.baseDir, "orphan-worktree")
	err = os.MkdirAll(orphanDir, 0755)
	require.NoError(t, err)

	// Add entries including the orphan (which doesn't exist in git)
	entries := provider.entries()
	entries = append(entries, WorktreeDirEntry{
		Name: "orphan-worktree",
		Slug: "orphan-worktree",
		Path: orphanDir,
	})

	infos, err := mgr.ListWorktrees(entries)
	require.NoError(t, err)

	// Build map for assertions
	infoByName := make(map[string]WorktreeInfo)
	for _, info := range infos {
		infoByName[info.Name] = info
	}

	// Real worktree should have no error
	realInfo, ok := infoByName["real-worktree"]
	require.True(t, ok, "expected real-worktree in results")
	assert.NoError(t, realInfo.Error)

	// Orphan should have error indicating missing git metadata
	orphanInfo, ok := infoByName["orphan-worktree"]
	require.True(t, ok, "expected orphan-worktree in results")
	require.Error(t, orphanInfo.Error)
	assert.Contains(t, orphanInfo.Error.Error(), "no git metadata")
	assert.Equal(t, orphanDir, orphanInfo.Path)
}

func TestGitManager_SetupWorktree_SucceedsWithExistingBranch(t *testing.T) {
	// After the fix, SetupWorktree should succeed with an existing branch
	// by using AddWithExistingBranch instead of AddWithNewBranch.
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	provider := newFakeWorktreeDirProvider(t)

	// Create a worktree for the existing master branch - should succeed now
	path, err := mgr.SetupWorktree(provider, "master", "")
	require.NoError(t, err)
	assert.NotEmpty(t, path)

	// Verify the worktree is on master
	wtRepo, err := wt.Open(path)
	require.NoError(t, err)

	head, err := wtRepo.Head()
	require.NoError(t, err)
	assert.Equal(t, "master", head.Name().Short())
}

func TestGitManager_SetupWorktree_CleanupOnInvalidBase(t *testing.T) {
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	provider := newFakeWorktreeDirProvider(t)

	// Try to create a worktree with an invalid base ref
	// This tests that the directory is cleaned up on failure
	_, err = mgr.SetupWorktree(provider, "new-branch", "nonexistent-ref")
	require.Error(t, err)

	// Verify the error is about resolving the base
	assert.Contains(t, err.Error(), "resolving base")
}

func TestGitManager_Worktrees_ConcurrentAccess(t *testing.T) {
	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	// Test that Worktrees() is safe to call concurrently
	const numGoroutines = 10
	errChan := make(chan error, numGoroutines)
	managerChan := make(chan *WorktreeManager, numGoroutines)

	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wt, err := mgr.Worktrees()
			if err != nil {
				errChan <- err
			} else {
				managerChan <- wt
			}
		}()
	}

	wg.Wait()
	close(errChan)
	close(managerChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("concurrent Worktrees() call failed: %v", err)
	}

	// All returned managers should be the same instance
	var managers []*WorktreeManager
	for wt := range managerChan {
		managers = append(managers, wt)
	}

	if len(managers) == 0 {
		t.Fatal("no successful calls to Worktrees()")
	}

	expectedManager := managers[0]
	for i, mgr := range managers {
		if mgr != expectedManager {
			t.Errorf("concurrent call %d returned different manager instance", i)
		}
	}
}

// === Bug fix tests for worktree slugified branch issue ===

func TestWorktreeManager_Add_CreatesBranchWithWorktreeName(t *testing.T) {
	// This test documents the current behavior of Add(): it creates a branch
	// named after the worktree name (the slugified version), which is the
	// root cause of the bug where we get both "a/output-styling" and
	// "a-output-styling" branches.
	//
	// When Add() is called with name="feature-foo", go-git's xworktree creates
	// a branch called "feature-foo" automatically.

	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	wtPath := filepath.Join(t.TempDir(), "slugified-name")
	err = os.MkdirAll(wtPath, 0755)
	require.NoError(t, err)

	// Add worktree with a specific name
	err = wt.Add(wtPath, "slugified-name", plumbing.ZeroHash)
	require.NoError(t, err)

	// This documents the bug: Add() creates a branch with the worktree name
	exists, err := mgr.BranchExists("slugified-name")
	require.NoError(t, err)
	assert.True(t, exists, "Add() creates a branch named after the worktree name (this is the bug)")
}

func TestWorktreeManager_AddDetached_DoesNotCreateBranch(t *testing.T) {
	// This test verifies that AddDetached() does NOT create any branch.
	// This is the key behavior we need to use to fix the bug.

	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	wtPath := filepath.Join(t.TempDir(), "detached-test")
	err = os.MkdirAll(wtPath, 0755)
	require.NoError(t, err)

	// Get list of branches before
	branchesBefore, err := listAllBranches(mgr)
	require.NoError(t, err)

	// Add detached worktree
	err = wt.AddDetached(wtPath, "detached-test", plumbing.ZeroHash)
	require.NoError(t, err)

	// Get list of branches after
	branchesAfter, err := listAllBranches(mgr)
	require.NoError(t, err)

	// No new branch should have been created
	assert.Equal(t, len(branchesBefore), len(branchesAfter),
		"AddDetached() should not create any new branches")

	// Specifically, no branch named "detached-test" should exist
	exists, err := mgr.BranchExists("detached-test")
	require.NoError(t, err)
	assert.False(t, exists, "AddDetached() should not create a branch with the worktree name")
}

func TestGitManager_SetupWorktree_ExistingBranchNoSlugifiedBranchCreated(t *testing.T) {
	// This is the main bug reproduction test:
	// Given: A repo with existing branch "a/output-styling"
	// When: SetupWorktree("a/output-styling", "") is called
	// Then: No "a-output-styling" branch should be created
	//       Worktree should checkout "a/output-styling"

	repo, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	// Create an existing branch "a/output-styling"
	head, err := repo.Head()
	require.NoError(t, err)

	branchRef := plumbing.NewBranchReferenceName("a/output-styling")
	ref := plumbing.NewHashReference(branchRef, head.Hash())
	err = repo.Storer.SetReference(ref)
	require.NoError(t, err)

	// Verify branch exists
	exists, err := mgr.BranchExists("a/output-styling")
	require.NoError(t, err)
	require.True(t, exists, "test setup: branch should exist")

	provider := newFakeWorktreeDirProvider(t)

	// Setup worktree for the existing branch
	path, err := mgr.SetupWorktree(provider, "a/output-styling", "")
	require.NoError(t, err)
	assert.NotEmpty(t, path)

	// CRITICAL ASSERTION: No slugified branch should have been created
	slugifiedExists, err := mgr.BranchExists("a-output-styling")
	require.NoError(t, err)
	assert.False(t, slugifiedExists,
		"SetupWorktree for existing branch should NOT create a slugified branch 'a-output-styling'")

	// Verify the worktree is on the correct branch
	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	wtRepo, err := wt.Open(path)
	require.NoError(t, err)

	wtHead, err := wtRepo.Head()
	require.NoError(t, err)
	assert.Equal(t, "a/output-styling", wtHead.Name().Short(),
		"worktree should be on the original branch with slashes")
}

func TestGitManager_SetupWorktree_CleansUpGitMetadataOnFailure(t *testing.T) {
	// This test verifies that when worktree creation fails partway through,
	// git worktree metadata is also cleaned up (not just the directory).

	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	// Create a worktree first
	provider := newFakeWorktreeDirProvider(t)
	path, err := mgr.SetupWorktree(provider, "test-branch", "")
	require.NoError(t, err)

	// Get the worktree name (slugified)
	wtName := filepath.Base(path)

	// Verify worktree exists
	exists, err := wt.Exists(wtName)
	require.NoError(t, err)
	require.True(t, exists, "worktree should exist after setup")

	// Now try to create a worktree with the same name in the same directory
	// This should fail because the worktree already exists
	// First, manually delete the directory contents but keep the provider's entry
	err = os.RemoveAll(path)
	require.NoError(t, err)
	err = os.MkdirAll(path, 0755)
	require.NoError(t, err)

	// Try to setup again - this will find the orphaned metadata and clean it up
	path2, err := mgr.SetupWorktree(provider, "test-branch", "")
	require.NoError(t, err)
	assert.Equal(t, path, path2)

	// Verify worktree is valid
	_, err = wt.Open(path)
	require.NoError(t, err)
}

func TestGitManager_SetupWorktree_ExistingBranchWorksCorrectly(t *testing.T) {
	// This test verifies that SetupWorktree correctly uses AddWithExistingBranch
	// for branches that already exist, instead of failing.

	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	provider := newFakeWorktreeDirProvider(t)

	// Setup worktree for master (which already exists)
	path, err := mgr.SetupWorktree(provider, "master", "")
	require.NoError(t, err)
	assert.NotEmpty(t, path)

	// Verify worktree was created
	wtName := filepath.Base(path)
	exists, err := wt.Exists(wtName)
	require.NoError(t, err)
	assert.True(t, exists)

	// Verify the worktree is on master
	wtRepo, err := wt.Open(path)
	require.NoError(t, err)

	head, err := wtRepo.Head()
	require.NoError(t, err)
	assert.Equal(t, "master", head.Name().Short())
}

func TestWorktreeManager_AddWithNewBranch_NoSlugifiedBranch(t *testing.T) {
	// This test verifies that AddWithNewBranch creates only the specified
	// branch, NOT a branch named after the worktree name.
	//
	// Given: worktree name "feature-foo" (slugified)
	//        branch ref "refs/heads/feature/foo" (slashed)
	// When: AddWithNewBranch is called
	// Then: Only branch "feature/foo" exists, NOT "feature-foo"

	_, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	wtPath := filepath.Join(t.TempDir(), "feature-foo")
	err = os.MkdirAll(wtPath, 0755)
	require.NoError(t, err)

	// Use slugified worktree name but slashed branch name
	branchRef := plumbing.NewBranchReferenceName("feature/foo")
	err = wt.AddWithNewBranch(wtPath, "feature-foo", branchRef, plumbing.ZeroHash)
	require.NoError(t, err)

	// The intended branch should exist
	exists, err := mgr.BranchExists("feature/foo")
	require.NoError(t, err)
	assert.True(t, exists, "AddWithNewBranch should create the specified branch")

	// CRITICAL: The slugified branch should NOT exist
	slugifiedExists, err := mgr.BranchExists("feature-foo")
	require.NoError(t, err)
	assert.False(t, slugifiedExists,
		"AddWithNewBranch should NOT create a branch named after the worktree name")
}

func TestWorktreeManager_AddWithExistingBranch(t *testing.T) {
	// Test the new AddWithExistingBranch method that checks out
	// an existing branch without trying to create a new one.

	repo, repoDir := newTestRepoOnDisk(t)
	mgr, err := NewGitManager(repoDir)
	require.NoError(t, err)

	wt, err := mgr.Worktrees()
	require.NoError(t, err)

	// Create an existing branch "existing/branch"
	head, err := repo.Head()
	require.NoError(t, err)

	branchRef := plumbing.NewBranchReferenceName("existing/branch")
	ref := plumbing.NewHashReference(branchRef, head.Hash())
	err = repo.Storer.SetReference(ref)
	require.NoError(t, err)

	wtPath := filepath.Join(t.TempDir(), "existing-branch")
	err = os.MkdirAll(wtPath, 0755)
	require.NoError(t, err)

	// Use the new method
	err = wt.AddWithExistingBranch(wtPath, "existing-branch", branchRef)
	require.NoError(t, err)

	// Verify worktree was created
	names, err := wt.List()
	require.NoError(t, err)
	assert.Contains(t, names, "existing-branch")

	// Verify it's on the correct branch
	wtRepo, err := wt.Open(wtPath)
	require.NoError(t, err)

	wtHead, err := wtRepo.Head()
	require.NoError(t, err)
	assert.Equal(t, "existing/branch", wtHead.Name().Short())

	// CRITICAL: No slugified branch should have been created
	slugifiedExists, err := mgr.BranchExists("existing-branch")
	require.NoError(t, err)
	assert.False(t, slugifiedExists,
		"AddWithExistingBranch should NOT create a slugified branch")
}

// listAllBranches returns all branch names in the repository.
func listAllBranches(mgr *GitManager) ([]string, error) {
	refs, err := mgr.Repository().References()
	if err != nil {
		return nil, err
	}

	var branches []string
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Name().IsBranch() {
			branches = append(branches, ref.Name().Short())
		}
		return nil
	})
	return branches, err
}

