---
title: "clawker alias delete"
---

## clawker alias delete

Delete a command alias

### Synopsis

Delete a command alias.

The alias is removed from every config file that defines it, so one
delete clears the name regardless of which layer a value lives in.

Shipped default aliases cannot be removed outright — deleting one
disables it by storing an empty expansion in the user-level config
file, which the alias loader skips.

```
clawker alias delete <alias> [flags]
```

### Aliases

`delete`, `rm`

### Examples

```
  # Delete a user-defined alias
  clawker alias delete co

  # Disable the shipped default
  clawker alias delete go
```

### Options

```
  -h, --help   help for delete
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker alias](clawker_alias) - Manage command aliases
