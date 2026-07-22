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
├── remove/
│   ├── remove.go         # Remove worktrees by branch name
│   └── remove_test.go
└── shared/
    ├── completion.go     # BranchCompletions — shell completion for worktree branch args
    └── completion_test.go
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
    NoTrack   bool
}
```

**Flags:**

- `--base REF` — Base ref to create branch from (default: HEAD). Only used if branch doesn't exist.
- `--no-track` — Do not configure upstream tracking when the branch is derived from a remote-tracking branch (parity with `git worktree add --no-track`).

**Behavior:**

- If worktree already exists in registry → error (`ErrWorktreeExists`). This is strict creation semantics.
- If branch exists but not checked out elsewhere → check it out in new worktree
- If branch doesn't exist but a remote-tracking ref matches its name (the dwim rule, e.g. after `git fetch`) → create from the remote tip with upstream tracking (unless `--no-track`)
- Otherwise → create from base ref

`clawker worktree add` is the canonical, full-surface worktree command — tracking flags live here. The `--worktree` shortcut on container commands is a limited happy-path alias (default track-on-match, no flag surface).

For idempotent "get or create" behavior, use `--worktree` on container commands instead (see `internal/cmd/container/shared/CLAUDE.md`).

Delegates orchestration to `project.ProjectManager.CurrentProject(ctx).CreateWorktree(...)`.

### List (`list/list.go`)

Lists all git worktrees for the current project.

```go
type ListOptions struct {
    IOStreams      *iostreams.IOStreams
    ProjectManager func() (project.ProjectManager, error)
    All   bool
    Quiet bool
}
```

**Output columns:** Branch, Path, HEAD, Modified, Status (when `--all`: PROJECT prepended)

**Status values:**

- `healthy` — All checks pass (directory, `.git` file, git metadata, branch all exist)
- `registry_only` — Directory deleted but registry entry remains (safe to prune)
- `dotgit_missing` — Directory exists but `.git` file is missing or is a directory
- `git_metadata_missing` — Git metadata directory (`.git/worktrees/<slug>/`) missing
- `broken` — Branch has been deleted

**Flags:**

- `--all` / `-a` — List worktrees across all registered projects
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

**Prunable criteria:** Directory missing, git worktree metadata missing, or branch deleted (stale registry entry). Locked worktrees (via `git worktree lock`) are skipped even if stale — reported in output with unlock instructions.

Delegates pruning to `project.ProjectManager.CurrentProject(ctx).PruneStaleWorktrees(...)`.

### Remove (`remove/remove.go`)

Removes git worktrees by branch name.

```go
type RemoveOptions struct {
    IOStreams      *iostreams.IOStreams
    ProjectManager func() (project.ProjectManager, error)
    Force        bool
    DeleteBranch bool
    Branches     []string
}
```

**Flags:**

- `--force` — Remove even with uncommitted changes
- `--delete-branch` — Also delete the git branch after removing worktree

**Safety checks:**

- Verifies worktree has no uncommitted changes (unless `--force`)
- If status cannot be verified, requires `--force`
- `--delete-branch` shows a warning and suggests `git branch -D` when branch has unmerged commits (`git.ErrBranchNotMerged`); the worktree is still removed successfully
- Batch operation: processes multiple branches, reports all errors at end

**Internal helpers:**

- `removeSingleWorktree(ctx, opts, proj, branch)` — per-branch orchestration; handles `ErrBranchNotMerged` with user-friendly warning

**Completion:** `ValidArgsFunction` wired to `shared.BranchCompletions` — tab-completes existing worktree branch names.

### Shared (`shared/completion.go`)

```go
func BranchCompletions(pmFn func() (project.ProjectManager, error)) cobra.CompletionFunc
```

Cobra completion function suggesting the current project's worktree branch names (`CurrentProject` → `ListWorktrees`). Suggests every registry entry regardless of health (detached/broken/prunable are all valid removal targets), excludes branches already present in the command's positional args (multi-arg support), sorted, `ShellCompDirectiveNoFileComp`. All failures degrade to no suggestions (breadcrumbs via `cobra.CompDebugln`). Wire via `ValidArgsFunction` for positional branch args and `RegisterFlagCompletionFunc` for `--worktree` flags.

## Command Patterns

Commands use Factory function references (not direct Factory access):

```go
opts := &ListOptions{
    IOStreams:      f.IOStreams,
    ProjectManager: f.ProjectManager,
}
```

## Dependencies

- `f.ProjectManager()` — Project-layer manager built from `config.Config`

## Testing

Tests use the Cobra+Factory pattern without Docker (worktree commands only interact with git/filesystem). Tests construct `&cmdutil.Factory{}` literals directly and inject `project/mocks.NewMockProjectManager()` via `runF` or via the `ProjectManager` func field.

```go
f := &cmdutil.Factory{IOStreams: ios}
cmd := NewCmdRemove(f, func(_ context.Context, opts *RemoveOptions) error {
    // assertions on opts
    return nil
})
cmd.SetArgs([]string{"--force", "feat-a"})
err := cmd.Execute()
```

See `add/add_test.go`, `list/list_test.go`, and `remove/remove_test.go` for examples.
