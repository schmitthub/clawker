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

Aliases are stored in user settings (settings.yaml). An alias cannot
shadow an existing clawker command. Overwriting an existing alias
requires --clobber.

```
clawker alias set <alias> <expansion> [flags]
```

### Examples

```
  # Shortcut with appended arguments
  clawker alias set co "container run --rm -it"

  # Positional placeholders
  clawker alias set lg "logs $1 --tail $2"

  # Overwrite an existing alias
  clawker alias set dev "run --rm -it --agent dev @" --clobber
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
