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

// Create from existing go-git Repository (for testing with in-memory repos)
mgr := git.NewGitManagerWithRepo(repo, "/logical/root/path")

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
// Branch names with slashes (e.g., "feature/foo") are supported.
path, err := mgr.SetupWorktree(provider, "feature-branch", "main")

// Remove worktree (git metadata + directory)
// Use the original branch name (with slashes if applicable).
err := mgr.RemoveWorktree(provider, "feature-branch")

// List all worktrees with info (takes entries from provider, not provider itself)
// Caller converts config.WorktreeDirInfo to git.WorktreeDirEntry
entries := []git.WorktreeDirEntry{{Name: "feature/foo", Slug: "feature-foo", Path: "/path"}}
infos, err := mgr.ListWorktrees(entries)
```

### WorktreeManager (Low-level)

Direct access to go-git worktree operations:

```go
wt, err := mgr.Worktrees()
if err != nil {
    // Handle error (storage doesn't support worktrees)
}

// Add worktree at commit (NOTE: this creates a branch named after the worktree!)
// For most use cases, prefer AddDetached, AddWithNewBranch, or AddWithExistingBranch.
err = wt.Add("/path/to/worktree", "name", commitHash)

// Add worktree with new branch (uses detached HEAD internally to avoid slugified branch)
// Creates worktree and a NEW branch pointing to baseCommit (or HEAD if zero)
err := wt.AddWithNewBranch("/path", "name", branchRef, baseCommit)

// Add worktree for an EXISTING branch (no new branch created)
// Use when the branch already exists and you want a worktree for it
err := wt.AddWithExistingBranch("/path", "name", branchRef)

// Add detached HEAD worktree (no branch created or checked out)
err := wt.AddDetached("/path", "name", commitHash)

// List worktree names
names, err := wt.List()

// Check if worktree exists in git metadata
exists, err := wt.Exists("name")

// Open worktree as Repository
repo, err := wt.Open("/path")

// Remove worktree metadata (not directory)
err := wt.Remove("name")
```

**Important**: `SetupWorktree` automatically chooses between `AddWithExistingBranch` and
`AddWithNewBranch` based on whether the branch already exists. This prevents the bug
where slashed branch names like "a/output-styling" would incorrectly create a slugified
branch "a-output-styling".

### WorktreeDirProvider Interface

Implemented by Config.Project() to manage worktree directories:

```go
type WorktreeDirProvider interface {
    GetOrCreateWorktreeDir(name string) (string, error)
    GetWorktreeDir(name string) (string, error)
    DeleteWorktreeDir(name string) error
}
```

### WorktreeDirEntry

Used by `ListWorktrees` to map between branch names, slugs, and paths:

```go
type WorktreeDirEntry struct {
    Name string // Original name (e.g., "feature/foo" with slashes)
    Slug string // Filesystem-safe slug (e.g., "feature-foo")
    Path string // Absolute filesystem path
}
```

Callers convert from `config.WorktreeDirInfo` to `git.WorktreeDirEntry` when calling `ListWorktrees`.

### WorktreeInfo

Information about a worktree:

```go
type WorktreeInfo struct {
    Name       string        // worktree name
    Slug       string        // registry slug (preserved from WorktreeDirEntry)
    Path       string        // filesystem path
    Head       plumbing.Hash // current commit
    Branch     string        // branch name (empty if detached)
    IsDetached bool          // true if detached HEAD
    Error      error         // error if info couldn't be read (other fields may be zero)
}
```

Note: If `Error` is non-nil, the worktree may be:
- Corrupted or inaccessible
- Orphaned (git metadata exists but directory entry is missing)
- Orphaned (directory entry exists but git metadata is missing)

Check this field before using other fields.

### Branch Operations

```go
// Check if a branch ref exists
exists, err := mgr.BranchExists("feature-branch")

// Delete a branch (ref + config), like `git branch -d`
// Safety: refuses to delete the current branch or branches with unmerged commits
err := mgr.DeleteBranch("feature-branch")
if errors.Is(err, git.ErrIsCurrentBranch) {
    // Cannot delete the currently checked-out branch
}
if errors.Is(err, git.ErrBranchNotMerged) {
    // Branch has commits not reachable from HEAD
}
if errors.Is(err, git.ErrBranchNotFound) {
    // Branch ref doesn't exist
}
```

## Utility Functions

```go
// Check if path is inside a linked worktree (not main repo)
isWorktree, err := git.IsInsideWorktree("/some/path")
```

## Errors

```go
// Sentinel errors
git.ErrNotRepository    // path is not inside a git repository
git.ErrBranchNotFound   // branch ref does not exist
git.ErrBranchNotMerged  // branch has commits not reachable from HEAD
git.ErrIsCurrentBranch  // cannot delete the currently checked-out branch

// Usage
if errors.Is(err, git.ErrNotRepository) {
    // Handle not in git repo
}
if errors.Is(err, git.ErrBranchNotMerged) {
    // Warn user, suggest `git branch -D`
}
if errors.Is(err, git.ErrIsCurrentBranch) {
    // Refuse to delete the branch HEAD points to
}
```

## Testing

Worktrees require real filesystem operations (`.git` files and `.git/worktrees/` directories), so worktree tests use `newTestRepoOnDisk(t)` with `t.TempDir()`. For non-worktree tests, use `gittest.NewInMemoryGitManager(t, "/logical/root")` which provides a seeded in-memory repository. The `gittest` subpackage is the public entry point for test consumers.

## Dependencies

`go-git/go-git/v6`, `go-git/go-billy/v6`, `go-git/go-git/v6/x/plumbing/worktree`
