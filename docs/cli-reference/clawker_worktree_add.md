## clawker worktree add

Create a worktree for a branch

### Synopsis

Creates a git worktree for the specified branch.

If the worktree already exists, the command succeeds (idempotent).
If the branch exists but isn't checked out elsewhere, it's checked out in the new worktree.
If the branch doesn't exist, it's created from the base ref (default: HEAD).

```
clawker worktree add BRANCH [flags]
```

### Examples

```
  # Create a worktree for a new branch
  clawker worktree add feat-42

  # Create a worktree from a specific base
  clawker worktree add feat-43 --base main

  # Create a worktree for a branch with slashes
  clawker worktree add feature/new-login
```

### Options

```
      --base string   Base ref to create branch from (default: HEAD)
  -h, --help          help for add
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker worktree](clawker_worktree.md) - Manage git worktrees for isolated branch development
