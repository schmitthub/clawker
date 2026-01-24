# CLI Command Reference

> Developer reference for clawker CLI commands and their flags.

## Top-Level Commands

### `clawker build`

Build a container image from a clawker project.

**Usage:**
```bash
clawker build [flags]
```

**Aliases:** Also available as `clawker image build`

**Flags:**

| Flag | Shorthand | Type | Default | Description |
|------|-----------|------|---------|-------------|
| `--file` | `-f` | string | | Path to Dockerfile (overrides build.dockerfile in config) |
| `--tag` | `-t` | stringArray | | Name and optionally a tag (format: name:tag) |
| `--no-cache` | | bool | false | Do not use cache when building the image |
| `--pull` | | bool | false | Always attempt to pull a newer version of the base image |
| `--build-arg` | | stringArray | | Set build-time variables (format: KEY=VALUE or KEY to pass through from environment) |
| `--label` | | stringArray | | Set metadata for the image (format: KEY=VALUE) |
| `--target` | | string | | Set the target build stage to build |
| `--quiet` | `-q` | bool | false | Suppress the build output |
| `--progress` | | string | auto | Set type of progress output (currently only `none` suppresses output; `auto`, `plain`, `tty` produce default output) |
| `--network` | | string | | Set the networking mode for the RUN instructions during build |

**Examples:**
```bash
# Build the project image
clawker build

# Build without Docker cache
clawker build --no-cache

# Build using a custom Dockerfile
clawker build -f ./Dockerfile.dev

# Build with multiple tags
clawker build -t myapp:latest -t myapp:v1.0

# Build with build arguments
clawker build --build-arg NODE_VERSION=20

# Build a specific target stage
clawker build --target builder

# Build quietly (suppress output)
clawker build -q

# Always pull base image
clawker build --pull

# Build with custom labels
clawker build --label version=1.0 --label team=backend
```

**Notes:**
- User-provided `--label` flags are merged with clawker's managed labels
- Clawker labels (`com.clawker.*`) take precedence over user labels
- Without `-f/--file`, builds from generated Dockerfile based on clawker.yaml

---

### `clawker init`

Initialize clawker user settings.

**Usage:**
```bash
clawker init [flags]
```

Creates or updates the user settings file at `~/.local/clawker/settings.yaml`.
This sets up user-level defaults that apply across all clawker projects.

In interactive mode (default), you will be prompted to:
- Build an initial base image (recommended)
- Select a Linux flavor (Debian or Alpine)

If you choose to build a base image, clawker will:
1. Fetch the latest Claude Code version from npm
2. Generate a Dockerfile for your selected flavor
3. Build the image as `clawker-default:latest`
4. Set this as your default image in settings

**Flags:**

| Flag | Shorthand | Type | Default | Description |
|------|-----------|------|---------|-------------|
| `--yes` | `-y` | bool | false | Non-interactive mode, accept all defaults (skips base image build) |

**Examples:**
```bash
# Interactive setup (prompts for options)
clawker init

# Non-interactive with all defaults (skips base image build)
clawker init --yes
```

**Linux Flavors:**

| Flavor | Description |
|--------|-------------|
| `bookworm` | Debian stable (Recommended) |
| `trixie` | Debian testing |
| `alpine3.22` | Alpine Linux 3.22 |
| `alpine3.23` | Alpine Linux 3.23 |

**Note:** To initialize a project, use `clawker project init` instead.

---

### `clawker project init`

Initialize a new clawker project in the current directory.

**Usage:**
```bash
clawker project init [project-name] [flags]
```

Creates `clawker.yaml` and `.clawkerignore` in the current directory.
If no project name is provided, you will be prompted to enter one.

In interactive mode (default), you will be prompted to configure:
- Project name
- Base container image (uses default from `clawker init` if available)
- Default workspace mode (bind or snapshot)

**Flags:**

| Flag | Shorthand | Type | Default | Description |
|------|-----------|------|---------|-------------|
| `--force` | `-f` | bool | false | Overwrite existing configuration files |
| `--yes` | `-y` | bool | false | Non-interactive mode, accept all defaults |

**Examples:**
```bash
# Interactive setup (prompts for options)
clawker project init

# Use "my-project" as project name (still prompts for other options)
clawker project init my-project

# Non-interactive with all defaults (requires default image from 'clawker init')
clawker project init --yes

# Overwrite existing configuration
clawker project init --force
```

**Note:** When using `--yes`, a default image must be configured via `clawker init`.
If no default image exists, the command will fail with a helpful error message.

---

### `clawker run`

Build and run a container with Claude Code. Alias for `clawker container run`.

**Usage:**
```bash
clawker run [flags]
```

---

### `clawker start`

Start an existing container. Alias for `clawker container start`.

**Usage:**
```bash
clawker start [flags]
```

---

## Autonomous Loops (`clawker ralph`)

Run Claude Code in autonomous loops using the "Ralph Wiggum" technique.

### `clawker ralph run`

Start an autonomous Claude Code loop.

**Usage:**
```bash
clawker ralph run --agent NAME [flags]
```

Runs Claude Code repeatedly with `--continue` until completion or stagnation.
The agent must output a RALPH_STATUS block for progress tracking.

**Flags:**

| Flag | Shorthand | Type | Default | Description |
|------|-----------|------|---------|-------------|
| `--agent` | | string | | Agent name (required) |
| `--prompt` | `-p` | string | | Initial prompt for the first loop |
| `--prompt-file` | | string | | File containing the initial prompt |
| `--max-loops` | | int | 50 | Maximum number of loops |
| `--stagnation-threshold` | | int | 3 | Loops without progress before circuit trips |
| `--timeout` | | duration | 15m | Timeout per loop iteration |
| `--reset-circuit` | | bool | false | Reset circuit breaker before starting |
| `--quiet` | `-q` | bool | false | Suppress progress output |
| `--json` | | bool | false | Output result as JSON |
| `--calls` | | int | 100 | Rate limit: max calls per hour (0 to disable) |
| `--monitor` | | bool | false | Enable live monitoring output |
| `--verbose` | `-v` | bool | false | Enable verbose output |
| `--strict-completion` | | bool | false | Require both EXIT_SIGNAL and completion indicators |
| `--same-error-threshold` | | int | 5 | Same error repetitions before circuit trips |
| `--output-decline-threshold` | | int | 70 | Output decline percentage that triggers trip |
| `--max-test-loops` | | int | 3 | Consecutive test-only loops before circuit trips |
| `--loop-delay` | | int | 3 | Seconds to wait between loop iterations |
| `--skip-permissions` | | bool | false | Pass --dangerously-skip-permissions to claude |

**Examples:**
```bash
# Start with an initial prompt
clawker ralph run --agent dev --prompt "Fix all failing tests"

# Start from a prompt file
clawker ralph run --agent dev --prompt-file task.md

# Continue an existing session
clawker ralph run --agent dev

# Reset circuit breaker and retry
clawker ralph run --agent dev --reset-circuit

# Run with custom limits
clawker ralph run --agent dev --max-loops 100 --stagnation-threshold 5

# Run with live monitoring
clawker ralph run --agent dev --monitor

# Run with rate limiting (5 calls per hour)
clawker ralph run --agent dev --calls 5

# Run with verbose output
clawker ralph run --agent dev -v

# Run in YOLO mode (skip all permission prompts)
clawker ralph run --agent dev --skip-permissions
```

**Exit conditions:**
- Claude signals `EXIT_SIGNAL: true` with sufficient completion indicators (strict mode)
- Claude signals `EXIT_SIGNAL: true` or `STATUS: COMPLETE` (default mode)
- Circuit breaker trips (no progress, same error, output decline, or test loops)
- Maximum loops reached
- Error during execution
- Claude's API rate limit hit

---

### `clawker ralph status`

Show current ralph session status.

**Usage:**
```bash
clawker ralph status --agent NAME [flags]
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--agent` | string | | Agent name (required) |
| `--json` | bool | false | Output as JSON |

**Examples:**
```bash
# Show status
clawker ralph status --agent dev

# Output as JSON
clawker ralph status --agent dev --json
```

---

### `clawker ralph reset`

Reset the circuit breaker for an agent.

**Usage:**
```bash
clawker ralph reset --agent NAME [flags]
```

**Flags:**

| Flag | Shorthand | Type | Default | Description |
|------|-----------|------|---------|-------------|
| `--agent` | | string | | Agent name (required) |
| `--all` | | bool | false | Also clear session history |
| `--quiet` | `-q` | bool | false | Suppress output |

**Examples:**
```bash
# Reset circuit breaker only
clawker ralph reset --agent dev

# Reset everything (circuit and session)
clawker ralph reset --agent dev --all
```

---

### RALPH_STATUS Block Format

Claude must output this block for progress tracking:

```
---RALPH_STATUS---
STATUS: IN_PROGRESS | COMPLETE | BLOCKED
TASKS_COMPLETED_THIS_LOOP: <number>
FILES_MODIFIED: <number>
TESTS_STATUS: PASSING | FAILING | NOT_RUN
WORK_TYPE: IMPLEMENTATION | TESTING | DOCUMENTATION | REFACTORING
EXIT_SIGNAL: false | true
RECOMMENDATION: <one line>
---END_RALPH_STATUS---
```

Add instructions to your project's CLAUDE.md to have the agent output this block.

---

## Management Commands

### Container Management (`clawker container`)

| Command | Description |
|---------|-------------|
| `list`, `ls`, `ps` | List containers |
| `run` | Build and run a new container |
| `create` | Create a new container |
| `start` | Start a stopped container |
| `stop` | Stop a running container |
| `restart` | Restart a container |
| `kill` | Kill a running container |
| `pause` | Pause a container |
| `unpause` | Unpause a container |
| `remove`, `rm` | Remove a container |
| `logs` | View container logs |
| `inspect` | Display detailed container information |
| `top` | Display running processes |
| `stats` | Display container resource usage |
| `exec` | Execute a command in a container |
| `attach` | Attach to a running container |
| `cp` | Copy files between container and host |
| `rename` | Rename a container |
| `wait` | Wait for container to stop |
| `update` | Update container configuration |

### Image Management (`clawker image`)

| Command | Description |
|---------|-------------|
| `list`, `ls` | List images |
| `build` | Build an image (same as `clawker build`) |
| `inspect` | Display detailed image information |
| `remove`, `rm` | Remove an image |
| `prune` | Remove unused images |

### Volume Management (`clawker volume`)

| Command | Description |
|---------|-------------|
| `list`, `ls` | List volumes |
| `create` | Create a volume |
| `inspect` | Display detailed volume information |
| `remove`, `rm` | Remove a volume |
| `prune` | Remove unused volumes |

### Network Management (`clawker network`)

| Command | Description |
|---------|-------------|
| `list`, `ls` | List networks |
| `create` | Create a network |
| `inspect` | Display detailed network information |
| `remove`, `rm` | Remove a network |
| `prune` | Remove unused networks |

---

## Flag Conventions

Standard flag names used across commands. Note that shorthand meanings are context-dependent:

| Flag | Shorthand | Description |
|------|-----------|-------------|
| `--help` | `-h` | Display help for command |
| `--quiet` | `-q` | Suppress output |
| `--force` | `-f` | Force operation (in `remove`, `prune` commands) |
| `--file` | `-f` | Dockerfile path (in `build` commands) |
| `--format` | `-f` | Go template format string (in `inspect` commands) |
| `--all` | `-a` | Include all resources |
| `--agent` | | Agent name shortcut for container commands |
| `--mode` | | Workspace mode: 'bind' (live sync) or 'snapshot' (isolated copy) |
| `--debug` | `-D` | Enable debug logging |

**Note:** The `-f` shorthand has different meanings depending on context:
- In build commands: `-f` means `--file` (Dockerfile path) - matches Docker CLI convention
- In remove/prune commands: `-f` means `--force`
- In inspect commands: `-f` means `--format` (Go template)

---

## The `--agent` Flag

Most container commands support the `--agent` flag as a convenient shortcut for specifying containers by agent name instead of the full container name.

**How it works:**

When `--agent` is provided, the container name is resolved as `clawker.<project>.<agent>` using the project name from your `clawker.yaml` configuration.

**Supported commands:**

| Command | Notes |
|---------|-------|
| `start` | Start container by agent name |
| `stop` | Stop container by agent name |
| `kill` | Kill container by agent name |
| `restart` | Restart container by agent name |
| `logs` | View logs by agent name |
| `exec` | Execute command in container by agent name |
| `attach` | Attach to container by agent name |
| `inspect` | Inspect container by agent name |
| `pause` | Pause container by agent name |
| `unpause` | Unpause container by agent name |
| `remove` | Remove container by agent name |
| `wait` | Wait for container by agent name |
| `update` | Update container by agent name |
| `top` | View processes by agent name |
| `stats` | View stats by agent name |
| `rename` | With --agent, only NEW_NAME is required |
| `cp` | `:PATH` uses --agent flag; `name:PATH` resolves name as agent |

**Examples:**

```bash
# Instead of:
clawker container stop clawker.myproject.ralph

# You can use:
clawker container stop --agent ralph

# View logs
clawker container logs --agent ralph --follow

# Copy files: :PATH uses the --agent flag value
clawker container cp --agent ralph :/app/config.json ./config.json

# Copy files: name:PATH resolves name as agent (overrides --agent)
clawker container cp --agent ralph writer:/app/output.txt ./output.txt

# Rename (only NEW_NAME required with --agent)
clawker container rename --agent ralph clawker.myproject.newname
```

**Mutual exclusivity:**

The `--agent` flag and positional container arguments are mutually exclusive. You cannot use both together.

**Special case for `cp` command:**

When using `--agent` with `cp`, there are two path syntaxes:
- `:PATH` - uses the agent from the `--agent` flag value
- `name:PATH` - resolves `name` as an agent (overrides `--agent` flag)

This allows copying files from different agents in a single command while still benefiting from the agent name resolution.

---

## The `--mode` Flag

The `container run` and `container create` commands support the `--mode` flag to control how the workspace is mounted into the container.

**Available modes:**

| Mode | Description |
|------|-------------|
| `bind` | Live sync - Host files are bind-mounted into the container. Changes in the container immediately affect the host filesystem. This is the default. |
| `snapshot` | Isolated copy - A snapshot of the workspace is created as a Docker volume. Changes in the container do not affect the host filesystem. Use git to push/pull changes. |

**Behavior:**

- If `--mode` is not specified, the default from `workspace.default_mode` in `clawker.yaml` is used
- If no config default is set, `bind` mode is used
- The mode determines which workspace strategy is used for mounting files

**Examples:**

```bash
# Run with bind mode (default - live sync)
clawker container run -it --agent dev alpine sh

# Run with snapshot mode (isolated copy)
clawker container run -it --agent sandbox --mode=snapshot alpine sh

# Create a container with snapshot mode
clawker container create --agent test --mode=snapshot alpine
```

**Workspace mounts created:**

Both modes automatically create the following mounts:
- Workspace mount at `workspace.remote_path` (default: `/workspace`)
  - **Bind mode**: Direct bind mount from host working directory; changes immediately affect host
  - **Snapshot mode**: Docker volume with copy of workspace files; changes isolated from host
- Config volume at `/home/claude/.claude`
- History volume at `/commandhistory`
- Docker socket mount (if `security.docker_socket: true`)

---

## The `--format` Flag

The `container list` and `container inspect` commands support the `--format` flag to customize output using Go templates.

**Supported commands:**

| Command | Format Target |
|---------|---------------|
| `container list` | `docker.Container` with `.Names` alias |
| `container inspect` | Docker `InspectResponse` (same as Docker CLI) |

**Common template fields for `container list`:**

| Field | Description |
|-------|-------------|
| `{{.Name}}` | Container name |
| `{{.Names}}` | Container name (Docker CLI compatibility alias) |
| `{{.Status}}` | Container status (e.g., "running", "exited") |
| `{{.Project}}` | Clawker project name |
| `{{.Agent}}` | Clawker agent name |
| `{{.Image}}` | Image name |
| `{{.ID}}` | Container ID |

**Common template fields for `container inspect`:**

| Field | Description |
|-------|-------------|
| `{{.State.Status}}` | Container state (running, exited, etc.) |
| `{{.Name}}` | Container name (includes leading `/`) |
| `{{.Config.Image}}` | Image name |
| `{{.NetworkSettings.IPAddress}}` | Container IP address |

**Examples:**

```bash
# List container names only
clawker container ls -a --format '{{.Names}}'

# List with custom format
clawker container ls -a --format '{{.Name}} {{.Status}}'

# Get container state
clawker container inspect --agent ralph --format '{{.State.Status}}'

# Get container IP
clawker container inspect --agent ralph --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}'
```

---

## Flag Passthrough Behavior

The `container run` and `container create` commands support passing flags to the container by placing them after the IMAGE argument.

**How it works:**

Clawker stops parsing its own flags after the first positional argument (IMAGE). All subsequent arguments, including flags, are passed directly to the container command.

```bash
clawker run [CLAWKER_FLAGS] IMAGE [CONTAINER_COMMAND_AND_FLAGS]
```

**Examples:**

```bash
# Clawker flags before image, container flags after
clawker run -it --rm alpine --version

# Pass Claude Code flags to the container
clawker run -it --rm clawker-myapp:latest --allow-dangerously-skip-permissions -p "Fix bugs"

# Mix clawker flags with container command
clawker run -it --rm -e FOO=bar alpine sh -c "echo hello"
```

**Using `--agent` without IMAGE:**

When using `--agent` without specifying an IMAGE (relying on defaults), you must use `--` to stop flag parsing:

```bash
# Without --, fails with "unknown flag: --allow-dangerously-skip-permissions"
clawker run -it --rm --agent ralph --allow-dangerously-skip-permissions  # ERROR

# Use -- to stop clawker flag parsing
clawker run -it --rm --agent ralph -- --allow-dangerously-skip-permissions -p "Fix bugs"
```

**Flag conflict: `-p`**

Clawker uses `-p` as shorthand for `--publish` (port mapping), while Claude Code uses `-p` for `--prompt`. Since clawker flags are parsed first, you must either specify the image or use `--`:

```bash
# This fails - clawker parses -p as port mapping
clawker run -it --rm -p "Fix bugs"  # ERROR: invalid port format

# Option 1: Specify image first, then Claude's -p is passed through
clawker run -it --rm clawker-myapp:latest -p "Fix bugs"

# Option 2: Use -- with --agent
clawker run -it --rm --agent ralph -- -p "Fix bugs"
```

---

## Configuration Commands

### `clawker config check`

Validate the clawker.yaml configuration file.

### `clawker monitor`

Manage the observability stack.

| Subcommand | Description |
|------------|-------------|
| `start`, `up` | Start the monitoring stack |
| `stop`, `down` | Stop the monitoring stack |
| `status` | Show monitoring stack status |

---

## Utility Commands

### `clawker generate`

Generate versions.json for Claude Code releases.

**Usage:**
```bash
clawker generate [output_dir] <version>...
```

**Examples:**
```bash
# Generate latest and stable versions
clawker generate latest stable

# Generate to specific directory
clawker generate ./dockerfiles latest 2.1 1.1
```
