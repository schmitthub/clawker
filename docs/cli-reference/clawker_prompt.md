---
title: "clawker prompt"
---

## clawker prompt

Access clawker's managed agent briefing

### Synopsis

Commands for the managed agent briefing clawker provides to coding agents
running in its containers.

The briefing is clawker-owned and harness-agnostic: it explains the container
environment, the egress firewall, and how the agent should surface blocked
connections. A harness whose agent reads managed context from a fixed image
path declares that path in its manifest (managed_prompt) and gets the file
baked at build time; deployments whose agent discovers context in
runtime-mounted state can print it host-side and place it themselves.

### Examples

```
  # Print the briefing
  clawker prompt print
```

### Subcommands

* [clawker prompt print](clawker_prompt_print) - Print the managed agent briefing to stdout

### Options

```
  -h, --help   help for prompt
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Run coding agents in secure Docker containers with clawker
