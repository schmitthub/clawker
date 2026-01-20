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
```

### Subcommands

* [clawker project init](clawker_project_init.md) - Initialize a new clawker project in the current directory

### Options

```
  -h, --help   help for project
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
