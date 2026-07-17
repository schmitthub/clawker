---
title: "clawker skill"
---

## clawker skill

Manage the clawker agent skills plugin

### Synopsis

Manage the clawker-support agent skills plugin.

The clawker-support skill gives your coding agent hands-on knowledge of
clawker internals — configuration, Dockerfile generation, firewall rules,
MCP wiring, and troubleshooting. It reads the real config schema and
templates so the advice it gives is always accurate.

The claude harness installs through the Claude CLI marketplace; codex,
opencode, and pi install by copying the plugin's skills into the harness's
native skills directory from the marketplace.

### Examples

```
  # Install the clawker skill plugin for Claude Code
  clawker skill install

  # Install for another harness
  clawker skill install --harness codex

  # Show the manual install commands
  clawker skill show

  # Remove the clawker skill plugin
  clawker skill remove
```

### Subcommands

* [clawker skill install](clawker_skill_install) - Install the clawker agent skills plugin
* [clawker skill remove](clawker_skill_remove) - Remove the clawker agent skills plugin
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

* [clawker](clawker) - Run coding agents in secure Docker containers with clawker
