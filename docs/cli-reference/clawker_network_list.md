## clawker network list

List networks

### Synopsis

Lists all networks created by clawker.

Networks are used for container communication and monitoring stack
integration. The primary network is clawker-net.

```
clawker network list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List all clawker networks
  clawker network list

  # List networks (short form)
  clawker network ls

  # List network names only
  clawker network ls -q
```

### Options

```
  -h, --help    help for list
  -q, --quiet   Only display network names
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker network](clawker_network.md) - Manage networks
