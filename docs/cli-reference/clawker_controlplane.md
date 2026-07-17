---
title: "clawker controlplane"
---

## clawker controlplane

Break-glass control plane lifecycle

### Synopsis

Explicit lifecycle control for the clawker control plane container.

The control plane is normally bootstrapped on demand the first time any
command needs to talk to it (for example, `clawker firewall status`).
These subcommands exist for debugging, upgrades, and recovery when you
need to observe or manipulate the CP directly.

### Examples

```
  # Start the control plane (idempotent)
  clawker controlplane up

  # Show CP health
  clawker controlplane status

  # Stop the control plane
  clawker controlplane down
```

### Subcommands

* [clawker controlplane agents](clawker_controlplane_agents) - List agents currently registered with the control plane
* [clawker controlplane down](clawker_controlplane_down) - Stop the control plane
* [clawker controlplane status](clawker_controlplane_status) - Show control plane health
* [clawker controlplane up](clawker_controlplane_up) - Start the control plane

### Options

```
  -h, --help   help for controlplane
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Run coding agents in secure Docker containers with clawker
