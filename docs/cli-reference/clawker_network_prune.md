## clawker network prune

Remove unused networks

### Synopsis

Removes all clawker-managed networks that are not currently in use.

This command removes networks that have no connected containers.
Use with caution as this may affect container communication.

Note: The built-in clawker-net network will be preserved if containers
are using it for the monitoring stack.

```
clawker network prune [OPTIONS] [flags]
```

### Examples

```
  # Remove all unused clawker networks
  clawker network prune

  # Remove without confirmation prompt
  clawker network prune --force
```

### Options

```
  -f, --force   Do not prompt for confirmation
  -h, --help    help for prune
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker network](clawker_network.md) - Manage networks
