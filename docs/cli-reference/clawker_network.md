---
title: "clawker network"
---

## clawker network

Manage networks

### Synopsis

Manage clawker networks.

Clawker uses a dedicated network (clawker-net) for container communication
and monitoring stack integration.

### Examples

```
  # List clawker networks
  clawker network ls

  # Inspect the clawker network
  clawker network inspect clawker-net

  # Create a new network
  clawker network create mynetwork

  # Remove a network
  clawker network rm mynetwork
```

### Subcommands

* [clawker network create](clawker_network_create) - Create a network
* [clawker network inspect](clawker_network_inspect) - Display detailed information on one or more networks
* [clawker network list](clawker_network_list) - List networks
* [clawker network prune](clawker_network_prune) - Remove unused networks
* [clawker network remove](clawker_network_remove) - Remove one or more networks

### Options

```
  -h, --help   help for network
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Manage Claude Code in secure Docker containers with clawker
