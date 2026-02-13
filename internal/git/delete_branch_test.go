package git_test

import (
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/git/gittest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitManager_DeleteBranch(t *testing.T) {
	// Helper: create a branch pointing at HEAD (trivially merged).
	createMergedBranch := func(t *testing.T, repo *gogit.Repository, branch string) {
		t.Helper()
		head, err := repo.Head()
		require.NoError(t, err)
		branchRef := plumbing.NewBranchReferenceName(branch)
		err = repo.Storer.SetReference(plumbing.NewHashReference(branchRef, head.Hash()))
		require.NoError(t, err)
	}

	// Helper: create a branch with a commit not on HEAD.
	createUnmergedBranch := func(t *testing.T, repo *gogit.Repository, branch string) {
		t.Helper()

		head, err := repo.Head()
		require.NoError(t, err)

		branchRef := plumbing.NewBranchReferenceName(branch)
		err = repo.Storer.SetReference(plumbing.NewHashReference(branchRef, head.Hash()))
		require.NoError(t, err)

		// Checkout branch, add a file, commit
		wt, err := repo.Worktree()
		require.NoError(t, err)

		err = wt.Checkout(&gogit.CheckoutOptions{Branch: branchRef})
		require.NoError(t, err)

		// Use billy filesystem (in-memory) to create a file
		fs := wt.Filesystem
		f, err := fs.Create("branch-only.txt")
		require.NoError(t, err)
		_, err = f.Write([]byte("branch content\n"))
		require.NoError(t, err)
		require.NoError(t, f.Close())

		_, err = wt.Add("branch-only.txt")
		require.NoError(t, err)

		_, err = wt.Commit("branch commit", &gogit.CommitOptions{
			Author: &object.Signature{
				Name:  "test",
				Email: "test@test.com",
				When:  time.Now(),
			},
		})
		require.NoError(t, err)

		// Switch back to master — branch now has a commit not on master
		err = wt.Checkout(&gogit.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("master")})
		require.NoError(t, err)
	}

	t.Run("deletes merged branch", func(t *testing.T) {
		m := gittest.NewInMemoryGitManager(t, "/test/repo")
		createMergedBranch(t, m.Repository(), "feature-done")

		exists, err := m.BranchExists("feature-done")
		require.NoError(t, err)
		require.True(t, exists)

		err = m.DeleteBranch("feature-done")
		require.NoError(t, err)

		exists, err = m.BranchExists("feature-done")
		require.NoError(t, err)
		assert.False(t, exists, "branch ref should be deleted")
	})

	t.Run("refuses unmerged branch", func(t *testing.T) {
		m := gittest.NewInMemoryGitManager(t, "/test/repo")
		createUnmergedBranch(t, m.Repository(), "feature-wip")

		err := m.DeleteBranch("feature-wip")
		require.Error(t, err)
		assert.ErrorIs(t, err, git.ErrBranchNotMerged)

		// Verify ref still exists
		exists, err := m.BranchExists("feature-wip")
		require.NoError(t, err)
		assert.True(t, exists, "branch ref should survive failed delete")
	})

	t.Run("branch not found", func(t *testing.T) {
		m := gittest.NewInMemoryGitManager(t, "/test/repo")

		err := m.DeleteBranch("nonexistent")
		require.Error(t, err)
		assert.ErrorIs(t, err, git.ErrBranchNotFound)
	})

	t.Run("refuses to delete current branch", func(t *testing.T) {
		m := gittest.NewInMemoryGitManager(t, "/test/repo")

		// "master" is the default branch and current HEAD
		err := m.DeleteBranch("master")
		require.Error(t, err)
		assert.ErrorIs(t, err, git.ErrIsCurrentBranch)

		// Verify branch still exists
		exists, err := m.BranchExists("master")
		require.NoError(t, err)
		assert.True(t, exists, "current branch should not be deleted")
	})

	t.Run("branch with no config", func(t *testing.T) {
		m := gittest.NewInMemoryGitManager(t, "/test/repo")
		repo := m.Repository()

		// Create branch ref directly (no config/tracking info)
		head, err := repo.Head()
		require.NoError(t, err)
		branchRef := plumbing.NewBranchReferenceName("no-config-branch")
		err = repo.Storer.SetReference(plumbing.NewHashReference(branchRef, head.Hash()))
		require.NoError(t, err)

		err = m.DeleteBranch("no-config-branch")
		require.NoError(t, err)

		exists, err := m.BranchExists("no-config-branch")
		require.NoError(t, err)
		assert.False(t, exists)
	})
}
