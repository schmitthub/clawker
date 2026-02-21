# Worktree Commands Package

Git worktree management commands for clawker projects.

## Package Structure

```text
internal/cmd/worktree/
├── worktree.go           # Parent command, registers subcommands
├── add/
│   ├── add.go            # Create worktree for a branch
│   └── add_test.go
├── list/
│   ├── list.go           # List worktrees for current project
│   └── list_test.go
├── prune/
│   ├── prune.go          # Remove stale registry entries
│   └── prune_test.go
└── remove/
    ├── remove.go         # Remove worktrees by branch name
    └── remove_test.go
```

## Parent Command (`worktree.go`)

```go
func NewCmdWorktree(f *cmdutil.Factory) *cobra.Command
```

Registers: `NewCmdAdd`, `NewCmdList`, `NewCmdPrune`, `NewCmdRemove`

## Subcommands

### Add (`add/add.go`)

Creates a git worktree for a specified branch.

```go
type AddOptions struct {
    IOStreams *iostreams.IOStreams
    ProjectManager func() (project.ProjectManager, error)
    Branch    string
    Base      string
}
```

**Flags:**

- `--base REF` — Base ref to create branch from (default: HEAD). Only used if branch doesn't exist.

**Behavior:**

- If worktree already exists → success (idempotent)
- If branch exists but not checked out elsewhere → check it out in new worktree
- If branch doesn't exist → create from base ref

Delegates orchestration to `project.ProjectManager.FromCWD(...).CreateWorktree(...)`.

### List (`list/list.go`)

Lists all git worktrees for the current project.

```go
type ListOptions struct {
    IOStreams  *iostreams.IOStreams
    GitManager func() (*git.GitManager, error)
    ProjectManager func() (project.ProjectManager, error)
    Quiet      bool
}
```

**Output columns:** Branch, Path, HEAD, Modified, Status

**Status values:**

- `healthy` — healthy worktree
- `dir missing` — worktree directory doesn't exist
- `git missing` — .git file missing or invalid
- `dir missing, git missing` — stale entry (prunable)
- `error: path error: ...` — failed to resolve worktree path (not prunable)

**Flags:**

- `--quiet` / `-q` — Suppress headers, show branch names only

**Prune Warning:** When stale entries are detected, shows a warning suggesting `clawker worktree prune`.

### Prune (`prune/prune.go`)

Removes stale worktree entries from the project registry.

```go
type PruneOptions struct {
    IOStreams *iostreams.IOStreams
    ProjectManager func() (project.ProjectManager, error)
    DryRun    bool
}
```

**When to use:**

- After using native `git worktree remove` (bypasses clawker registry)
- When `clawker worktree remove` failed partway through
- After manual deletion of worktree directories

**Flags:**

- `--dry-run` — Show what would be pruned without removing

**Prunable criteria:** Both directory and git metadata are missing (stale registry entry).

Delegates pruning to `project.ProjectManager.FromCWD(...).PruneStaleWorktrees(...)`.

### Remove (`remove/remove.go`)

Removes git worktrees by branch name.

```go
type RemoveOptions struct {
    IOStreams    *iostreams.IOStreams
    GitManager   func() (*git.GitManager, error)
    ProjectManager func() (project.ProjectManager, error)
    Prompter     func() *prompter.Prompter
    Force        bool
    DeleteBranch bool
    Branches     []string
}
```

**Flags:**

- `--force` — Remove even with uncommitted changes
- `--delete-branch` — Also delete the git branch after removing worktree (uses `GitManager.DeleteBranch`)

**Safety checks:**

- Verifies worktree has no uncommitted changes (unless `--force`)
- If status cannot be verified, requires `--force`
- `--delete-branch` refuses to delete branches with unmerged commits (like `git branch -d`): prints warning and suggests `git branch -D` for force deletion. The worktree is still removed successfully.
- `--delete-branch` refuses to delete the currently checked-out branch (`git.ErrIsCurrentBranch`)
- If branch ref doesn't exist when `--delete-branch` is used, silently succeeds (branch already gone)
- Batch operation: processes multiple branches, reports all errors at end

**Internal helpers:**

- `handleBranchDelete(ios, gitMgr, branch)` — Extracted helper for branch deletion with user-friendly error reporting. Tested directly with `gittest.InMemoryGitManager` (doesn't require worktree filesystem operations).

## Command Patterns

Commands use Factory function references (not direct Factory access):

```go
opts := &ListOptions{
    IOStreams:  f.IOStreams,
    GitManager: f.GitManager,
    ProjectManager: f.ProjectManager,
}
```

## Dependencies

- `f.GitManager()` — Access to git operations via `internal/git.GitManager`
- `f.ProjectManager()` — Project-layer manager built from `config.Config`

## Testing

Tests use the Cobra+Factory pattern without Docker (worktree commands only interact with git/filesystem).

```go
// testFactory helper creates Factory with faked GitManager and Config
f, ios := testFactory(t, gitMgr, cfg)
cmd := NewCmdList(f, nil)
cmd.SetArgs([]string{})
err := cmd.Execute()
```

See `add/add_test.go`, `list/list_test.go`, and `remove/remove_test.go` for examples.
