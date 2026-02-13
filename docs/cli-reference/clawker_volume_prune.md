## clawker volume prune

Remove unused local volumes

### Synopsis

Removes all clawker-managed volumes that are not currently in use.

This command removes volumes that are not attached to any container.
Use with caution as this will permanently delete data.

```
clawker volume prune [OPTIONS] [flags]
```

### Examples

```
  # Remove all unused clawker volumes
  clawker volume prune

  # Remove without confirmation prompt
  clawker volume prune --force
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

* [clawker volume](clawker_volume.md) - Manage volumes
