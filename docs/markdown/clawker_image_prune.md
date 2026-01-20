## clawker image prune

Remove unused images

### Synopsis

Removes all unused clawker-managed images.

By default, only dangling images (untagged images) are removed.
Use --all to remove all images not used by any container.

Use with caution as this will permanently delete images.

```
clawker image prune [OPTIONS] [flags]
```

### Examples

```
  # Remove unused (dangling) clawker images
  clawker image prune

  # Remove all unused clawker images
  clawker image prune --all

  # Remove without confirmation prompt
  clawker image prune --force
```

### Options

```
  -a, --all     Remove all unused images, not just dangling ones
  -f, --force   Do not prompt for confirmation
  -h, --help    help for prune
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker image](clawker_image.md) - Manage images
