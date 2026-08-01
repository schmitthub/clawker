---
title: "clawker prompt print"
---

## clawker prompt print

Print the managed agent briefing to stdout

### Synopsis

Prints clawker's managed agent briefing — the document that tells a coding
agent it is running inside a clawker container and how to work with the
egress firewall, credential forwarding, and workspace modes.

Harness images whose agent reads managed context from a fixed image path get
this file baked in at build time. For agents that read context from
runtime-mounted state instead, pipe this output to wherever the agent
discovers it.

```
clawker prompt print [flags]
```

### Examples

```
  # Show the briefing
  clawker prompt print

  # Place it where an agent reads bootstrap context from a bind-mounted tree
  clawker prompt print > home/.openclaw/workspace/clawker/AGENTS.md
```

### Options

```
  -h, --help   help for print
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker prompt](clawker_prompt) - Access clawker's managed agent briefing
