## clawker volume remove

Remove one or more volumes

### Synopsis

Removes one or more clawker-managed volumes.

Only removes volumes that are not currently in use by any container.
Use --force to remove volumes that may be in use (dangerous).

Note: Only clawker-managed volumes can be removed with this command.

```
clawker volume remove VOLUME [VOLUME...] [flags]
```

### Aliases

`remove`, `rm`

### Examples

```
  # Remove a volume
  clawker volume remove clawker.myapp.ralph-workspace

  # Remove multiple volumes
  clawker volume rm clawker.myapp.ralph-workspace clawker.myapp.ralph-config

  # Force remove a volume
  clawker volume remove --force clawker.myapp.ralph-workspace
```

### Options

```
  -f, --force   Force removal of volumes
  -h, --help    help for remove
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker volume](clawker_volume.md) - Manage volumes
