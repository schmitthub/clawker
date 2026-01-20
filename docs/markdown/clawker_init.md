## clawker init

Initialize clawker user settings

### Synopsis

Creates or updates the user settings file at ~/.local/clawker/settings.yaml.

This command sets up user-level defaults that apply across all clawker projects.
In interactive mode (default), you will be prompted to:
  - Build an initial base image (recommended)
  - Select a Linux flavor (Debian or Alpine)

Use --yes/-y to skip prompts and accept all defaults (skips base image build).

To initialize a project in the current directory, use 'clawker project init' instead.

```
clawker init [flags]
```

### Examples

```
  # Interactive setup (prompts for options)
  clawker init

  # Non-interactive with all defaults
  clawker init --yes
```

### Options

```
  -h, --help   help for init
  -y, --yes    Non-interactive mode, accept all defaults
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
