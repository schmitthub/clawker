---
title: "clawker skill show"
---

## clawker skill show

Show manual install commands for the clawker skill plugin

### Synopsis

Display the commands needed to manually install the
clawker-support skill plugin for a harness.

```
clawker skill show [flags]
```

### Examples

```
  # Claude Code (default)
  clawker skill show

  # Another harness
  clawker skill show --harness opencode
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

* [clawker skill](clawker_skill) - Manage the clawker agent skills plugin
