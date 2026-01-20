## clawker rmi

Remove one or more images

### Synopsis

Removes one or more clawker-managed images.

Only removes images that were created by clawker. Use --force to
remove images even if they have stopped containers using them.

Note: Only clawker-managed images can be removed with this command.

```
clawker rmi [OPTIONS] [flags]
```

### Aliases

`rmi`, `rm`, `rmi`

### Examples

```
  # Remove an image
  clawker image remove clawker-myapp:latest

  # Remove multiple images
  clawker image rm clawker-myapp:latest clawker-backend:latest

  # Force remove an image (even if containers reference it)
  clawker image remove --force clawker-myapp:latest

  # Remove an image without pruning parent images
  clawker image rm --no-prune clawker-myapp:latest
```

### Options

```
  -f, --force      Force removal of the image
  -h, --help       help for rmi
      --no-prune   Do not delete untagged parents
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
