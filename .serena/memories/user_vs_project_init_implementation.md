# User vs Project Init Implementation Progress

## Branch: `a/user-vs-project-init`

## Goal
Separate `clawker init` into two commands:
- `clawker init` - User-level setup (creates `~/.local/clawker/settings.yaml`)
- `clawker project init` - Project-level setup (creates `clawker.yaml` in current directory)

Both commands interactive by default with `--yes`/`-y` for non-interactive mode.

## Implementation Status

### COMPLETED
1. **`pkg/cmdutil/iostreams.go`** - CREATED
   - `IOStreams` struct with `In`, `Out`, `ErrOut` streams
   - `IsInputTTY()`, `IsOutputTTY()`, `IsInteractive()` helpers
   - `NewIOStreams()` for production
   - `NewTestIOStreams()` with `TestIOStreams` for unit tests
   - `SetInteractive()` method for test control

2. **`pkg/cmdutil/prompts.go`** - ENHANCED
   - Added `Prompter` struct using `IOStreams`
   - Added `PromptConfig` struct for string prompts
   - Added `SelectOption` struct for selection prompts
   - Added methods:
     - `String(cfg PromptConfig)` - string input with default/validation
     - `Confirm(message, defaultYes)` - yes/no confirmation
     - `Select(message, options, defaultIdx)` - numbered selection
   - All methods auto-use defaults in non-interactive mode

3. **`pkg/cmdutil/factory.go`** - UPDATED
   - Added `IOStreams *IOStreams` field
   - `New()` now initializes `IOStreams: NewIOStreams()`
   - Added `Prompter()` method returning `*Prompter`

4. **`pkg/cmd/project/project.go`** - CREATED
   - Parent command for project management
   - Adds init subcommand

5. **`pkg/cmd/project/init/init.go`** - CREATED
   - Interactive project initialization
   - Prompts for: project name, base image, workspace mode
   - Creates `clawker.yaml` and `.clawkerignore`
   - Registers project in user settings
   - Flags: `--force`, `--yes`

6. **`pkg/cmd/init/init.go`** - REFACTORED
   - Now only creates user settings
   - Prompts for default image only
   - Points users to `clawker project init`
   - Flag: `--yes`

7. **`pkg/cmd/root/root.go`** - UPDATED
   - Registered project command
   - Updated Long description

8. **`pkg/cmd/init/init_test.go`** - UPDATED
   - Tests match new command behavior

9. **Documentation** - UPDATED
   - `.claude/docs/CLI-VERBS.md` - Added init and project init sections
   - `README.md` - Updated Quick Start and Commands table
   - `CLAUDE.md` - Updated CLI Commands section

## Key Files Reference
- Current init: `pkg/cmd/init/init.go` (has `NewCmdInit`, `runInit`)
- Factory: `pkg/cmdutil/factory.go` (has lazy loaders for client, config, settings)
- Settings: `internal/config/settings.go`, `settings_loader.go`
- Root: `pkg/cmd/root/root.go` (registers commands with `cmd.AddCommand()`)
- Config defaults: `internal/config/defaults.go` (`DefaultConfigYAML`, `DefaultIgnoreFile`)

## Interactive Flow Design

### `clawker init` (User Setup)
```
Setting up clawker user settings...
(Press Enter to accept defaults)

Default container image [node:20-slim]: _

Created: ~/.local/clawker/settings.yaml

Next Steps:
  1. Navigate to a project directory
  2. Run 'clawker project init' to set up the project
```

### `clawker project init` (Project Setup)
```
Setting up clawker project...
(Press Enter to accept defaults)

Project name [my-app]: _
Base image [node:20-slim]: _
Default workspace mode:
  > 1. bind (live sync)
    2. snapshot (isolated copy)
Enter selection [1]: _

Created: clawker.yaml
Created: .clawkerignore
Project: my-app

Next Steps:
  1. Review and customize clawker.yaml
  2. Run 'clawker start' to start Claude in a container
```

## Notes
- IOStreams follows GitHub CLI pattern for testability
- No external prompt library needed - built minimal prompter
- Auto-detect non-TTY: behave as if `--yes` passed (for CI)
- `project` command group ready for future `list`, `remove`, `info` subcommands
