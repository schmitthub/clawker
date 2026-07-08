---
title: "clawker harness remove"
---

## clawker harness remove

Remove a harness registration

### Synopsis

Removes a harness registration from the project's clawker.yaml.

Only project registrations can be removed. Built-in (shipped) harnesses cannot
be removed — they can only be shadowed by registering your own bundle under the
same name. If the entry also carries per-harness init config, only the
registration path is removed and the init config is left in place.

```
clawker harness remove <name> [flags]
```

### Aliases

`remove`, `rm`

### Examples

```
  # Remove a registration
  clawker harness remove codex
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

* [clawker harness](clawker_harness) - Manage harness bundles
