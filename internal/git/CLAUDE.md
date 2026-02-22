# internal/git

Leaf git operations package for repository discovery, branch safety checks, and linked worktree lifecycle.

## Recent Changes (Current State)

- Clarified package boundary language: callers own directory/layout concerns; git package stays pure stdlib + go-git.
- Worktree orchestration now explicitly relies on caller-provided `WorktreeDirProvider` and caller-provided `WorktreeDirEntry` metadata.
- Worktree branch creation path is hardened:
  - existing branch => `AddWithExistingBranch`
  - missing branch => `AddWithNewBranch`
- Slashed branch names are intentionally supported without creating incorrect slug-derived git branches.
- `ListWorktrees` behavior clearly includes orphan detection in both directions:
  - git metadata without caller entry
  - caller entry without git metadata

## Architecture

This is a **Tier 1 leaf package**:

- Imports only stdlib + go-git ecosystem packages.
- Does not import any `internal/*` package.
- Uses dependency inversion for filesystem layout via `WorktreeDirProvider`.

Facade shape:

- `GitManager`: top-level repository facade.
- `WorktreeManager`: low-level linked worktree operations over go-git x/worktree.

## Exported API

### Constructors

```go
// Discover repo from any path inside a repository.
mgr, err := git.NewGitManager(path)

// Testing/integration constructor when repo already exists.
mgr := git.NewGitManagerWithRepo(repo, repoRoot)
```

### Core Accessors

```go
repo := mgr.Repository()
root := mgr.RepoRoot()
wt, err := mgr.Worktrees()
```

### High-Level Orchestration (caller-integrated)

```go
path, err := mgr.SetupWorktree(provider, branch, base)
err = mgr.RemoveWorktree(provider, branch)
infos, err := mgr.ListWorktrees(entries)
```

Behavior details:

- `SetupWorktree` validates/reuses existing directories when possible.
- `SetupWorktree` removes orphaned git metadata for a target worktree before fresh creation.
- `RemoveWorktree` removes both git metadata and caller-managed directory.
- `ListWorktrees` returns `WorktreeInfo` for all known worktrees, including recoverable error states.

### Branch/Ref Operations

```go
branch, err := mgr.GetCurrentBranch()   // empty string when detached HEAD
hash, err := mgr.ResolveRef(ref)
exists, err := mgr.BranchExists(branch)
err = mgr.DeleteBranch(branch)
```

`DeleteBranch` is equivalent to safe `git branch -d` semantics:

- refuses current branch (`ErrIsCurrentBranch`)
- refuses unmerged branch (`ErrBranchNotMerged`)
- returns `ErrBranchNotFound` when missing

### Utility

```go
isLinkedWorktree, err := git.IsInsideWorktree(path)
```

## Worktree Types

```go
type WorktreeDirProvider interface {
    GetOrCreateWorktreeDir(name string) (string, error)
    GetWorktreeDir(name string) (string, error)
    DeleteWorktreeDir(name string) error
}

type WorktreeDirEntry struct {
    Name string
    Slug string
    Path string
}

type WorktreeInfo struct {
    Name       string
    Slug       string
    Path       string
    Head       plumbing.Hash
    Branch     string
    IsDetached bool
    Error      error
}
```

Notes:

- `Slug` is caller-provided metadata preserved through the pipeline.
- `Name` is usually the canonical branch identity (can include slashes).
- Non-nil `Error` indicates a partial info record; consumers should degrade gracefully.

## WorktreeManager (Low-level)

`WorktreeManager` is intentionally low-level and go-git-centric:

- `Add`
- `AddDetached`
- `AddWithNewBranch`
- `AddWithExistingBranch`
- `List`
- `Exists`
- `Open`
- `Remove`

These are composed by `GitManager.SetupWorktree`/`RemoveWorktree` for domain workflows.

## Sentinel Errors

- `ErrNotRepository`
- `ErrBranchNotFound`
- `ErrBranchNotMerged`
- `ErrIsCurrentBranch`

Prefer `errors.Is` checks at command/service boundaries.

## Testing Guidance

- For real linked worktree behavior, use filesystem-backed temp repos.
- For fast branch/ref behavior, use `internal/git/gittest` (`NewInMemoryGitManager`).
- Worktree tests should explicitly validate both git metadata and directory-side effects.

## Dependencies

- `github.com/go-git/go-git/v6`
- `github.com/go-git/go-git/v6/x/plumbing/worktree`
- `github.com/go-git/go-billy/v6/osfs`
