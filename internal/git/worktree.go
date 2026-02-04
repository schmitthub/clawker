package git

import (
	"fmt"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/storage"
	xworktree "github.com/go-git/go-git/v6/x/plumbing/worktree"

	"github.com/go-git/go-billy/v6/osfs"
)

// WorktreeManager manages linked worktrees for a git repository.
// It wraps the experimental x/plumbing/worktree package from go-git.
type WorktreeManager struct {
	repo *gogit.Repository
	wt   *xworktree.Worktree
}

// newWorktreeManager creates a new WorktreeManager for the given repository.
// Returns an error if the storer doesn't support worktrees.
func newWorktreeManager(repo *gogit.Repository, storer storage.Storer) (*WorktreeManager, error) {
	wt, err := xworktree.New(storer)
	if err != nil {
		return nil, fmt.Errorf("creating worktree manager: %w", err)
	}
	return &WorktreeManager{repo: repo, wt: wt}, nil
}

// Add creates a linked worktree at the given path, checking out the
// specified commit. If commit is zero, HEAD is used.
//
// The name parameter is used to identify the worktree in git's metadata
// (stored in .git/worktrees/<name>/).
func (w *WorktreeManager) Add(path, name string, commit plumbing.Hash) error {
	wtFS := osfs.New(path)

	var opts []xworktree.Option
	if !commit.IsZero() {
		opts = append(opts, xworktree.WithCommit(commit))
	}

	if err := w.wt.Add(wtFS, name, opts...); err != nil {
		return fmt.Errorf("adding worktree %q at %s: %w", name, path, err)
	}
	return nil
}

// AddWithNewBranch creates a linked worktree at the given path and creates a new branch.
// It creates the worktree, creates the branch pointing to base (or HEAD if base is zero),
// and checks out the branch.
//
// Note: go-git's worktree API doesn't have WithBranch, so we create the worktree
// first, then create and checkout the branch using the worktree's Repository.
func (w *WorktreeManager) AddWithNewBranch(path, name string, branch plumbing.ReferenceName, base plumbing.Hash) error {
	// First, create the worktree at the commit
	if err := w.Add(path, name, base); err != nil {
		return err
	}

	// Open the worktree as a repository
	wtRepo, err := w.Open(path)
	if err != nil {
		return fmt.Errorf("opening newly created worktree: %w", err)
	}

	// Get the worktree to checkout the branch
	wt, err := wtRepo.Worktree()
	if err != nil {
		return fmt.Errorf("getting worktree: %w", err)
	}

	// Resolve base commit if not provided
	commitHash := base
	if commitHash.IsZero() {
		head, err := w.repo.Head()
		if err != nil {
			return fmt.Errorf("getting HEAD: %w", err)
		}
		commitHash = head.Hash()
	}

	// Create the branch reference
	ref := plumbing.NewHashReference(branch, commitHash)
	if err := wtRepo.Storer.SetReference(ref); err != nil {
		return fmt.Errorf("creating branch reference: %w", err)
	}

	// Checkout the branch
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: branch,
	}); err != nil {
		return fmt.Errorf("checking out branch %s: %w", branch.Short(), err)
	}

	return nil
}

// AddDetached creates a linked worktree with a detached HEAD.
// If commit is zero, HEAD is used.
func (w *WorktreeManager) AddDetached(path, name string, commit plumbing.Hash) error {
	wtFS := osfs.New(path)

	opts := []xworktree.Option{xworktree.WithDetachedHead()}
	if !commit.IsZero() {
		opts = append(opts, xworktree.WithCommit(commit))
	}

	if err := w.wt.Add(wtFS, name, opts...); err != nil {
		return fmt.Errorf("adding detached worktree %q at %s: %w", name, path, err)
	}
	return nil
}

// List returns the names of all linked worktrees.
func (w *WorktreeManager) List() ([]string, error) {
	names, err := w.wt.List()
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}
	return names, nil
}

// Open opens a linked worktree as a full *git.Repository.
// The path must point to an existing worktree directory.
func (w *WorktreeManager) Open(path string) (*gogit.Repository, error) {
	wtFS := osfs.New(path)
	repo, err := w.wt.Open(wtFS)
	if err != nil {
		return nil, fmt.Errorf("opening worktree at %s: %w", path, err)
	}
	return repo, nil
}

// Remove deletes the worktree metadata from .git/worktrees/.
// It does NOT delete the worktree directory on disk — that's the caller's responsibility.
func (w *WorktreeManager) Remove(name string) error {
	if err := w.wt.Remove(name); err != nil {
		return fmt.Errorf("removing worktree %q: %w", name, err)
	}
	return nil
}
