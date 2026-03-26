---
title: "clawker project init"
---

## clawker project init

Initialize a new project or configuration file

### Synopsis

Creates a .clawker.yaml configuration file in the current directory.

When run at a project root, performs full project setup: creates config and
.clawkerignore files, and registers the project. When run inside an existing
project subdirectory, creates a layer config file that overrides the root config
for that subdirectory — skipping registration and ignore file creation.

Provides language-based presets for quick setup, plus a "Build from scratch" path
that walks through each config field step by step.

If no project name is provided, you will be prompted to enter one (or accept the
current directory name as the default).

Use --yes/-y to skip all prompts (defaults to Bare preset with GitHub HTTPS).
Combine --yes with --preset, --vcs, --git-protocol, and --no-gpg for full control.

```
clawker project init [project-name] [flags]
```

### Examples

```
  # Interactive setup with preset picker and VCS config
  clawker project init

  # Non-interactive with Bare preset defaults
  clawker project init --yes

  # Non-interactive with a specific preset and VCS
  clawker project init --yes --preset Go --vcs github
  clawker project init --yes --preset Python --vcs gitlab --git-protocol ssh

  # Non-interactive with SSH and GPG disabled
  clawker project init --yes --preset Rust --vcs github --git-protocol ssh --no-gpg

  # Overwrite existing configuration
  clawker project init --force
```

### Options

```
  -f, --force                 Overwrite existing configuration files
      --git-protocol string   Git protocol: https, ssh (requires --yes)
  -h, --help                  help for init
      --no-gpg                Disable GPG agent forwarding (requires --yes)
      --preset string         Select a language preset (requires --yes)
      --vcs string            VCS provider: github, gitlab, bitbucket (requires --yes)
  -y, --yes                   Non-interactive mode, accept all defaults
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker project](clawker_project) - Manage clawker projects
