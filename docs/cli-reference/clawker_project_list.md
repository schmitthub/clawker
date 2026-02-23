---
title: "clawker project list"
---

## clawker project list

List registered projects

### Synopsis

Lists all projects registered in the clawker project registry.

Shows the project name, root path, number of worktrees, and whether the
project directory still exists on disk.

```
clawker project list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List all registered projects
  clawker project list

  # List projects (short form)
  clawker project ls

  # List project names only
  clawker project list -q

  # Output as JSON
  clawker project list --json

  # Custom Go template
  clawker project list --format '{{.Name}} {{.Root}}'
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

* [clawker project](clawker_project) - Manage clawker projects
