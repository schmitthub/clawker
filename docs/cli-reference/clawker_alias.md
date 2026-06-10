---
title: "clawker alias"
---

## clawker alias

Manage command aliases

### Synopsis

Manage user-defined command aliases.

Aliases are shortcuts expanded before execution: the stored value is
appended to 'clawker' in place of the alias name, and any further
arguments are appended after it. Values may reference positional
arguments as $1..$N.

Aliases live in the project config's aliases key and merge across all
layers: project files discovered in the walk-up override the user-level
clawker.yaml in the config directory, which overrides shipped defaults.
'clawker alias set' writes the user-level file; 'clawker alias export'
publishes aliases into the project's own config file so they are
version-controlled with the repo.

### Examples

```
  # Define an alias
  clawker alias set fable "container run --rm -it --agent fable @ --dangerously-skip-permissions --model \"claude-fable-5\""

  clawker alias set wt "container run --rm -it --agent $1 --worktree $2:main @ --dangerously-skip-permissions"

  # List configured aliases
  clawker alias list

  # Share aliases with the team via the project config
  clawker alias export
```

### Subcommands

* [clawker alias delete](clawker_alias_delete) - Delete a command alias
* [clawker alias export](clawker_alias_export) - Export aliases to the project config
* [clawker alias list](clawker_alias_list) - List configured command aliases
* [clawker alias set](clawker_alias_set) - Create or update a command alias

### Options

```
  -h, --help   help for alias
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Manage Claude Code in secure Docker containers with clawker
