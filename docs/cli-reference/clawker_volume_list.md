## clawker volume list

List volumes

### Synopsis

Lists all volumes created by clawker.

Volumes are used to persist data between container runs, including:
  - Workspace data (in snapshot mode)
  - Configuration files
  - Command history

```
clawker volume list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List all clawker volumes
  clawker volume list

  # List volumes (short form)
  clawker volume ls

  # List volume names only
  clawker volume ls -q
```

### Options

```
  -h, --help    help for list
  -q, --quiet   Only display volume names
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker volume](clawker_volume.md) - Manage volumes
