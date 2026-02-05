// Package git provides Git repository operations, including worktree management.
//
// This is a Tier 1 (Leaf) package in the clawker architecture:
//   - It imports ONLY stdlib and go-git packages
//   - It does NOT import any internal packages
//   - Configuration is passed as parameters, not via config package imports
//
// The package follows the Facade pattern with domain-specific sub-managers:
//   - GitManager is the top-level facade owning the repository
//   - WorktreeManager handles linked worktree operations
//
// Dependency Inversion: GitManager defines WorktreeDirProvider interface which
// Config.Project() implements. This allows high-level orchestration without
// importing the config package.
package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

// ErrNotRepository is returned when the path is not inside a git repository.
// Callers can use errors.Is to check for this condition.
var ErrNotRepository = errors.New("not a git repository")

// GitManager is the top-level facade for git operations.
// It owns the repository handle and provides access to domain-specific sub-managers.
type GitManager struct {
	repo     *gogit.Repository
	repoRoot string

	worktrees    *WorktreeManager
	worktreeErr  error     // cached error from worktree manager initialization
	worktreeOnce sync.Once // ensures single initialization of worktree manager
}

// NewGitManager opens the git repository containing the given path.
// It walks up the directory tree to find the repository root.
//
// Returns ErrNotRepository (wrapped) if path is not inside a git repository.
func NewGitManager(path string) (*GitManager, error) {
	// PlainOpenWithOptions with DetectDotGit walks up to find the repo
	repo, err := gogit.PlainOpenWithOptions(path, &gogit.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		if errors.Is(err, gogit.ErrRepositoryNotExists) {
			return nil, fmt.Errorf("%w: %s", ErrNotRepository, path)
		}
		return nil, fmt.Errorf("opening repository at %s: %w", path, err)
	}

	// Get the repository root from the worktree filesystem
	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("getting worktree: %w", err)
	}
	repoRoot := wt.Filesystem.Root()

	return &GitManager{
		repo:     repo,
		repoRoot: repoRoot,
	}, nil
}

// NewGitManagerWithRepo creates a GitManager from an existing go-git Repository.
// This is primarily used for testing with in-memory repositories.
// The repoRoot parameter should be the logical root directory (can be a fake path for testing).
func NewGitManagerWithRepo(repo *gogit.Repository, repoRoot string) *GitManager {
	return &GitManager{
		repo:     repo,
		repoRoot: repoRoot,
	}
}

// Repository returns the underlying go-git Repository.
// Use this for operations not covered by the sub-managers.
func (g *GitManager) Repository() *gogit.Repository {
	return g.repo
}

// RepoRoot returns the root directory of the git repository.
func (g *GitManager) RepoRoot() string {
	return g.repoRoot
}

// Worktrees returns the WorktreeManager for linked worktree operations.
// The manager is lazily initialized on first access.
// Returns an error if the repository's storage doesn't support worktrees.
func (g *GitManager) Worktrees() (*WorktreeManager, error) {
	g.worktreeOnce.Do(func() {
		g.worktrees, g.worktreeErr = newWorktreeManager(g.repo, g.repo.Storer)
	})
	return g.worktrees, g.worktreeErr
}

// === High-level orchestration methods ===
// These methods coordinate with WorktreeDirProvider to manage both
// git worktree metadata and filesystem directories.

// SetupWorktree creates or gets a worktree for the given branch.
// It orchestrates: directory creation (via provider) + git worktree add.
//
// Parameters:
//   - dirs: Provider for worktree directory management (typically Config.Project())
//   - branch: Branch name to check out (created if doesn't exist)
//   - base: Base ref to create branch from (empty string uses HEAD)
//
// Returns the worktree path ready for mounting.
func (g *GitManager) SetupWorktree(dirs WorktreeDirProvider, branch, base string) (string, error) {
	// 1. Get or create worktree directory in CLAWKER_HOME
	wtPath, err := dirs.GetOrCreateWorktreeDir(branch)
	if err != nil {
		return "", fmt.Errorf("creating worktree directory: %w", err)
	}

	// 2. Check if worktree already exists (directory has files)
	entries, err := os.ReadDir(wtPath)
	if err != nil {
		return "", fmt.Errorf("reading worktree directory: %w", err)
	}

	if len(entries) > 0 {
		// Worktree exists, verify it's valid and return
		wt, err := g.Worktrees()
		if err != nil {
			return "", fmt.Errorf("initializing worktree manager: %w", err)
		}
		if _, err := wt.Open(wtPath); err != nil {
			return "", fmt.Errorf("worktree directory exists but is invalid: %w", err)
		}
		return wtPath, nil
	}

	// 3. Resolve base commit
	var baseCommit plumbing.Hash
	if base != "" {
		hash, err := g.repo.ResolveRevision(plumbing.Revision(base))
		if err != nil {
			return "", fmt.Errorf("resolving base %q: %w", base, err)
		}
		baseCommit = *hash
	}

	// 4. Create git worktree with branch
	wt, err := g.Worktrees()
	if err != nil {
		return "", fmt.Errorf("initializing worktree manager: %w", err)
	}
	branchRef := plumbing.NewBranchReferenceName(branch)
	// Use directory basename as worktree name (already slugified by GetOrCreateWorktreeDir).
	// Branch names like "a/foo" have slashes that go-git rejects in worktree names,
	// but the path basename "a-foo" is safe. This matches native git behavior.
	wtName := filepath.Base(wtPath)
	if err := wt.AddWithNewBranch(wtPath, wtName, branchRef, baseCommit); err != nil {
		// Clean up the directory on failure - include cleanup error if it occurs
		if cleanupErr := os.RemoveAll(wtPath); cleanupErr != nil {
			return "", fmt.Errorf("creating git worktree: %w (cleanup also failed: %v)", err, cleanupErr)
		}
		return "", fmt.Errorf("creating git worktree: %w", err)
	}

	return wtPath, nil
}

// RemoveWorktree removes a worktree (both git metadata and directory).
//
// Parameters:
//   - dirs: Provider for worktree directory management
//   - branch: Branch/worktree name to remove
func (g *GitManager) RemoveWorktree(dirs WorktreeDirProvider, branch string) error {
	// 1. Get worktree directory path (also verifies it exists)
	wtPath, err := dirs.GetWorktreeDir(branch)
	if err != nil {
		return fmt.Errorf("looking up worktree: %w", err)
	}

	// 2. Remove git worktree metadata
	wt, err := g.Worktrees()
	if err != nil {
		return fmt.Errorf("initializing worktree manager: %w", err)
	}
	// Use directory basename as worktree name (matches SetupWorktree behavior).
	// The worktree was created with the slugified name, not the branch name.
	wtName := filepath.Base(wtPath)
	if err := wt.Remove(wtName); err != nil {
		return fmt.Errorf("removing git worktree: %w", err)
	}

	// 3. Delete worktree directory
	if err := dirs.DeleteWorktreeDir(branch); err != nil {
		return fmt.Errorf("deleting worktree directory: %w", err)
	}

	return nil
}

// ListWorktrees returns information about all linked worktrees.
// Worktrees that have errors reading their info will have the Error field set.
//
// Parameters:
//   - entries: Worktree directory entries from the provider (name, slug, path)
//
// The function matches entries to git worktree metadata by slug (which matches
// the go-git worktree name). Entries without corresponding git metadata are
// included with an error, as are entries that can't be opened.
func (g *GitManager) ListWorktrees(entries []WorktreeDirEntry) ([]WorktreeInfo, error) {
	wt, err := g.Worktrees()
	if err != nil {
		return nil, fmt.Errorf("initializing worktree manager: %w", err)
	}

	// Build a map of slug -> entry for quick lookup
	entryBySlug := make(map[string]WorktreeDirEntry, len(entries))
	for _, e := range entries {
		entryBySlug[e.Slug] = e
	}

	// Get worktree names from go-git (these are slugs)
	names, err := wt.List()
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}

	// Track which slugs we've seen from git
	seenSlugs := make(map[string]bool, len(names))

	var infos []WorktreeInfo
	for _, slug := range names {
		seenSlugs[slug] = true
		entry, ok := entryBySlug[slug]
		if !ok {
			// Orphaned git worktree - no matching directory entry
			infos = append(infos, WorktreeInfo{
				Name:  slug,
				Error: fmt.Errorf("worktree %q has git metadata but no directory entry (orphaned)", slug),
			})
			continue
		}

		info := WorktreeInfo{
			Name: entry.Name, // Use original name (with slashes), not slug
			Path: entry.Path,
		}

		// Try to get HEAD info - capture errors
		wtRepo, err := wt.Open(entry.Path)
		if err != nil {
			info.Error = fmt.Errorf("opening worktree: %w", err)
		} else {
			head, err := wtRepo.Head()
			if err != nil {
				info.Error = fmt.Errorf("getting HEAD: %w", err)
			} else {
				info.Head = head.Hash()
				info.Branch = head.Name().Short()
				info.IsDetached = head.Name() == plumbing.HEAD
			}
		}

		infos = append(infos, info)
	}

	// Second pass: find orphaned directories (entries without git metadata)
	for _, entry := range entries {
		if seenSlugs[entry.Slug] {
			continue
		}
		// Orphaned directory - has config entry but no git metadata
		infos = append(infos, WorktreeInfo{
			Name:  entry.Name,
			Path:  entry.Path,
			Error: fmt.Errorf("worktree %q has directory but no git metadata (orphaned)", entry.Name),
		})
	}

	return infos, nil
}

// GetCurrentBranch returns the current branch name of the repository.
// Returns empty string and no error for detached HEAD state.
func (g *GitManager) GetCurrentBranch() (string, error) {
	head, err := g.repo.Head()
	if err != nil {
		return "", fmt.Errorf("getting HEAD: %w", err)
	}

	if head.Name() == plumbing.HEAD {
		// Detached HEAD
		return "", nil
	}

	return head.Name().Short(), nil
}

// ResolveRef resolves a reference (branch, tag, commit) to a commit hash.
func (g *GitManager) ResolveRef(ref string) (plumbing.Hash, error) {
	hash, err := g.repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("resolving %q: %w", ref, err)
	}
	return *hash, nil
}

// BranchExists checks if a branch exists in the repository.
func (g *GitManager) BranchExists(branch string) (bool, error) {
	branchRef := plumbing.NewBranchReferenceName(branch)
	_, err := g.repo.Reference(branchRef, true)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("checking branch %q: %w", branch, err)
	}
	return true, nil
}

// IsInsideWorktree checks if the given path is inside a git worktree
// (not the main repository worktree).
func IsInsideWorktree(path string) (bool, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("getting absolute path: %w", err)
	}

	// Walk up looking for .git file (not directory)
	current := absPath
	for {
		gitPath := filepath.Join(current, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			// .git exists - if it's a file, we're in a worktree
			// (worktrees have a .git file pointing to main repo)
			return !info.IsDir(), nil
		}
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("checking %s: %w", gitPath, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			// Reached filesystem root without finding .git
			return false, nil
		}
		current = parent
	}
}
