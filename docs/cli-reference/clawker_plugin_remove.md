---
title: "clawker plugin remove"
---

## clawker plugin remove

Remove the clawker agent skills plugin

### Synopsis

Remove the clawker-support agent skills plugin.

For the claude harness this uninstalls the plugin through the Claude CLI;
the marketplace registration is left in place. For codex, opencode, and pi
it deletes the plugin's skills from the harness's native skills directory.

```
clawker plugin remove [flags]
```

### Aliases

`remove`, `uninstall`, `rm`

### Examples

```
  # Remove from Claude Code (default)
  clawker plugin remove

  # Remove from another harness
  clawker plugin remove --harness codex

  # Remove from project scope (claude only)
  clawker plugin remove --scope project
```

### Options

```
      --harness string   Target harness: claude, codex, opencode, or pi (default "claude")
  -h, --help             help for remove
  -s, --scope string     Uninstall from scope: user, project, or local (claude only) (default "user")
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker plugin](clawker_plugin) - Manage the clawker agent skills plugin
