# internal/git

Git repository operations including worktree management.

## Architecture

**Tier 1 (Leaf) Package** — imports ONLY stdlib + go-git, NO internal packages.

This package follows the Facade pattern:
- `GitManager` is the top-level facade owning the repository
- `WorktreeManager` handles linked worktree operations (low-level go-git primitives)

**Dependency Inversion:** The `WorktreeDirProvider` interface is defined here, not in config.
Config.Project() implements this interface, allowing GitManager to orchestrate worktree
setup without importing the config package.

## Key Types

### GitManager

Top-level facade for git operations.

```go
// Create from any path within a git repo (walks up to find root)
mgr, err := git.NewGitManager("/path/within/repo")

// Access sub-managers (returns error if storage doesn't support worktrees)
wt, err := mgr.Worktrees()

// Core accessors
mgr.RepoRoot()      // repository root directory
mgr.Repository()    // underlying go-git Repository
```

### High-level Orchestration (requires WorktreeDirProvider)

These methods coordinate git operations with directory management:

```go
// Setup worktree (create dir + git worktree add)
path, err := mgr.SetupWorktree(provider, "feature-branch", "main")

// Remove worktree (git metadata + directory)
err := mgr.RemoveWorktree(provider, "feature-branch")

// List all worktrees with info
infos, err := mgr.ListWorktrees(provider)
```

### WorktreeManager (Low-level)

Direct access to go-git worktree operations:

```go
wt, err := mgr.Worktrees()
if err != nil {
    // Handle error (storage doesn't support worktrees)
}

// Add worktree at commit
err = wt.Add("/path/to/worktree", "name", commitHash)

// Add worktree with new branch
err := wt.AddWithNewBranch("/path", "name", branchRef, baseCommit)

// Add detached HEAD worktree
err := wt.AddDetached("/path", "name", commitHash)

// List worktree names
names, err := wt.List()

// Open worktree as Repository
repo, err := wt.Open("/path")

// Remove worktree metadata (not directory)
err := wt.Remove("name")
```

### WorktreeDirProvider Interface

Implemented by Config.Project() to manage worktree directories:

```go
type WorktreeDirProvider interface {
    GetOrCreateWorktreeDir(name string) (string, error)
    GetWorktreeDir(name string) (string, error)
    DeleteWorktreeDir(name string) error
}
```

### WorktreeInfo

Information about a worktree:

```go
type WorktreeInfo struct {
    Name       string        // worktree name
    Path       string        // filesystem path
    Head       plumbing.Hash // current commit
    Branch     string        // branch name (empty if detached)
    IsDetached bool          // true if detached HEAD
    Error      error         // error if info couldn't be read (other fields may be zero)
}
```

Note: If `Error` is non-nil, the worktree may be corrupted or inaccessible.
Check this field before using other fields.

## Utility Functions

```go
// Check if path is inside a linked worktree (not main repo)
isWorktree, err := git.IsInsideWorktree("/some/path")
```

## Errors

```go
// Sentinel error for non-git directories
git.ErrNotRepository

// Usage
if errors.Is(err, git.ErrNotRepository) {
    // Handle not in git repo
}
```

## Testing

This package uses temp directories for tests because go-git's worktree API
requires real filesystem operations (worktrees create .git files and
.git/worktrees/ directories).

```go
func newTestRepoOnDisk(t *testing.T) (*gogit.Repository, string) {
    dir := t.TempDir()
    repo, _ := gogit.PlainInit(dir, false)
    // ... seed with initial commit
    return repo, dir
}
```

## Dependencies

- `github.com/go-git/go-git/v6` - Git operations
- `github.com/go-git/go-billy/v6` - Filesystem abstraction
- `github.com/go-git/go-git/v6/x/plumbing/worktree` - Experimental worktree API

## Rules

1. **Never import internal packages** — this is a leaf package
2. **Return errors, don't log** — callers handle logging
3. **Pass configuration as parameters** — no config package dependency
4. **WorktreeDirProvider enables DI** — high-level methods work with any implementation
