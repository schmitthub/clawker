## clawker worktree remove

Remove one or more worktrees

### Synopsis

Removes worktrees by their branch name.

This removes both the git worktree metadata and the filesystem directory.
The branch itself is preserved unless --delete-branch is specified.

If the worktree has uncommitted changes, the command will fail unless
--force is used.

```
clawker worktree remove BRANCH [BRANCH...] [flags]
```

### Aliases

`remove`, `rm`

### Examples

```
  # Remove a worktree
  clawker worktree remove feat-42

  # Remove multiple worktrees
  clawker worktree rm feat-42 feat-43

  # Remove worktree and delete the branch
  clawker worktree remove --delete-branch feat-42

  # Force remove a worktree with uncommitted changes
  clawker worktree remove --force feat-42
```

### Options

```
      --delete-branch   Also delete the branch after removing the worktree
  -f, --force           Force removal even if worktree has uncommitted changes
  -h, --help            help for remove
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker worktree](clawker_worktree.md) - Manage git worktrees for isolated branch development
