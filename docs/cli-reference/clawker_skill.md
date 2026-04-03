---
title: "clawker skill"
---

## clawker skill

Manage the clawker Claude Code skill plugin

### Synopsis

Manage the clawker-support Claude Code skill plugin.

The clawker-support skill gives Claude Code hands-on knowledge of clawker
internals — configuration, Dockerfile generation, firewall rules, MCP wiring,
and troubleshooting. It reads the real config schema and template so the
advice it gives is always accurate.

### Examples

```
  # Install the clawker skill plugin
  clawker skill install

  # Show the manual install commands
  clawker skill show

  # Remove the clawker skill plugin
  clawker skill remove
```

### Subcommands

* [clawker skill install](clawker_skill_install) - Install the clawker skill plugin for Claude Code
* [clawker skill remove](clawker_skill_remove) - Remove the clawker skill plugin from Claude Code
* [clawker skill show](clawker_skill_show) - Show manual install commands for the clawker skill plugin

### Options

```
  -h, --help   help for skill
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Manage Claude Code in secure Docker containers with clawker
