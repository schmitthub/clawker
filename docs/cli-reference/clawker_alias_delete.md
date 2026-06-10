---
title: "clawker alias delete"
---

## clawker alias delete

Delete a command alias

### Synopsis

Delete a command alias from user settings.

Shipped default aliases cannot be removed outright — deleting one
disables it by storing an empty expansion, which the alias loader
skips.

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
  clawker alias delete dev
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
