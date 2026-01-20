## clawker image list

List images

### Synopsis

Lists all images created by clawker.

Images are built from project configurations and can be shared
across multiple containers.

```
clawker image list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List all clawker images
  clawker image list

  # List images (short form)
  clawker image ls

  # List image IDs only
  clawker image ls -q

  # Show all images (including intermediate)
  clawker image ls -a
```

### Options

```
  -a, --all     Show all images (default hides intermediate images)
  -h, --help    help for list
  -q, --quiet   Only display image IDs
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker image](clawker_image.md) - Manage images
