# CLI Verbs Reference

> **LLM Memory Document**: This document is optimized for Claude to reference during planning. It catalogs all clawker CLI commands, their flags, and design conventions to ensure consistency across the codebase.

## Design Philosophy

Clawker follows the [CLI Guidelines](cli-guidelines.md) with these core principles:

| Principle | Implementation |
|-----------|----------------|
| **Human-First** | Conversational error messages with "Next Steps" guidance |
| **Safe Autonomy** | Destructive operations require `--force` or confirmation |
| **Composability** | stdout for data, stderr for status messages |
| **Idempotent** | `run` reattaches to existing containers |
| **Discoverability** | All commands have `Example` fields |

Clawker will mimic a subset of docker cli commands, add additional flags or functionality to a subset of docker commands, or explose its own bespoke commands that don't match docker cli.

## Command Taxonomy

```
clawker
├── Lifecycle Commands
│   ├── init          Create project configuration
│   ├── build         Build container image
│   ├── run           Build and run Claude (idempotent, aliases: start)
│   ├── stop          Stop containers
│   └── restart       Restart with fresh environment
│
├── Inspection Commands
│   ├── list          List containers
│   └── logs          Stream container logs
│
├── Cleanup Commands
│   ├── remove        Remove containers, volumes, or unused resources
│   └── prune         Alias for 'remove --unused'
│
├── Configuration Commands
│   └── config
│       └── check     Validate clawker.yaml
│
├── Container Commands (Docker CLI mimicry)
│   └── container
│       ├── list      List containers (aliases: ls, ps)
│       ├── inspect   Display detailed container info
│       ├── logs      Fetch container logs
│       ├── start     Start stopped containers
│       ├── stop      Stop running containers
│       ├── kill      Kill containers with signal
│       ├── pause     Pause running containers
│       ├── unpause   Unpause paused containers
│       └── remove    Remove containers (aliases: rm)
│
├── Observability Commands
│   └── monitor
│       ├── init      Scaffold monitoring config
│       ├── up        Start monitoring stack
│       ├── down      Stop monitoring stack
│       └── status    Show stack status
│
└── Development Commands
    └── generate      Generate Dockerfiles from npm
```

---

## Global Flags

These flags are available on all commands via `PersistentFlags()`:

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-d` | `--debug` | bool | `false` | Enable debug logging (zerolog) |
| `-w` | `--workdir` | string | cwd | Working directory for config lookup |
| | `--version` | | | Print version and exit |
| `-h` | `--help` | | | Show help text |

---

## Command Reference

### `init`

**Category:** Lifecycle
**File:** `pkg/cmd/init/init.go`

```
clawker init [project-name]
```

Creates `clawker.yaml` and `.clawkerignore` in the current directory.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-f` | `--force` | bool | `false` | Overwrite existing configuration files |

**Examples:**

```bash
# Use current directory name as project
clawker init

# Use "my-project" as project name
clawker init my-project

# Overwrite existing configuration
clawker init --force
```

---

### `build`

**Category:** Lifecycle
**File:** `pkg/cmd/build/build.go`

```
clawker build
```

Builds the container image for this project. Always builds unconditionally.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| | `--no-cache` | bool | `false` | Build image without Docker cache |
| | `--dockerfile` | string | `""` | Path to custom Dockerfile (overrides config) |

**Examples:**

```bash
# Build image (uses Docker cache)
clawker build

# Build image without cache
clawker build --no-cache

# Build using custom Dockerfile
clawker build --dockerfile ./Dockerfile.dev
```

---

### `run`

**Category:** Lifecycle
**File:** `pkg/cmd/run/run.go`
**Aliases:** `start`

```
clawker run [flags] [-- <command>...]
```

Builds the image (if needed), creates volumes, and runs Claude. **Idempotent**: reattaches to existing containers. Containers are preserved by default; use `--remove` for ephemeral containers.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-m` | `--mode` | string | config | Workspace mode: `bind` or `snapshot` |
| | `--build` | bool | `false` | Force rebuild of the container image |
| | `--shell` | bool | `false` | Run shell instead of claude |
| `-s` | `--shell-path` | string | config/bash | Path to shell executable |
| `-u` | `--user` | string | claude | User to run shell as (only with --shell) |
| `-r` | `--remove` | bool | `false` | Remove container and volumes on exit (ephemeral) |
| | `--detach` | bool | `false` | Run container in background |
| | `--clean` | bool | `false` | Remove existing container and volumes before starting |
| | `--agent` | string | random | Agent name for the container |
| `-p` | `--publish` | []string | `nil` | Publish container port(s) to host |

**Shell Path Resolution:**
The shell path is resolved using Viper configuration hierarchy:

1. CLI flag `-s, --shell-path` (highest priority)
2. `CLAWKER_AGENT_SHELL` environment variable
3. `agent.shell` in clawker.yaml
4. Default: `/bin/sh`

**Examples:**

```bash
# Run Claude interactively (container preserved after exit)
clawker run

# Using 'start' alias (same behavior)
clawker start

# Run Claude with a prompt
clawker run -- -p "build a feature"

# Resume previous session
clawker run -- --resume

# Run in snapshot mode
clawker run --mode=snapshot

# Run in background
clawker run --detach

# Run ephemeral container (removed on exit)
clawker run --remove

# Open a shell session
clawker run --shell

# Open shell with specific shell and user
clawker run --shell -s /bin/zsh -u root

# Publish ports to access services
clawker run -p 8080:8080
clawker run -p 24282:24282
```

**Gotcha:** The `-p` flag conflicts with `ls -p` (project filter). See [Known Issues](#known-issues).

---

### `stop`

**Category:** Lifecycle
**File:** `pkg/cmd/stop/stop.go`

```
clawker stop
```

Stops Claude containers for this project. Volumes are preserved unless `--clean` is used.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| | `--agent` | string | all | Agent name to stop (default: all agents) |
| | `--clean` | bool | `false` | Remove all volumes (workspace, config, history) |
| `-f` | `--force` | bool | `false` | Force stop (SIGKILL) |
| `-t` | `--timeout` | int | `10` | Timeout in seconds before force kill |

**Examples:**

```bash
# Stop all containers for this project
clawker stop

# Stop only the 'ralph' agent
clawker stop --agent ralph

# Stop and remove all volumes
clawker stop --clean
```

---

### `restart`

**Category:** Lifecycle
**File:** `pkg/cmd/restart/restart.go`

```
clawker restart
```

Restarts Claude containers to pick up environment changes. Volumes are preserved.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| | `--agent` | string | all | Agent name to restart (default: all agents) |
| `-t` | `--timeout` | int | `10` | Timeout in seconds before force kill |

**Examples:**

```bash
# Restart all containers for project
clawker restart

# Restart specific agent
clawker restart --agent ralph
```

---

### `list`

**Category:** Inspection
**File:** `pkg/cmd/list/list.go`
**Aliases:** `ls`, `ps`

```
clawker list
```

Lists all containers created by clawker.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-a` | `--all` | bool | `false` | Show all containers (including stopped) |
| `-p` | `--project` | string | `""` | Filter by project name |

**Examples:**

```bash
# List running containers
clawker list

# List all containers (including stopped)
clawker list -a

# List containers for a specific project
clawker list -p myproject
```

**Note:** Output goes to stdout (table format) for scripting compatibility.

---

### `logs`

**Category:** Inspection
**File:** `pkg/cmd/logs/logs.go`

```
clawker logs
```

Shows logs from a Claude container.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| | `--agent` | string | `""` | Agent name (required if multiple containers) |
| `-f` | `--follow` | bool | `false` | Follow log output (like tail -f) |
| | `--tail` | string | `"100"` | Number of lines to show (or `"all"`) |

**Examples:**

```bash
# Show logs (if single container)
clawker logs

# Show logs for specific agent
clawker logs --agent ralph

# Follow log output
clawker logs -f

# Show last 50 lines
clawker logs --tail 50
```

---

### `remove`

**Category:** Cleanup
**File:** `pkg/cmd/remove/remove.go`
**Aliases:** `rm`

```
clawker remove
```

Removes clawker containers and their associated resources. Supports three modes: by name, by project, or unused resources.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-n` | `--name` | string | `""` | Container name to remove |
| `-p` | `--project` | string | `""` | Remove all containers for a project |
| `-u` | `--unused` | bool | `false` | Remove unused resources (prune mode) |
| `-a` | `--all` | bool | `false` | With --unused, also remove volumes and all images |
| `-f` | `--force` | bool | `false` | Force remove or skip confirmation |

**Validation:** `cmd.MarkFlagsOneRequired("name", "project", "unused")`

**Examples:**

```bash
# Remove a specific container
clawker remove -n clawker.myapp.ralph

# Remove all containers for a project
clawker remove -p myapp

# Force remove running containers
clawker remove -p myapp -f

# Remove unused resources (stopped containers, dangling images)
clawker remove --unused

# Remove ALL clawker resources (including volumes)
clawker remove --unused --all

# Skip confirmation prompt
clawker remove --unused --all --force
```

**Gotcha:** Uses `-n/--name` instead of `--agent`. See [Known Issues](#known-issues).

---

### `prune`

**Category:** Cleanup
**File:** `pkg/cmd/prune/prune.go`

```
clawker prune
```

Alias for `clawker remove --unused`. Removes unused clawker resources. With `--all`, removes ALL resources including volumes.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-a` | `--all` | bool | `false` | Remove ALL clawker resources (including volumes) |
| `-f` | `--force` | bool | `false` | Skip confirmation prompt |

**Examples:**

```bash
# Remove unused resources (stopped containers, dangling images)
clawker prune

# Remove ALL clawker resources (including volumes)
clawker prune --all

# Skip confirmation prompt
clawker prune --all --force
```

**Note:** `prune --all` prompts for confirmation unless `--force` is used.

---

### `config check`

**Category:** Configuration
**File:** `pkg/cmd/config/config.go`

```
clawker config check
```

Validates the `clawker.yaml` configuration file.

No additional flags.

**Examples:**

```bash
# Validate configuration in current directory
clawker config check
```

---

### `monitor init`

**Category:** Observability
**File:** `pkg/cmd/monitor/init.go`

```
clawker monitor init
```

Scaffolds monitoring stack configuration files in `~/.clawker/monitor/`.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-f` | `--force` | bool | `false` | Overwrite existing configuration files |

**Examples:**

```bash
# Initialize monitoring configuration
clawker monitor init

# Overwrite existing configuration
clawker monitor init --force
```

---

### `monitor up`

**Category:** Observability
**File:** `pkg/cmd/monitor/up.go`

```
clawker monitor up
```

Starts the monitoring stack using Docker Compose.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| | `--detach` | bool | `true` | Run in detached mode |

**Examples:**

```bash
# Start the monitoring stack (detached)
clawker monitor up

# Start in foreground (see logs)
clawker monitor up --detach=false
```

---

### `monitor down`

**Category:** Observability
**File:** `pkg/cmd/monitor/down.go`

```
clawker monitor down
```

Stops the monitoring stack.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-v` | `--volumes` | bool | `false` | Remove named volumes from compose.yaml |

**Examples:**

```bash
# Stop the monitoring stack
clawker monitor down

# Stop and remove volumes
clawker monitor down --volumes
```

---

### `monitor status`

**Category:** Observability
**File:** `pkg/cmd/monitor/status.go`

```
clawker monitor status
```

Shows the current status of the monitoring stack containers.

No additional flags.

**Examples:**

```bash
# Check monitoring stack status
clawker monitor status
```

---

### `generate`

**Category:** Development
**File:** `pkg/cmd/generate/generate.go`

```
clawker generate [versions...]
```

Fetches Claude Code versions from npm and generates Dockerfiles.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| | `--skip-fetch` | bool | `false` | Skip npm fetch, use existing versions.json |
| | `--cleanup` | bool | `true` | Remove obsolete version directories |
| `-o` | `--output` | string | `""` | Output directory for generated files |

**Examples:**

```bash
# Generate Dockerfiles for latest version
clawker generate latest

# Generate for multiple versions
clawker generate latest 2.1

# Output to specific directory
clawker generate --output ./build latest

# Show existing versions.json
clawker generate
```

---

## Container Commands

The `clawker container` command group provides Docker CLI-compatible subcommands for direct container management. These commands operate on containers by name (positional arguments) rather than by project/agent flags.

**Design Pattern:** These commands mimic the Docker CLI interface (`docker container ls`, `docker container rm`, etc.) for users familiar with Docker workflows.

### `container list`

**File:** `pkg/cmd/container/list.go`
**Aliases:** `ls`, `ps`

```
clawker container list [flags]
```

Lists clawker-managed containers.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-a` | `--all` | bool | `false` | Show all containers (including stopped) |
| `-p` | `--project` | string | `""` | Filter by project name |

**Examples:**

```bash
# List running containers
clawker container list

# List all containers (including stopped)
clawker container ls -a

# Filter by project
clawker container ps -p myproject
```

---

### `container inspect`

**File:** `pkg/cmd/container/inspect.go`

```
clawker container inspect CONTAINER [CONTAINER...]
```

Displays detailed information about containers in JSON format.

No additional flags.

**Examples:**

```bash
# Inspect a single container
clawker container inspect clawker.myapp.ralph

# Inspect multiple containers
clawker container inspect clawker.myapp.ralph clawker.myapp.writer
```

---

### `container logs`

**File:** `pkg/cmd/container/logs.go`

```
clawker container logs [flags] CONTAINER
```

Fetches logs from a container.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-f` | `--follow` | bool | `false` | Follow log output |
| | `--tail` | string | `"all"` | Number of lines to show from end |
| `-t` | `--timestamps` | bool | `false` | Show timestamps |
| | `--since` | string | `""` | Show logs since timestamp (e.g., 2024-01-01T00:00:00Z) or relative (e.g., 1h) |
| | `--until` | string | `""` | Show logs before timestamp |

**Examples:**

```bash
# Show all logs
clawker container logs clawker.myapp.ralph

# Follow logs in real-time
clawker container logs -f clawker.myapp.ralph

# Show last 100 lines with timestamps
clawker container logs --tail 100 -t clawker.myapp.ralph

# Show logs from the last hour
clawker container logs --since 1h clawker.myapp.ralph
```

---

### `container start`

**File:** `pkg/cmd/container/start.go`

```
clawker container start CONTAINER [CONTAINER...]
```

Starts one or more stopped containers.

No additional flags.

**Examples:**

```bash
# Start a single container
clawker container start clawker.myapp.ralph

# Start multiple containers
clawker container start clawker.myapp.ralph clawker.myapp.writer
```

---

### `container stop`

**File:** `pkg/cmd/container/stop.go`

```
clawker container stop [flags] CONTAINER [CONTAINER...]
```

Stops one or more running containers.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-t` | `--time` | int | `10` | Seconds to wait before killing |
| `-s` | `--signal` | string | `""` | Signal to send (default: SIGTERM, then SIGKILL) |

**Examples:**

```bash
# Stop a container (graceful, 10s timeout)
clawker container stop clawker.myapp.ralph

# Stop with custom timeout
clawker container stop -t 30 clawker.myapp.ralph

# Stop multiple containers
clawker container stop clawker.myapp.ralph clawker.myapp.writer
```

---

### `container kill`

**File:** `pkg/cmd/container/kill.go`

```
clawker container kill [flags] CONTAINER [CONTAINER...]
```

Kills one or more containers by sending a signal (default: SIGKILL).

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-s` | `--signal` | string | `SIGKILL` | Signal to send to the container |

**Examples:**

```bash
# Kill a container (SIGKILL)
clawker container kill clawker.myapp.ralph

# Kill with SIGTERM
clawker container kill -s SIGTERM clawker.myapp.ralph

# Kill multiple containers
clawker container kill clawker.myapp.ralph clawker.myapp.writer
```

---

### `container pause`

**File:** `pkg/cmd/container/pause.go`

```
clawker container pause CONTAINER [CONTAINER...]
```

Pauses all processes within one or more containers.

No additional flags.

**Examples:**

```bash
# Pause a container
clawker container pause clawker.myapp.ralph

# Pause multiple containers
clawker container pause clawker.myapp.ralph clawker.myapp.writer
```

---

### `container unpause`

**File:** `pkg/cmd/container/pause.go`

```
clawker container unpause CONTAINER [CONTAINER...]
```

Unpauses all processes within one or more paused containers.

No additional flags.

**Examples:**

```bash
# Unpause a container
clawker container unpause clawker.myapp.ralph

# Unpause multiple containers
clawker container unpause clawker.myapp.ralph clawker.myapp.writer
```

---

### `container remove`

**File:** `pkg/cmd/container/remove.go`
**Aliases:** `rm`

```
clawker container remove [flags] CONTAINER [CONTAINER...]
```

Removes one or more containers.

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-f` | `--force` | bool | `false` | Force remove running containers |
| `-v` | `--volumes` | bool | `false` | Remove anonymous volumes associated with container |

**Examples:**

```bash
# Remove a stopped container
clawker container rm clawker.myapp.ralph

# Force remove a running container
clawker container rm -f clawker.myapp.ralph

# Remove container and its volumes
clawker container rm -v clawker.myapp.ralph

# Remove multiple containers
clawker container rm clawker.myapp.ralph clawker.myapp.writer
```

---

## Flag Conventions

### Standard Flags

| Flag | Usage | Commands |
|------|-------|----------|
| `-d, --debug` | Enable debug logging | Global |
| `-f, --force` | Skip confirmation or overwrite | `init`, `stop`, `rm`, `prune`, `monitor init` |
| `-a, --all` | Expand scope (include stopped/all) | `ls`, `rm --unused`, `prune` |
| `-r, --remove` | Remove resources on exit | `run` |
| `-u, --unused` | Target unused resources | `rm` |
| `--agent` | Target specific agent container | `run`, `stop`, `restart`, `logs` |
| `-p, --project` | Filter by project name | `ls`, `rm` |
| `-t, --timeout` | Timeout in seconds | `stop`, `restart` |
| `-m, --mode` | Workspace mode (bind/snapshot) | `run` |
| `-s, --shell-path` | Shell executable path | `run --shell` |
| `-u, --user` | User to run as | `run --shell` |

### Flag Naming Patterns

1. **Boolean flags**: Use affirmative names (`--force`, `--clean`, `--detach`)
2. **Negation**: Use `--no-` prefix (`--no-cache`, `--no-input`)
3. **Short flags**: Reserve for common operations (don't pollute namespace)
4. **Long flags**: Always provide for clarity in scripts

---

## UX Patterns

### Output Routing

```go
// Status messages → stderr
fmt.Fprintln(os.Stderr, "Starting container...")
fmt.Fprintf(os.Stderr, "Container %s started\n", name)

// Data output → stdout (e.g., ls command table)
fmt.Println(tableOutput)
```

### Error Handling

```go
// Docker errors (rich formatting with Next Steps)
if err != nil {
    cmdutil.HandleError(err)
    return err
}

// Config not found
if config.IsConfigNotFound(err) {
    cmdutil.PrintError("No clawker.yaml found in current directory")
    cmdutil.PrintNextSteps(
        "Run 'clawker init' to create a configuration",
        "Or change to a directory with clawker.yaml",
    )
    return err
}
```

### Help Text Format

```go
cmd := &cobra.Command{
    Use:   "command",
    Short: "One-line description",
    Long: `Detailed description with context.

Additional paragraphs as needed.`,
    Example: `  # Basic usage
  clawker command

  # With flags
  clawker command --flag value`,
    RunE: func(cmd *cobra.Command, args []string) error { ... },
}
```

---

## Known Issues

### Issue 1: `-p` Flag Conflict

**Problem:** `-p` means different things in different commands:

- `ls -p` → `--project` (filter by project)
- `run -p` → `--publish` (port mapping)

**Recommendation:** Change publish to `-P` (uppercase) or use `--port` long form only.

### Issue 2: `--agent` vs `-n/--name` Inconsistency

**Problem:** Different container targeting patterns:

- `stop`, `restart`, `logs` use `--agent`
- `rm` uses `-n/--name` (expects full container name)

**Recommendation:** Add `--agent` to `rm`, deprecate `-n/--name`.

### Issue 3: Missing Standard Flags

**Problem:** Missing common CLI flags:

- No `--json` output for scripting (`ls`, `config check`)
- No `--quiet/-q` for silent operation
- No `--dry-run` for preview

**Recommendation:** Add these flags incrementally.

### Issue 4: Missing Confirmation

**Problem:** `rm -p` removes all containers in a project without confirmation.

**Recommendation:** Add confirmation prompt for `rm -p` (like `prune --all`).

---

## New Command Checklist

Before adding a new command, verify:

```
□ Has Example field with 2+ examples
□ Uses PersistentPreRunE (not PersistentPreRun)
□ Routes status messages to stderr (fmt.Fprintf(os.Stderr, ...))
□ Uses cmdutil.HandleError(err) for Docker errors
□ Uses cmdutil.PrintNextSteps() for guidance
□ Registered in pkg/cmd/root/root.go
□ Updates README.md with user-facing docs
□ Uses standard flag names from Flag Conventions
□ Validates input early (before state changes)
□ Has tests in *_test.go file
□ Handles Ctrl+C gracefully (term.SetupSignalContext)
```

### Command Template

```go
package mycommand

import (
    "fmt"
    "os"

    "github.com/schmitthub/clawker/pkg/cmdutil"
    "github.com/spf13/cobra"
)

type Options struct {
    Force bool
}

func NewCmdMyCommand(f *cmdutil.Factory) *cobra.Command {
    opts := &Options{}

    cmd := &cobra.Command{
        Use:   "mycommand",
        Short: "One-line description",
        Long: `Detailed description.

Additional context here.`,
        Example: `  # Basic usage
  clawker mycommand

  # With flags
  clawker mycommand --force`,
        RunE: func(cmd *cobra.Command, args []string) error {
            return runMyCommand(f, opts)
        },
    }

    cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force the operation")

    return cmd
}

func runMyCommand(f *cmdutil.Factory, opts *Options) error {
    // Implementation
    fmt.Fprintln(os.Stderr, "Operation complete")
    return nil
}
```

---

## Aliases

| Alias | Canonical Command |
|-------|-------------------|
| `start` | `run` |
| `ls` | `list` |
| `ps` | `list` |
| `rm` | `remove` |
| `prune` | `remove --unused` |
| `container ls` | `container list` |
| `container ps` | `container list` |
| `container rm` | `container remove` |

---

## Version History

- **v1.2**: Docker CLI mimicry - Container commands (2026-01)
  - Added `clawker container` parent command
  - Added 9 subcommands: `list`, `inspect`, `logs`, `start`, `stop`, `kill`, `pause`, `unpause`, `remove`
  - Subcommands use positional arguments for container names (Docker-compatible interface)
  - Commands use `internal/docker.Client` which wraps `pkg/whail.Engine`
- **v1.1**: CLI verb consolidation (2026-01)
  - Merged `start` into `run` (start is now alias)
  - Removed standalone `shell` command (use `run --shell`)
  - Added `--unused` to `remove` (prune is now alias)
  - Changed `run` default: preserves containers (use `--remove` for ephemeral)
- **v1.0**: Initial CLI verbs reference (2025-01)
