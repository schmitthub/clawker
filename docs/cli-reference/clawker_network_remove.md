## clawker network remove

Remove one or more networks

### Synopsis

Removes one or more clawker-managed networks.

Only removes networks that are not currently in use by any container.
Containers must be disconnected from the network before it can be removed.

Note: Only clawker-managed networks can be removed with this command.

```
clawker network remove NETWORK [NETWORK...] [flags]
```

### Aliases

`remove`, `rm`

### Examples

```
  # Remove a network
  clawker network remove mynetwork

  # Remove multiple networks
  clawker network rm mynetwork1 mynetwork2

  # Force remove (future: disconnect containers first)
  clawker network remove --force mynetwork
```

### Options

```
  -f, --force   Force removal (reserved for future use)
  -h, --help    help for remove
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker network](clawker_network.md) - Manage networks
