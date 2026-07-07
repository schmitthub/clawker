---
title: "clawker skill remove"
---

## clawker skill remove

Remove the clawker agent skills plugin

### Synopsis

Remove the clawker-support agent skills plugin.

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

* [clawker skill](clawker_skill) - Manage the clawker agent skills plugin
