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

* [clawker network create](clawker_network_create.md) - Create a network
* [clawker network inspect](clawker_network_inspect.md) - Display detailed information on one or more networks
* [clawker network list](clawker_network_list.md) - List networks
* [clawker network prune](clawker_network_prune.md) - Remove unused networks
* [clawker network remove](clawker_network_remove.md) - Remove one or more networks

### Options

```
  -h, --help   help for network
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
