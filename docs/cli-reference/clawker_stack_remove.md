---
title: "clawker stack remove"
---

## clawker stack remove

Remove a stack registration

### Synopsis

Removes a stack registration from the project's clawker.yaml.

Only project registrations can be removed. Built-in (shipped) stacks cannot be
removed — they can only be shadowed by registering your own definition under
the same name.

```
clawker stack remove <name> [flags]
```

### Aliases

`remove`, `rm`

### Examples

```
  # Remove a registration
  clawker stack remove my-rust
```

### Options

```
  -h, --help   help for remove
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker stack](clawker_stack) - Manage stack definitions
