---
title: "clawker alias set"
---

## clawker alias set

Create or update a command alias

### Synopsis

Create a shortcut for a clawker command.

The expansion is appended to 'clawker' in place of the alias name; any
extra arguments are appended after it. Use $1..$N in the expansion to
place positional arguments explicitly.

Be cautious with placeholders: most shells expand $1 before clawker
sees it. Escape them ("\$1") or single-quote the expansion.

Overwriting an existing alias requires --clobber.

```
clawker alias set <alias> <expansion> [flags]
```

### Examples

```
  # Shortcut with appended arguments
  clawker alias set fable "container run --rm -it --agent fable @ --dangerously-skip-permissions --model \"claude-fable-5\""

  # Positional placeholders
  clawker alias set wtm "container run --rm -it --agent \$1 --worktree \$2:main @ --dangerously-skip-permissions"

  # Overwrite an existing alias
  clawker alias set go "run --rm -it --agent go @" --clobber
```

### Options

```
      --clobber   Overwrite an existing alias
  -h, --help      help for set
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker alias](clawker_alias) - Manage command aliases
