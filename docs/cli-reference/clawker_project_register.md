## clawker project register

Register an existing clawker project in the local registry

### Synopsis

Registers the project in the current directory in the local project registry
without modifying the configuration file.

If no project name is provided, you will be prompted to enter one (or accept the
current directory name as the default). Use --yes to accept the directory name
without prompting.

This is useful when a clawker.yaml was manually created, copied from another
machine, or already exists and you want to register it locally.

```
clawker project register [project-name] [flags]
```

### Examples

```
  # Register with interactive prompt for project name
  clawker project register

  # Register with a specific project name
  clawker project register my-project

  # Register using directory name without prompting
  clawker project register --yes
```

### Options

```
  -h, --help   help for register
  -y, --yes    Non-interactive mode, use directory name as project name
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker project](clawker_project.md) - Manage clawker projects
