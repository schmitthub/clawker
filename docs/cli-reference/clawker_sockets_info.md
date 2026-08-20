---
title: "clawker sockets info"
---

## clawker sockets info

Show one socket bridge grant

### Synopsis

```
clawker sockets info <id> [flags]
```

### Examples

```
  # Show all stored fields for grant 7
  clawker sockets info 7

  # Output the grant as JSON
  clawker sockets info 7 --json
```

### Options

```
      --format string   Output format: "json", "table", or a Go template
  -h, --help            help for info
      --json            Output as JSON (shorthand for --format json)
  -q, --quiet           Only display IDs
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker sockets](clawker_sockets) - Manage host socket bridge grants
