## clawker worktree prune

Remove stale worktree entries from the registry

### Synopsis

Removes worktree entries from the project registry when both the worktree
directory and git metadata no longer exist.

This can happen when:
- Native 'git worktree remove' was used (bypasses clawker registry)
- 'clawker worktree remove' failed partway through
- Manual deletion of worktree directory

Use 'clawker worktree list' to see which entries are stale before pruning.

```
clawker worktree prune [flags]
```

### Examples

```
  # Preview what would be pruned
  clawker worktree prune --dry-run

  # Remove all stale worktree entries
  clawker worktree prune
```

### Options

```
      --dry-run   Show what would be pruned without removing
  -h, --help      help for prune
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker worktree](clawker_worktree.md) - Manage git worktrees for isolated branch development
