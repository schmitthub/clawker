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

Initialize a new clawker project in the current directory.

**Usage:**
```bash
clawker init [flags]
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
| `--all` | `-a` | Include all resources |
| `--agent` | | Agent name shortcut for container commands |
| `--debug` | | Enable debug logging |

**Note:** The `-f` shorthand has different meanings depending on context:
- In build commands: `-f` means `--file` (Dockerfile path) - matches Docker CLI convention
- In remove/prune commands: `-f` means `--force`

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
| `cp` | Use `:PATH` syntax instead of `CONTAINER:PATH` |

**Examples:**

```bash
# Instead of:
clawker container stop clawker.myproject.ralph

# You can use:
clawker container stop --agent ralph

# View logs
clawker container logs --agent ralph --follow

# Copy files (use :PATH with --agent)
clawker container cp --agent ralph :/app/config.json ./config.json

# Rename (only NEW_NAME required with --agent)
clawker container rename --agent ralph clawker.myproject.newname
```

**Mutual exclusivity:**

The `--agent` flag and positional container arguments are mutually exclusive. You cannot use both together.

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
