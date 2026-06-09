---
title: "clawker worktree add"
---

## clawker worktree add

Create a worktree for a branch

### Synopsis

Creates a git worktree for the specified branch.

If the worktree already exists, the command will fail.
If the branch exists but isn't checked out elsewhere, it's checked out in the new worktree.
If the branch doesn't exist but a remote-tracking branch matches its name (e.g. after
'git fetch'), it's created from the remote tip with upstream tracking configured.
Otherwise the branch is created from the base ref (default: HEAD).

```
clawker worktree add BRANCH [flags]
```

### Examples

```
  # Create a worktree for a new branch
  clawker worktree add feat-42

  # Create a worktree from a specific base
  clawker worktree add feat-43 --base main

  # Check out a fetched remote branch with upstream tracking
  clawker worktree add feature/new-login

  # Create the branch without tracking the remote
  clawker worktree add feature/new-login --no-track
```

### Options

```
      --base string   Base ref to create branch from (default: HEAD)
  -h, --help          help for add
      --no-track      Do not set up upstream tracking when basing the branch on a remote-tracking branch
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker worktree](clawker_worktree) - Manage git worktrees for isolated branch development
