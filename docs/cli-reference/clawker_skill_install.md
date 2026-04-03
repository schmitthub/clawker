---
title: "clawker skill install"
---

## clawker skill install

Install the clawker skill plugin for Claude Code

### Synopsis

Install the clawker-support skill plugin for Claude Code.

This adds the schmitthub/claude-plugins marketplace (if not already present)
and installs the clawker-support plugin. The plugin gives Claude Code
hands-on knowledge of clawker configuration, troubleshooting, and internals.

```
clawker skill install [flags]
```

### Examples

```
  # Install with default user scope
  clawker skill install

  # Install with project scope
  clawker skill install --scope project
```

### Options

```
  -h, --help           help for install
  -s, --scope string   Installation scope: user, project, or local (default "user")
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker skill](clawker_skill) - Manage the clawker Claude Code skill plugin
