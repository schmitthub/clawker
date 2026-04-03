---
title: "clawker skill remove"
---

## clawker skill remove

Remove the clawker skill plugin from Claude Code

### Synopsis

Remove the clawker-support skill plugin from Claude Code.

This uninstalls the plugin from the specified scope. The marketplace
registration is left in place.

```
clawker skill remove [flags]
```

### Aliases

`remove`, `uninstall`, `rm`

### Examples

```
  # Remove with default user scope
  clawker skill remove

  # Remove from project scope
  clawker skill remove --scope project
```

### Options

```
  -h, --help           help for remove
  -s, --scope string   Uninstall from scope: user, project, or local (default "user")
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker skill](clawker_skill) - Manage the clawker Claude Code skill plugin
