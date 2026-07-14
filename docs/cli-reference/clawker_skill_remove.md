---
title: "clawker skill remove"
---

## clawker skill remove

Remove the clawker agent skills plugin

### Synopsis

Remove the clawker-support agent skills plugin.

For the claude harness this uninstalls the plugin through the Claude CLI;
the marketplace registration is left in place. For codex, opencode, and pi
it deletes the plugin's skills from the harness's native skills directory.

```
clawker skill remove [flags]
```

### Aliases

`remove`, `uninstall`, `rm`

### Examples

```
  # Remove from Claude Code (default)
  clawker skill remove

  # Remove from another harness
  clawker skill remove --harness codex

  # Remove from project scope (claude only)
  clawker skill remove --scope project
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

* [clawker skill](clawker_skill) - Manage the clawker agent skills plugin
