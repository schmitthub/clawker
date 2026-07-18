---
title: "clawker plugin install"
---

## clawker plugin install

Install the clawker agent skills plugin

### Synopsis

Install the clawker-support agent skills plugin.

For the claude harness this adds the schmitthub/clawker-plugin marketplace
(if not already present) and installs the clawker-support plugin through the
Claude CLI. For codex, opencode, and pi it fetches the plugin from the
marketplace and copies its skills into the harness's native skills
directory. The plugin gives your coding agent hands-on knowledge of
clawker configuration, troubleshooting, and internals.

```
clawker plugin install [flags]
```

### Examples

```
  # Install for Claude Code (default)
  clawker plugin install

  # Install for another harness
  clawker plugin install --harness codex

  # Install with project scope (claude only)
  clawker plugin install --scope project
```

### Options

```
      --harness string   Target harness: claude, codex, opencode, or pi (default "claude")
  -h, --help             help for install
  -s, --scope string     Installation scope: user, project, or local (claude only) (default "user")
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker plugin](clawker_plugin) - Manage the clawker agent skills plugin
