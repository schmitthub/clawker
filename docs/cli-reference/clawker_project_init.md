---
title: "clawker project init"
---

## clawker project init

Initialize a new clawker project in the current directory

### Synopsis

Creates a .clawker.yaml configuration file and .clawkerignore in the current directory.

Provides language-based presets for quick setup, plus a "Build from scratch" path
that walks through each config field step by step.

If no project name is provided, you will be prompted to enter one (or accept the
current directory name as the default).

Use --yes/-y to skip all prompts (defaults to Bare preset).
Combine --yes --preset to select a specific language preset non-interactively.

```
clawker project init [project-name] [flags]
```

### Examples

```
  # Interactive setup with preset picker
  clawker project init

  # Specify project name (still prompts for preset)
  clawker project init my-project

  # Non-interactive with Bare preset defaults
  clawker project init --yes

  # Non-interactive with a specific preset
  clawker project init --yes --preset Go
  clawker project init my-project --yes --preset Python

  # Overwrite existing configuration
  clawker project init --force
```

### Options

```
  -f, --force           Overwrite existing configuration files
  -h, --help            help for init
      --preset string   Select a language preset (requires --yes)
  -y, --yes             Non-interactive mode, accept all defaults
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker project](clawker_project) - Manage clawker projects
