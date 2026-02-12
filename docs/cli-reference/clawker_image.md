## clawker image

Manage images

### Synopsis

Manage clawker images.

This command provides image management operations similar to Docker's
image management commands.

### Examples

```
  # List clawker images
  clawker image ls

  # Build an image
  clawker image build

  # Remove an image
  clawker image rm clawker-myapp:latest

  # Inspect an image
  clawker image inspect clawker-myapp:latest

  # Remove unused images
  clawker image prune
```

### Subcommands

* [clawker image build](clawker_image_build.md) - Build an image from a clawker project
* [clawker image inspect](clawker_image_inspect.md) - Display detailed information on one or more images
* [clawker image list](clawker_image_list.md) - List images
* [clawker image prune](clawker_image_prune.md) - Remove unused images
* [clawker image remove](clawker_image_remove.md) - Remove one or more images

### Options

```
  -h, --help   help for image
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
