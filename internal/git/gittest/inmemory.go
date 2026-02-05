// Package gittest provides test utilities for the git package.
package gittest

import (
	"testing"
	"time"

	"github.com/go-git/go-billy/v6/memfs"
	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/cache"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/storage/filesystem"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/stretchr/testify/require"
)

// InMemoryGitManager wraps *git.GitManager with test-only accessors.
// The underlying repository uses in-memory storage (memfs).
type InMemoryGitManager struct {
	*git.GitManager
	repo *gogit.Repository
}

// NewInMemoryGitManager creates a GitManager backed by in-memory storage.
// The repoRoot is a logical path (not a real filesystem path) used for
// path construction in tests.
//
// The repository is seeded with an initial commit so HEAD exists.
func NewInMemoryGitManager(t *testing.T, repoRoot string) *InMemoryGitManager {
	t.Helper()

	// Use memfs for both .git storage and worktree
	dotGitFS := memfs.New()
	worktreeFS := memfs.New()

	// Create storage using filesystem.NewStorage wrapping memfs
	// This gives filesystem semantics with in-memory speed
	storer := filesystem.NewStorage(dotGitFS, cache.NewObjectLRUDefault())

	// Initialize the repository with the in-memory worktree
	repo, err := gogit.Init(storer, gogit.WithWorkTree(worktreeFS))
	require.NoError(t, err, "failed to init in-memory repo")

	// Get the worktree to create a file and commit
	wt, err := repo.Worktree()
	require.NoError(t, err, "failed to get worktree")

	// Create a README file so we have something to commit
	readme, err := worktreeFS.Create("README.md")
	require.NoError(t, err, "failed to create README")
	_, err = readme.Write([]byte("# Test Repository\n"))
	require.NoError(t, err, "failed to write README")
	err = readme.Close()
	require.NoError(t, err, "failed to close README")

	// Stage and commit
	_, err = wt.Add("README.md")
	require.NoError(t, err, "failed to add README")

	_, err = wt.Commit("Initial commit", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test User",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	require.NoError(t, err, "failed to create initial commit")

	// Create GitManager using the new constructor
	mgr := git.NewGitManagerWithRepo(repo, repoRoot)

	return &InMemoryGitManager{
		GitManager: mgr,
		repo:       repo,
	}
}

// Repository returns the underlying go-git Repository for test assertions.
func (m *InMemoryGitManager) Repository() *gogit.Repository {
	return m.repo
}
