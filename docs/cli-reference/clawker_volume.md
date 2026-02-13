## clawker volume

Manage volumes

### Synopsis

Manage clawker volumes.

Clawker uses volumes to persist workspace data (in snapshot mode),
configuration, and command history between container runs.

### Examples

```
  # List clawker volumes
  clawker volume ls

  # Remove a volume
  clawker volume rm clawker.myapp.dev-workspace

  # Inspect a volume
  clawker volume inspect clawker.myapp.dev-workspace
```

### Subcommands

* [clawker volume create](clawker_volume_create.md) - Create a volume
* [clawker volume inspect](clawker_volume_inspect.md) - Display detailed information on one or more volumes
* [clawker volume list](clawker_volume_list.md) - List volumes
* [clawker volume prune](clawker_volume_prune.md) - Remove unused local volumes
* [clawker volume remove](clawker_volume_remove.md) - Remove one or more volumes

### Options

```
  -h, --help   help for volume
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
