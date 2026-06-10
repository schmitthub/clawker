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

Active aliases are stored in user settings (settings.yaml) and can also
be edited with 'clawker settings edit'. The project config's aliases key
is a sharing vehicle only: 'clawker alias import' deliberately copies
project aliases into settings, and 'clawker alias export' publishes
settings aliases into the project config. Project aliases are never
applied automatically.

### Examples

```
  # Define an alias
  clawker alias set co "container run --rm -it"

  # List configured aliases
  clawker alias list

  # Import aliases shared in the project config
  clawker alias import
```

### Subcommands

* [clawker alias delete](clawker_alias_delete) - Delete a command alias
* [clawker alias export](clawker_alias_export) - Export aliases to the project config
* [clawker alias import](clawker_alias_import) - Import aliases from the project config
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
