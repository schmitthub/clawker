---
title: "clawker plugin"
---

## clawker plugin

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

### Aliases

`plugin`, `skill`

### Examples

```
  # Install the clawker plugin for Claude Code
  clawker plugin install

  # Install for another harness
  clawker plugin install --harness codex

  # Show the manual install commands
  clawker plugin show

  # Remove the clawker plugin
  clawker plugin remove
```

### Subcommands

* [clawker plugin install](clawker_plugin_install) - Install the clawker agent skills plugin
* [clawker plugin remove](clawker_plugin_remove) - Remove the clawker agent skills plugin
* [clawker plugin show](clawker_plugin_show) - Show manual install commands for the clawker plugin

### Options

```
  -h, --help   help for plugin
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Run coding agents in secure Docker containers with clawker
