## clawker image inspect

Display detailed information on one or more images

### Synopsis

Returns low-level information about clawker images.

Outputs detailed image information in JSON format.

```
clawker image inspect IMAGE [IMAGE...] [flags]
```

### Examples

```
  # Inspect an image
  clawker image inspect clawker-myapp:latest

  # Inspect multiple images
  clawker image inspect clawker-myapp:latest clawker-backend:latest
```

### Options

```
  -h, --help   help for inspect
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker image](clawker_image.md) - Manage images
