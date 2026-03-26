---
title: "clawker project"
---

## clawker project

Manage clawker projects

### Synopsis

Manage clawker projects.

This command provides project-level operations for clawker projects.
Use 'clawker project init' to set up a new project in the current directory.

### Examples

```
  # Initialize a new project
  clawker project init

  # Register an existing project
  clawker project register

  # List all registered projects
  clawker project list

  # Show project details
  clawker project info my-project

  # Remove a project from registry
  clawker project remove my-project

  # Interactively edit project configuration
  clawker project edit
```

### Subcommands

* [clawker project edit](clawker_project_edit) - Interactively edit project configuration
* [clawker project info](clawker_project_info) - Show details of a registered project
* [clawker project init](clawker_project_init) - Initialize a new project or configuration file
* [clawker project list](clawker_project_list) - List registered projects
* [clawker project register](clawker_project_register) - Register an existing clawker project in the local registry
* [clawker project remove](clawker_project_remove) - Remove projects from the registry

### Options

```
  -h, --help   help for project
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Manage Claude Code in secure Docker containers with clawker
