---
title: "clawker sockets list"
---

## clawker sockets list

List stored socket bridge grants

### Synopsis

```
clawker sockets list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List all approvals and denials
  clawker sockets list

  # Output summaries as JSON
  clawker sockets list --json
```

### Options

```
      --format string   Output format: "json", "table", or a Go template
  -h, --help            help for list
      --json            Output as JSON (shorthand for --format json)
  -q, --quiet           Only display IDs
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker sockets](clawker_sockets) - Manage host socket bridge grants
