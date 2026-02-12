## clawker network inspect

Display detailed information on one or more networks

### Synopsis

Returns low-level information about clawker networks.

Outputs detailed network information in JSON format, including
connected containers and configuration.

```
clawker network inspect NETWORK [NETWORK...] [flags]
```

### Examples

```
  # Inspect a network
  clawker network inspect clawker-net

  # Inspect multiple networks
  clawker network inspect clawker-net myapp-net

  # Inspect with verbose output (includes services and tasks)
  clawker network inspect --verbose clawker-net
```

### Options

```
  -h, --help      help for inspect
  -v, --verbose   Verbose output for swarm networks
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker network](clawker_network.md) - Manage networks
