---
title: "clawker project remove"
---

## clawker project remove

Remove projects from the registry

### Synopsis

Removes one or more projects from the clawker project registry.

This only removes the project's registration — it does not delete any files
from disk. The project directory and clawker.yaml remain untouched.

Use 'clawker project list' to see registered project names.

```
clawker project remove NAME [NAME...] [flags]
```

### Aliases

`remove`, `rm`

### Examples

```
  # Remove a project by name
  clawker project remove my-app

  # Remove multiple projects
  clawker project rm my-app another-app

  # Remove without confirmation prompt
  clawker project remove --yes my-app
```

### Options

```
  -h, --help   help for remove
  -y, --yes    Skip confirmation prompt
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker project](clawker_project) - Manage clawker projects
