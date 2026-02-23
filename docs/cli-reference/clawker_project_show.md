---
title: "clawker project show"
---

## clawker project show

Show details of a registered project

### Synopsis

Shows detailed information about a registered project, including its
name, root path, directory status, and any registered worktrees.

```
clawker project show NAME [flags]
```

### Examples

```
  # Show project details
  clawker project show my-app

  # Output as JSON
  clawker project show my-app --json
```

### Options

```
      --format string   Output format: "json", "table", or a Go template
  -h, --help            help for show
      --json            Output as JSON (shorthand for --format json)
  -q, --quiet           Only display IDs
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker project](clawker_project) - Manage clawker projects
