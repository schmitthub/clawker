---
title: "clawker worktree"
---

## clawker worktree

Manage git worktrees for isolated branch development

### Synopsis

Manage git worktrees used by clawker for isolated branch development.

Worktrees allow running containers against different branches simultaneously
without switching branches in your main repository. Each worktree is a
separate checkout of the repository at a specific branch.

Worktrees are created automatically when using 'clawker run --worktree `<branch>`'.

Worktree containers mask the main repo's .git/hooks and .git/config read-only
(security measure for unattended sessions); 'git config --local', 'git remote add',
and 'git push -u' fail inside them. See https://docs.clawker.dev/worktrees#worktree-caveats

### Examples

```
  # Create a worktree for a new branch
  clawker worktree add feat-42

  # Create a worktree from a specific base
  clawker worktree add feat-43 --base main

  # List all worktrees for the current project
  clawker worktree list

  # Remove a worktree by branch name
  clawker worktree remove feat-42

  # Remove a worktree and also delete the branch\n  clawker worktree remove --delete-branch feat-42\n\n  # Preview stale entries that would be pruned
  clawker worktree prune --dry-run

  # Remove stale worktree entries from the registry
  clawker worktree prune
```

### Subcommands

* [clawker worktree add](clawker_worktree_add) - Create a worktree for a branch
* [clawker worktree list](clawker_worktree_list) - List worktrees for the current project
* [clawker worktree prune](clawker_worktree_prune) - Remove stale worktree entries from the registry
* [clawker worktree remove](clawker_worktree_remove) - Remove one or more worktrees

### Options

```
  -h, --help   help for worktree
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Manage Claude Code in secure Docker containers with clawker
