## clawker worktree list

List worktrees for the current project

### Synopsis

Lists all git worktrees registered for the current project.

Shows the branch name, filesystem path, HEAD commit, and last modified time
for each worktree.

```
clawker worktree list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List all worktrees
  clawker worktree list

  # List worktrees (short form)
  clawker worktree ls

  # List only branch names
  clawker worktree ls -q
```

### Options

```
  -h, --help    help for list
  -q, --quiet   Only display branch names
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker worktree](clawker_worktree.md) - Manage git worktrees for isolated branch development
