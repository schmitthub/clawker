## clawker project init

Initialize a new clawker project in the current directory

### Synopsis

Creates a clawker.yaml configuration file and .clawkerignore in the current directory.

If no project name is provided, you will be prompted to enter one (or accept the
current directory name as the default).

In interactive mode (default), you will be prompted to configure:
  - Project name
  - Base container image
  - Default workspace mode (bind or snapshot)

Use --yes/-y to skip prompts and accept all defaults.

```
clawker project init [project-name] [flags]
```

### Examples

```
  # Interactive setup (prompts for options)
  clawker project init

  # Use "my-project" as project name (still prompts for other options)
  clawker project init my-project

  # Non-interactive with all defaults
  clawker project init --yes

  # Overwrite existing configuration
  clawker project init --force
```

### Options

```
  -f, --force   Overwrite existing configuration files
  -h, --help    help for init
  -y, --yes     Non-interactive mode, accept all defaults
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker project](clawker_project.md) - Manage clawker projects
