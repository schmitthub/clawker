---
title: "clawker init"
---

## clawker init

Initialize a new clawker project (alias for 'project init')

### Synopsis

Alias for 'clawker project init'. Initializes a new clawker project in the current
directory with language-based presets for quick setup.

See 'clawker project init --help' for full documentation.

```
clawker init [project-name] [flags]
```

### Examples

```
  # Interactive setup with preset picker
  clawker init

  # Specify project name
  clawker init my-project

  # Non-interactive with Bare preset defaults
  clawker init --yes

  # Overwrite existing configuration
  clawker init --force
```

### Options

```
  -f, --force   Overwrite existing configuration files
  -h, --help    help for init
  -y, --yes     Non-interactive mode, accept all defaults
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Manage Claude Code in secure Docker containers with clawker
