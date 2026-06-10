# internal/git

Leaf git operations package for repository discovery, branch safety checks, and linked worktree lifecycle.

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
gitDir := mgr.GitDir()           // absolute path to .git dir (empty for in-memory repos)
wt, err := mgr.Worktrees()
```

### High-Level Orchestration (caller-integrated)

```go
path, err := mgr.SetupWorktree(provider, branch, base, noTrack)
err = mgr.RemoveWorktree(provider, branch)
infos, err := mgr.ListWorktrees(entries)
```

Behavior details:

- `SetupWorktree` validates/reuses existing directories when possible.
- `SetupWorktree` removes orphaned git metadata for a target worktree before fresh creation.
- `SetupWorktree` refuses to create a worktree for an existing branch that is already
  checked out elsewhere — the repo root checkout or another linked worktree —
  returning `ErrBranchAlreadyCheckedOut` (path named in the wrapped message). This
  is the interlock native `git worktree add` enforces; go-git's experimental
  worktree package does not, so without it two checkouts could share one branch
  ref and a commit in either would slide the other's HEAD. The guard fires only on
  fresh creation; idempotent reuse of an already-established worktree returns
  before it. New branches can't collide, so only the existing-branch path is gated.
- `SetupWorktree` branch resolution mirrors native `git worktree add` (no network):
  - branch is a local head → check it out.
  - `base != ""` → create the branch at base; if base is a remote-tracking branch
    (`origin/foo`) the new branch also tracks it.
  - `base == ""` and a remote-tracking ref matching branch exists in exactly one
    remote (the dwim rule) → branch from that remote tip and configure upstream;
    multiple remotes → `checkout.defaultRemote` or `ErrAmbiguousRemoteBranch`;
    no match → new local branch from HEAD.
  - `noTrack` suppresses the upstream config (parity with `--no-track`).
  - branch name is itself an existing remote-tracking ref (`origin/foo`) with no
    explicit base → `ErrExplicitRemoteRef`. Native git detaches HEAD there;
    clawker's branch-keyed worktrees do not support detached HEAD, so it steers
    the caller to the bare name (which dwim-tracks the remote) instead of
    creating a literal `origin/foo` branch. (Branch is the worktree identity by
    design — detached HEAD is intentionally unsupported.)
- `RemoveWorktree` removes both git metadata and caller-managed directory.
- `ListWorktrees` returns `WorktreeInfo` for all known worktrees, including recoverable error states.

### Branch/Ref Operations

```go
branch, err := mgr.GetCurrentBranch()   // empty string when detached HEAD
hash, err := mgr.ResolveRef(ref)
exists, err := mgr.BranchExists(branch)
err = mgr.CreateBranch(branch, base)    // base empty → HEAD; ErrBranchAlreadyExists if exists
err = mgr.DeleteBranch(branch)

// Remote-tracking (dwim) helpers, used by SetupWorktree:
remote, hash, found, err := mgr.ResolveRemoteTrackingBranch(branch) // refs/remotes/*/<branch>
err = mgr.SetBranchUpstream(localBranch, remote, remoteBranch)      // branch.<local>.{remote,merge}
```

`ResolveRemoteTrackingBranch` returns `found=false` (nil error) when no remote has
the branch; `ErrAmbiguousRemoteBranch` when multiple remotes match and
`checkout.defaultRemote` does not disambiguate. `SetBranchUpstream`'s `remoteBranch`
is the branch name on the remote (may differ from `localBranch`, e.g. a local
`mybranch` tracking `origin/foo`). Like `git branch --set-upstream-to`, it upserts:
a pre-existing `branch.<local>` config section is updated in place (unrelated keys
preserved), never rejected.

`DeleteBranch` is equivalent to safe `git branch -d` semantics:

- refuses current branch (`ErrIsCurrentBranch`)
- refuses unmerged branch (`ErrBranchNotMerged`)
- returns `ErrBranchNotFound` when missing

### Worktree Lock

```go
locked, err := mgr.IsWorktreeLocked(slug)
```

Checks if `.git/worktrees/<slug>/locked` exists (created by `git worktree lock`). Returns `(false, nil)` for in-memory repos. Non-nil error on unexpected filesystem failures (permissions, etc.).

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
- `ErrBranchAlreadyExists`
- `ErrAmbiguousRemoteBranch`
- `ErrExplicitRemoteRef`
- `ErrBranchAlreadyCheckedOut`

Prefer `errors.Is` checks at command/service boundaries.

## Testing Guidance

- For real linked worktree behavior, use filesystem-backed temp repos.
- For fast branch/ref behavior, use `internal/git/gittest` (`NewInMemoryGitManager`).
- Worktree tests should explicitly validate both git metadata and directory-side effects.

## Dependencies

- `github.com/go-git/go-git/v6`
- `github.com/go-git/go-git/v6/x/plumbing/worktree`
- `github.com/go-git/go-billy/v6/osfs`
