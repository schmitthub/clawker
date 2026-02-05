# Worktree Commands Package

Git worktree management commands for clawker projects.

## Package Structure

```
internal/cmd/worktree/
├── worktree.go           # Parent command, registers subcommands
├── add/
│   ├── add.go            # Create worktree for a branch
│   └── add_test.go
├── list/
│   ├── list.go           # List worktrees for current project
│   └── list_test.go
└── remove/
    ├── remove.go         # Remove worktrees by branch name
    └── remove_test.go
```

## Parent Command (`worktree.go`)

```go
func NewCmdWorktree(f *cmdutil.Factory) *cobra.Command
```

Registers: `NewCmdAdd`, `NewCmdList`, `NewCmdRemove`

## Subcommands

### Add (`add/add.go`)

Creates a git worktree for a specified branch.

```go
type AddOptions struct {
    IOStreams  *iostreams.IOStreams
    GitManager func() (*git.GitManager, error)
    Config     func() *config.Config
    Branch     string
    Base       string
}
```

**Flags:**
- `--base REF` — Base ref to create branch from (default: HEAD). Only used if branch doesn't exist.

**Behavior:**
- If worktree already exists → success (idempotent)
- If branch exists but not checked out elsewhere → check it out in new worktree
- If branch doesn't exist → create from base ref

Wraps `GitManager.SetupWorktree()` which handles all the above logic.

### List (`list/list.go`)

Lists all git worktrees for the current project.

```go
type ListOptions struct {
    IOStreams  *iostreams.IOStreams
    GitManager func() (*git.GitManager, error)
    Config     func() *config.Config
    Quiet      bool
}
```

**Output columns:** Branch, Path, Last Modified

**Flags:**
- `--quiet` / `-q` — Suppress headers, show branch names only

### Remove (`remove/remove.go`)

Removes git worktrees by branch name.

```go
type RemoveOptions struct {
    IOStreams    *iostreams.IOStreams
    GitManager   func() (*git.GitManager, error)
    Config       func() *config.Config
    Prompter     func() *prompter.Prompter
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
- Batch operation: processes multiple branches, reports all errors at end

## Command Patterns

Commands use Factory function references (not direct Factory access):

```go
opts := &ListOptions{
    IOStreams:  f.IOStreams,
    GitManager: f.GitManager,
    Config:     f.Config,
}
```

## Dependencies

- `f.GitManager()` — Access to git operations via `internal/git.GitManager`
- `f.Config().Project()` — Project info and worktree directory management

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
