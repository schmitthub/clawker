---
title: "clawker worktree list"
---

## clawker worktree list

List worktrees for the current project

### Synopsis

Lists git worktrees registered for the current project.

Shows the branch name, filesystem path, HEAD commit, and last modified time
for each worktree. Use --all to list worktrees across all registered projects.

```
clawker worktree list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List worktrees for the current project
  clawker worktree list

  # List worktrees (short form)
  clawker worktree ls

  # List worktrees across all projects
  clawker worktree ls -a

  # List only branch names
  clawker worktree ls -q
```

### Options

```
  -a, --all     List worktrees across all registered projects
  -h, --help    help for list
  -q, --quiet   Only display branch names
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker worktree](clawker_worktree) - Manage git worktrees for isolated branch development
