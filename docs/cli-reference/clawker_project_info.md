---
title: "clawker project info"
---

## clawker project info

Show details of a registered project

### Synopsis

Shows detailed information about a registered project, including its
name, root path, directory status, and any registered worktrees with
their health status.

```
clawker project info NAME [flags]
```

### Examples

```
  # Show project details
  clawker project info my-app

  # Output as JSON
  clawker project info my-app --json
```

### Options

```
  -h, --help   help for info
      --json   Output as JSON
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker project](clawker_project) - Manage clawker projects
