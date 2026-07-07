---
title: "clawker skill"
---

## clawker skill

Manage the clawker Claude Code skill plugin

### Synopsis

Manage the clawker-support Claude Code skill plugin.

The plugin ships two skills: clawker-support gives Claude Code hands-on
knowledge of clawker internals — configuration, Dockerfile generation,
firewall rules, MCP wiring, and troubleshooting — and harness-toolchain-dev
covers authoring harness bundles and toolchain definitions. Both read the
real config schema and templates so the advice they give is always accurate.

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

* [clawker](clawker) - Run coding agents in secure Docker containers with clawker
