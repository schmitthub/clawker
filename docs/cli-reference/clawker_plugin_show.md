---
title: "clawker plugin show"
---

## clawker plugin show

Show manual install commands for the clawker plugin

### Synopsis

Display the commands needed to manually install the
clawker-support skill plugin for a harness.

```
clawker plugin show [flags]
```

### Examples

```
  # Claude Code (default)
  clawker plugin show

  # Another harness
  clawker plugin show --harness opencode
```

### Options

```
      --harness string   Target harness: claude, codex, opencode, or pi (default "claude")
  -h, --help             help for show
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker plugin](clawker_plugin) - Manage the clawker agent skills plugin
