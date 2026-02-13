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
- Clawker labels (`dev.clawker.*`) take precedence over user labels
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

**Note:** To initialize a project, use `clawker project init` instead. `clawker init` sets up user-level settings only; it does not register projects.

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

**Registry:** `project init` also registers the project in `~/.local/clawker/projects.yaml`.

---

### `clawker project register`

Register an existing clawker project in the local registry without modifying the configuration file.

**Usage:**
```bash
clawker project register [project-name] [flags]
```

Useful when a `clawker.yaml` was manually created, copied from another machine, or already exists and you want to register it locally.

**Flags:**

| Flag | Shorthand | Type | Default | Description |
|------|-----------|------|---------|-------------|
| `--yes` | `-y` | bool | false | Non-interactive mode, use directory name as project name |

**Examples:**
```bash
# Register with interactive prompt for project name
clawker project register

# Register with a specific project name
clawker project register my-project

# Register using directory name without prompting
clawker project register --yes
```

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

## Autonomous Loops (`clawker loop`)

Run Claude Code in autonomous loops with circuit breaker protection.

### `clawker loop run`

Start an autonomous Claude Code loop.

**Usage:**
```bash
clawker loop run --agent NAME [flags]
```

Runs Claude Code repeatedly with `--continue` until completion or stagnation.
The agent must output a LOOP_STATUS block for progress tracking.

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
clawker loop run --agent dev --prompt "Fix all failing tests"

# Start from a prompt file
clawker loop run --agent dev --prompt-file task.md

# Continue an existing session
clawker loop run --agent dev

# Reset circuit breaker and retry
clawker loop run --agent dev --reset-circuit

# Run with custom limits
clawker loop run --agent dev --max-loops 100 --stagnation-threshold 5

# Run with live monitoring
clawker loop run --agent dev --monitor

# Run with rate limiting (5 calls per hour)
clawker loop run --agent dev --calls 5

# Run with verbose output
clawker loop run --agent dev -v

# Run in YOLO mode (skip all permission prompts)
clawker loop run --agent dev --skip-permissions
```

**Exit conditions:**
- Claude signals `EXIT_SIGNAL: true` with sufficient completion indicators (strict mode)
- Claude signals `EXIT_SIGNAL: true` or `STATUS: COMPLETE` (default mode)
- Circuit breaker trips (no progress, same error, output decline, or test loops)
- Maximum loops reached
- Error during execution
- Claude's API rate limit hit

---

### `clawker loop status`

Show current loop session status.

**Usage:**
```bash
clawker loop status --agent NAME [flags]
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--agent` | string | | Agent name (required) |
| `--json` | bool | false | Output as JSON |

**Examples:**
```bash
# Show status
clawker loop status --agent dev

# Output as JSON
clawker loop status --agent dev --json
```

---

### `clawker loop reset`

Reset the circuit breaker for an agent.

**Usage:**
```bash
clawker loop reset --agent NAME [flags]
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
clawker loop reset --agent dev

# Reset everything (circuit and session)
clawker loop reset --agent dev --all
```

---

### `clawker loop tui`

Launch an interactive TUI dashboard for monitoring loop agents.

**Usage:**
```bash
clawker loop tui
```

Provides a real-time terminal interface for monitoring all loop agents in the current project. Features include live agent discovery, status updates, and log streaming.

**Examples:**
```bash
# Launch TUI for current project
clawker loop tui
```

**Note:** Must be run from a directory containing `clawker.yaml`.

---

### LOOP_STATUS Block Format

Claude must output this block for progress tracking:

```
---LOOP_STATUS---
STATUS: IN_PROGRESS | COMPLETE | BLOCKED
TASKS_COMPLETED_THIS_LOOP: <number>
FILES_MODIFIED: <number>
TESTS_STATUS: PASSING | FAILING | NOT_RUN
WORK_TYPE: IMPLEMENTATION | TESTING | DOCUMENTATION | REFACTORING
EXIT_SIGNAL: false | true
RECOMMENDATION: <one line>
---END_LOOP_STATUS---
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

### Worktree Management (`clawker worktree`)

| Command | Description |
|---------|-------------|
| `add <branch>` | Create a worktree for a branch (idempotent) |
| `list`, `ls` | List git worktrees for the current project |
| `prune` | Remove stale worktree entries from registry |
| `remove <branch>` | Remove a git worktree |

**Flags for `add`:**

| Flag | Description |
|------|-------------|
| `--base REF` | Base ref to create branch from (default: HEAD) |

**Flags for `list`:**

| Flag | Description |
|------|-------------|
| `--quiet`, `-q` | Suppress headers, show branch names only |

**Flags for `prune`:**

| Flag | Description |
|------|-------------|
| `--dry-run` | Show what would be pruned without removing |

**Flags for `remove`:**

| Flag | Description |
|------|-------------|
| `--force` | Remove worktree even if it has uncommitted changes |
| `--delete-branch` | Also delete the git branch after removing worktree |

**Status column in `list`:**
- (empty) — healthy worktree
- `dir missing` — worktree directory doesn't exist
- `git missing` — .git file missing or invalid
- `dir missing, git missing` — stale entry, use `prune` to clean up

**Notes:**
- Branch names with slashes (e.g., `feature/foo`, `a/my-branch`) are fully supported
- The `add` command is idempotent: if the worktree exists, it returns the existing path
- Worktree directories use slugified names (e.g., `feature/foo` → `feature-foo`)
- Use `prune` to clean up stale registry entries after using native `git worktree remove`

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

## The `@` Symbol (Automatic Image Resolution)

The `container run` and `container create` commands support `@` as a special IMAGE argument that automatically resolves the image name.

**How it works:**

When you pass `@` as the IMAGE argument:

1. **Project image** - Looks for `clawker-<project>:latest` with managed labels
2. **Default image** - Falls back to `default_image` from settings or config
3. **Error** - If neither found, prompts with next steps

**Resolution order:**

| Priority | Source | Example |
|----------|--------|---------|
| 1 | Project-built image | `clawker-myproject:latest` |
| 2 | Settings default_image | From `~/.local/clawker/settings.yaml` |
| 3 | Config default_image | From `clawker.yaml` |

**Examples:**

```bash
# Instead of typing the full image name:
clawker run -it clawker-myproject:abcd1234

# Just use @:
clawker run -it @

# Works with all run/create flags:
clawker run -it --rm @
clawker run -it --agent dev @
clawker container create --agent sandbox @

# When using with Claude Code flags, @ stops clawker's flag parsing:
clawker run -it --rm @ --dangerously-skip-permissions -p "Fix bugs"
```

**When to use `@`:**

- After running `clawker build` - `@` resolves to the built image
- When you have a `default_image` configured - `@` uses that
- For quick iteration - no need to remember exact image names/tags

**Error handling:**

If no image can be resolved, you'll see:

```
Error: Could not resolve image

Next steps:
  1. Run 'clawker build' to build a project image
  2. Set 'default_image' in clawker.yaml or ~/.local/clawker/settings.yaml
  3. Or specify an image directly: clawker run IMAGE
```

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
clawker container stop clawker.myproject.dev

# You can use:
clawker container stop --agent dev

# View logs
clawker container logs --agent dev --follow

# Copy files: :PATH uses the --agent flag value
clawker container cp --agent dev :/app/config.json ./config.json

# Copy files: name:PATH resolves name as agent (overrides --agent)
clawker container cp --agent dev writer:/app/output.txt ./output.txt

# Rename (only NEW_NAME required with --agent)
clawker container rename --agent dev clawker.myproject.newname
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
clawker container run -it --agent dev @

# Run with snapshot mode (isolated copy)
clawker container run -it --agent sandbox --mode=snapshot @

# Create a container with snapshot mode
clawker container create --agent test --mode=snapshot @
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

## Container Run/Create Options

The `container run` and `container create` commands support a comprehensive set of options for configuring containers. These options mirror the Docker CLI for familiarity.

### Basic Options

| Flag | Shorthand | Type | Description |
|------|-----------|------|-------------|
| `--agent` | | string | Agent name for container (uses `clawker.<project>.<agent>` naming) |
| `--name` | | string | Same as `--agent`; provided for Docker CLI familiarity |
| `--env` | `-e` | stringArray | Set environment variables |
| `--volume` | `-v` | stringArray | Bind mount a volume |
| `--publish` | `-p` | stringArray | Publish container port(s) to host |
| `--worktree` | | string | Use git worktree: 'branch' to use/create, 'branch:base' to create from base |
| `--user` | `-u` | string | Username or UID |
| `--entrypoint` | | string | Overwrite the default ENTRYPOINT |
| `--tty` | `-t` | bool | Allocate a pseudo-TTY |
| `--interactive` | `-i` | bool | Keep STDIN open even if not attached |
| `--network` | | string | Connect container to a network |
| `--label` | `-l` | stringArray | Set metadata on container |
| `--rm` | | bool | Automatically remove container when it exits |
| `--mode` | | string | Workspace mode: 'bind' (live sync) or 'snapshot' (isolated copy) |

### Resource Limits

| Flag | Shorthand | Type | Description |
|------|-----------|------|-------------|
| `--memory` | `-m` | string | Memory limit (e.g., `512m`, `2g`) |
| `--memory-swap` | | string | Total memory (memory + swap), `-1` for unlimited swap |
| `--cpus` | | string | Number of CPUs (e.g., `1.5`) |
| `--cpu-shares` | `-c` | int64 | CPU shares (relative weight) |

**Notes:**
- `--memory-swap` requires `--memory` to be set (unless `-1` for unlimited)
- `--memory-swap` must be greater than or equal to `--memory`

### Networking

| Flag | Type | Description |
|------|------|-------------|
| `--hostname` | string | Container hostname |
| `--dns` | stringArray | Set custom DNS servers |
| `--dns-search` | stringArray | Set custom DNS search domains |
| `--add-host` | stringArray | Add custom host-to-IP mapping (host:ip) |

**Examples:**
```bash
# Set container hostname
clawker run -it --hostname myhost @ sh

# Use custom DNS servers
clawker run -it --dns 8.8.8.8 --dns 8.8.4.4 @

# Add host entries
clawker run -it --add-host "myservice:192.168.1.100" @
```

### Storage

| Flag | Type | Description |
|------|------|-------------|
| `--tmpfs` | stringArray | Mount a tmpfs directory (e.g., `/tmp:rw,size=64m`) |
| `--read-only` | bool | Mount the container's root filesystem as read-only |
| `--volumes-from` | stringArray | Mount volumes from the specified container(s) |

**Examples:**
```bash
# Mount tmpfs for temporary storage
clawker run -it --tmpfs /tmp:rw,size=64m @

# Read-only root with tmpfs for writable areas
clawker run -it --read-only --tmpfs /tmp @

# Share volumes from another container
clawker run -it --volumes-from clawker.myproject.data @
```

### Security

| Flag | Type | Description |
|------|------|-------------|
| `--cap-add` | stringArray | Add Linux capabilities |
| `--cap-drop` | stringArray | Drop Linux capabilities |
| `--privileged` | bool | Give extended privileges to this container |
| `--security-opt` | stringArray | Security options (e.g., seccomp, apparmor, label) |

**Examples:**
```bash
# Add specific capability
clawker run -it --cap-add SYS_PTRACE @

# Drop all capabilities, add only what's needed
clawker run -it --cap-drop ALL --cap-add NET_RAW @

# Run privileged (use with caution)
clawker run -it --privileged @

# Custom security options
clawker run -it --security-opt seccomp=unconfined @
```

**Note:** Capabilities specified via CLI flags take precedence over those in `security.cap_add` in `clawker.yaml`.

### Health Checks

| Flag | Type | Description |
|------|------|-------------|
| `--health-cmd` | string | Command to run to check health |
| `--health-interval` | duration | Time between running the check (e.g., `30s`, `1m`) |
| `--health-timeout` | duration | Maximum time to allow one check to run (e.g., `30s`) |
| `--health-retries` | int | Consecutive failures needed to report unhealthy |
| `--health-start-period` | duration | Start period for the container to initialize (e.g., `5s`) |
| `--no-healthcheck` | bool | Disable any container-specified HEALTHCHECK |

**Examples:**
```bash
# Simple health check
clawker run -it --health-cmd "curl -f http://localhost:8080/health" \
  --health-interval 30s --health-retries 3 @

# Health check with startup grace period
clawker run -it --health-cmd "exit 0" \
  --health-interval 10s --health-start-period 30s @

# Disable healthcheck from base image
clawker run -it --no-healthcheck @
```

**Note:** `--no-healthcheck` conflicts with any `--health-*` options.

### Process and Runtime

| Flag | Type | Description |
|------|------|-------------|
| `--restart` | string | Restart policy (`no`, `always`, `on-failure[:max-retries]`, `unless-stopped`) |
| `--stop-signal` | string | Signal to stop the container (e.g., `SIGTERM`, `SIGKILL`) |
| `--stop-timeout` | int | Timeout (in seconds) to stop a container |
| `--init` | bool | Run an init inside the container that forwards signals and reaps processes |

**Examples:**
```bash
# Restart on failure with max 3 retries
clawker run --detach --restart on-failure:3 @

# Always restart
clawker run --detach --restart always @

# Use init process
clawker run -it --init @

# Custom stop signal and timeout
clawker run -it --stop-signal SIGKILL --stop-timeout 30 @
```

**Note:** `--restart` and `--rm` are mutually exclusive (except `--restart no`).

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
clawker container inspect --agent dev --format '{{.State.Status}}'

# Get container IP
clawker container inspect --agent dev --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}'
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
clawker run -it --rm @ -- --version

# Pass Claude Code flags to the container
clawker run -it --rm @ -- --dangerously-skip-permissions -p "Fix bugs"

# Mix clawker flags with container command
clawker run -it --rm -e FOO=bar @ -- sh -c "echo hello"
```

**Using `--agent` with `@`:**

When using `--agent`, you must also specify the `@` symbol to auto-resolve the image:

```bash
# Standard pattern: --agent with @ for image resolution
clawker run -it --rm --agent dev @

# Pass flags to Claude Code after @ and --
clawker run -it --rm --agent dev @ -- --dangerously-skip-permissions -p "Fix bugs"
```

**Flag conflict: `-p`**

Clawker uses `-p` as shorthand for `--publish` (port mapping), while Claude Code uses `-p` for `--prompt`. Since clawker flags are parsed first, use `@` and `--` to pass Claude's `-p`:

```bash
# This fails - clawker parses -p as port mapping
clawker run -it --rm -p "Fix bugs"  # ERROR: invalid port format

# Correct: Use @ for image, -- to separate, then Claude's -p
clawker run -it --rm --agent dev @ -- -p "Fix bugs"

# Also works with explicit image name
clawker run -it --rm clawker-myapp:latest -- -p "Fix bugs"
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
