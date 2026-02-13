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

  # Output as JSON
  clawker image ls --json

  # Custom Go template
  clawker image ls --format '{{.ID}} {{.Size}}'

  # Filter by reference
  clawker image ls --filter reference=myapp*
```

### Options

```
  -a, --all                  Show all images (default hides intermediate images)
      --filter stringArray   Filter output (key=value, repeatable)
      --format string        Output format: "json", "table", or a Go template
  -h, --help                 help for list
      --json                 Output as JSON (shorthand for --format json)
  -q, --quiet                Only display IDs
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker image](clawker_image.md) - Manage images
