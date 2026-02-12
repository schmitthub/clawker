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

  # Initialize with a specific project name
  clawker project init my-project

  # Initialize non-interactively with defaults
  clawker project init --yes

  # Register an existing project
  clawker project register
```

### Subcommands

* [clawker project init](clawker_project_init.md) - Initialize a new clawker project in the current directory
* [clawker project register](clawker_project_register.md) - Register an existing clawker project in the local registry

### Options

```
  -h, --help   help for project
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
