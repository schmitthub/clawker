# Clawker

Manage Claude Code in secure Docker containers with clawker

Clawker (claude + docker) wraps [Claude Code](https://docs.anthropic.com/en/docs/claude-code) in safe, reproducible, monitored, isolated Docker containers.

## Quick Start

### Prerequisites

- Docker installed and running
- Go 1.21+ (for building from source)

### Installation

```bash
# Build from source
git clone https://github.com/schmitthub/clawker.git
cd clawker
go build -o bin/clawker ./cmd/clawker
```

### Basic Workflow

```bash
# 1. Initialize a project
cd your-project
clawker init

# 2. Build the image
clawker build

# 3. Run a container
clawker run -it --agent dev myimage sh

# Press Ctrl+C to exit when done
```

## Multi-Container Management

Clawker supports running multiple containers per project using **agents**. Each agent has its own container, volumes, and Claude Code session.

### Container Naming

Containers follow the format: `clawker.project.agent`

```
clawker.myapp.ralph      # Project "myapp", agent "ralph"
clawker.myapp.writer     # Project "myapp", agent "writer"
clawker.backend.worker   # Project "backend", agent "worker"
```

### Working with Agents

```bash
# Run containers with specific agent names
clawker run -it --agent ralph alpine sh
clawker run -it --agent writer node

# List all containers
clawker list

# Work with specific agents using container commands
clawker logs --agent ralph
clawker container exec -it clawker.myapp.ralph sh
clawker stop --agent ralph

# Stop all agents for a project
clawker stop

# Remove all containers for a project
clawker remove -p myapp
```

## Authentication

Clawker automatically passes Anthropic authentication from your host environment to the container:

| Environment Variable | Purpose |
|---------------------|---------|
| `ANTHROPIC_API_KEY` | API key for Claude authentication |
| `ANTHROPIC_AUTH_TOKEN` | Custom authorization token |
| `ANTHROPIC_BASE_URL` | Custom API endpoint |
| `ANTHROPIC_CUSTOM_HEADERS` | Additional HTTP headers |

Simply set `ANTHROPIC_API_KEY` on your host before running containers:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
clawker run -it --agent dev -e ANTHROPIC_API_KEY myimage claude
```

The environment variable is passed to the container for authentication.

## CLI Commands

| Command | Description |
|---------|-------------|
| `clawker init` | Create `clawker.yaml` and `.clawkerignore` in current directory |
| `clawker build` | Build the container image |
| `clawker run` | Create and run a new container (alias for `container run`) |
| `clawker start` | Start stopped containers (alias for `container start`) |
| `clawker stop` | Stop containers for the project |
| `clawker restart` | Restart containers to pick up environment changes |
| `clawker logs` | View container logs |
| `clawker list` | List all clawker containers |
| `clawker remove` | Remove containers, volumes, or unused resources |
| `clawker prune` | Alias for `clawker remove --unused` |
| `clawker monitor` | Manage local observability stack |
| `clawker config check` | Validate your `clawker.yaml` |
| `clawker generate` | Generate versions.json for Claude Code releases |

### clawker build

Builds the container image. Use this when you want to pre-build or rebuild with specific options.

```bash
clawker build [flags]

# Examples:
clawker build                            # Build image (uses Docker cache)
clawker build --no-cache                 # Build without Docker cache
clawker build --dockerfile ./Dockerfile  # Build using custom Dockerfile

Flags:
  --no-cache              Build without Docker cache
  --dockerfile <path>     Path to custom Dockerfile (overrides build.dockerfile in config)
```

### clawker run

Creates and runs a new container. This is an alias for `clawker container run`, following Docker's pattern where `docker run` is an alias for `docker container run`.

```bash
clawker run [OPTIONS] IMAGE [COMMAND] [ARG...]

# Examples:
clawker run -it --agent shell alpine sh        # Run interactive shell
clawker run --agent worker alpine echo "hello" # Run a command
clawker run --detach --agent web -p 8080:80 nginx  # Run in background
clawker run -it --agent dev -e NODE_ENV=dev node   # With env vars
clawker run -it --agent dev -v /host:/container alpine  # With bind mount
clawker run --rm -it alpine sh                 # Auto-remove on exit

Flags:
  --agent <name>        Agent name (uses clawker.<project>.<agent> naming)
  --name <name>         Full container name (overrides --agent)
  -e, --env <var>       Set environment variables
  -v, --volume <mount>  Bind mount a volume
  -p, --publish <port>  Publish container port to host
  -u, --user <user>     Username or UID
  -w, --workdir <dir>   Working directory inside container
  -i, --interactive     Keep STDIN open
  -t, --tty             Allocate a pseudo-TTY
  --rm                  Auto-remove container on exit
  --detach              Run in background
  --entrypoint <cmd>    Override default entrypoint
  --network <name>      Connect to a network
```

**Note:** Build your image first with `clawker build` before running.

### clawker start

Alias for `clawker container start`. Starts one or more stopped containers.

```bash
clawker start CONTAINER [CONTAINER...]

# Examples:
clawker start clawker.myapp.ralph         # Start a stopped container
clawker start --attach clawker.myapp.ralph  # Start and attach

Flags:
  -a, --attach       Attach STDOUT/STDERR and forward signals
  -i, --interactive  Attach container's STDIN
```

### clawker stop

Stops containers for this project. By default, stops all containers; use `--agent` to stop a specific one.

```bash
clawker stop [flags]

# Examples:
clawker stop                    # Stop all containers for project
clawker stop --agent ralph      # Stop specific agent
clawker stop --clean            # Stop and remove all volumes

Flags:
  --agent <name>  Agent name to stop (default: all agents)
  --clean         Also remove volumes (workspace, config, history)
  --force         Force stop (SIGKILL)
  --timeout       Seconds before force kill (default: 10)
```

### clawker list

Lists all clawker containers across all projects.

```bash
clawker list [flags]

# Examples:
clawker list              # List running containers
clawker list -a           # Include stopped containers
clawker list -p myapp     # Filter by project

Flags:
  -a, --all              Show all containers (including stopped)
  -p, --project <name>   Filter by project name
```

### clawker remove

Removes clawker containers and their associated resources. Supports three modes: by name, by project, or unused resources.

```bash
clawker remove [flags]

# Examples:
clawker remove -n clawker.myapp.ralph   # Remove specific container
clawker remove -p myapp                 # Remove all containers for project
clawker remove -p myapp -f              # Force remove running containers
clawker remove --unused                 # Remove unused resources (prune)
clawker remove --unused --all           # Remove ALL clawker resources
clawker remove --unused --all -f        # Skip confirmation

Flags:
  -n, --name <name>      Container name to remove
  -p, --project <name>   Remove all containers for project
  -u, --unused           Remove unused resources (prune mode)
  -a, --all              With --unused, also remove volumes and images
  -f, --force            Force remove or skip confirmation
```

### clawker prune

Alias for `clawker remove --unused`. Removes unused clawker resources.

```bash
clawker prune [flags]

# Examples:
clawker prune        # Remove stopped containers and dangling images
clawker prune -a     # Remove ALL clawker resources (including volumes)
clawker prune -f     # Skip confirmation prompt

Flags:
  -a, --all    Remove ALL clawker resources (containers, images, volumes)
  -f, --force  Skip confirmation prompt
```

**Warning:** `--all` removes persistent data including workspace volumes.

### clawker monitor

Manages the local observability stack for telemetry visualization (OpenTelemetry, Jaeger, Prometheus, Grafana).

```bash
clawker monitor <command>

# Subcommands:
clawker monitor init     # Scaffold monitoring configuration files
clawker monitor up       # Start the monitoring stack
clawker monitor down     # Stop the monitoring stack
clawker monitor status   # Show monitoring stack status
```

After starting the monitoring stack, restart your Claude containers to enable telemetry:

```bash
clawker monitor up
clawker restart
```

### clawker generate

Generates `versions.json` for Claude Code releases by fetching version information from npm. Used for maintaining the Docker image build infrastructure.

```bash
clawker generate [versions...]

# Examples:
clawker generate                    # Display current versions.json
clawker generate latest             # Fetch latest version from npm
clawker generate latest 2.1         # Fetch multiple version patterns
clawker generate --skip-fetch       # Use existing versions.json only

Flags:
  --skip-fetch  Skip npm fetch, use existing versions.json only
  --cleanup     Remove obsolete version directories (default: true)
```

**Version patterns:**

- `latest`, `stable`, `next` - Resolve via npm dist-tags
- `2.1` - Match highest 2.1.x release
- `2.1.2` - Exact version match

A standalone binary `clawker-generate` is also available for CI/CD pipelines:

```bash
# Build standalone binary
make cli-generate

# Run standalone
./bin/clawker-generate latest 2.1.2
```

## Configuration

Clawker uses `clawker.yaml` for project configuration. Run `clawker init` to generate a template.

### Full Example

```yaml
version: "1"
project: "my-app"

build:
  # Base image for the container
  image: "node:20-slim"
  # Optional: path to custom Dockerfile
  # dockerfile: "./.devcontainer/Dockerfile"
  # System packages to install
  packages:
    - git
    - curl
    - ripgrep

agent:
  # Files to include in Claude's context
  includes:
    - "./README.md"
    - "./.claude/memory.md"
  # Environment variables for Claude
  env:
    NODE_ENV: "development"

workspace:
  # Container path for your code
  remote_path: "/workspace"
  # Default mode: "bind" or "snapshot"
  default_mode: "bind"

security:
  # Network firewall (blocks outbound by default)
  enable_firewall: true
  # Docker socket access (disabled for security)
  docker_socket: false
  # Allowed domains when firewall enabled
  # allowed_domains:
  #   - "api.github.com"
  #   - "registry.npmjs.org"
```

## Advanced Build Configuration

Clawker provides two ways to customize the generated Dockerfile: **type-safe instructions** and **raw injection points**.

### Type-Safe Instructions (`build.instructions`)

Structured configuration for common Dockerfile instructions with validation:

```yaml
build:
  image: "node:20-slim"
  packages: [git, curl]

  instructions:
    # Environment variables
    env:
      NODE_ENV: "production"
      APP_PORT: "3000"

    # Docker labels
    labels:
      maintainer: "team@example.com"
      version: "1.0.0"

    # Copy files into image (runs as root for proper permissions)
    copy:
      - src: "./config/app.json"
        dest: "/etc/app/config.json"
        chown: "claude:claude"
        chmod: "644"

    # Expose ports
    expose:
      - port: 3000
      - port: 8080
        protocol: tcp

    # Build arguments
    args:
      - name: BUILD_VERSION
        default: "latest"

    # Volume mount points
    volumes:
      - "/data"
      - "/var/log/app"

    # Override default workdir
    workdir: "/app"

    # Health check
    healthcheck:
      cmd: ["curl", "-f", "http://localhost:3000/health"]
      interval: "30s"
      timeout: "10s"
      retries: 3
      start_period: "5s"

    # Custom shell
    shell: ["/bin/bash", "-c"]

    # Commands to run as root (before user switch)
    root_run:
      - cmd: "mkdir -p /opt/myapp"  # Runs on all distros
      - alpine: "apk add --no-cache sqlite"  # Alpine-specific
        debian: "apt-get install -y sqlite3"  # Debian-specific

    # Commands to run as claude user
    user_run:
      - cmd: "npm install -g typescript"
```

### OS-Aware Run Commands

The `root_run` and `user_run` instructions support OS-specific commands:

```yaml
instructions:
  root_run:
    # Generic command (runs on both Alpine and Debian)
    - cmd: "echo 'Hello World'"

    # OS-specific commands
    - alpine: "apk add --no-cache postgresql-client"
      debian: "apt-get install -y postgresql-client"
```

Clawker detects the base image OS and uses the appropriate command.

### Raw Injection Points (`build.inject`)

For advanced customization, inject raw Dockerfile instructions at specific points:

```yaml
build:
  image: "python:3.11-slim"

  inject:
    # After FROM line
    after_from:
      - "ARG BUILDPLATFORM"

    # After package installation
    after_packages:
      - "RUN pip install poetry"

    # After user/group setup (still as root)
    after_user_setup:
      - "COPY --chown=claude:claude ./scripts /opt/scripts"

    # After switching to claude user
    after_user_switch:
      - "RUN poetry config virtualenvs.in-project true"

    # After Claude Code installation
    after_claude_install:
      - "RUN claude config set theme dark"

    # Just before ENTRYPOINT
    before_entrypoint:
      - "HEALTHCHECK CMD curl -f http://localhost/ || exit 1"
```

**Injection points in order:**

1. `after_from` - After base image, before packages
2. `after_packages` - After apt/apk install
3. `after_user_setup` - After claude user created (still root)
4. `after_user_switch` - After `USER claude` (as claude)
5. `after_claude_install` - After Claude Code installed
6. `before_entrypoint` - Final customization point

### When to Use Each

| Use Case | Approach |
|----------|----------|
| Set environment variables | `instructions.env` |
| Install npm/pip packages | `instructions.user_run` |
| Install system packages (non-standard) | `instructions.root_run` with OS variants |
| Copy config files | `instructions.copy` |
| Complex multi-stage builds | `inject.*` with raw instructions |
| Platform-specific builds | `inject.after_from` with ARG/FROM |

## Workspace Modes

### Bind Mode (default)

Live sync between host and container. Changes in the container immediately reflect on your host filesystem.

```bash
clawker start --mode=bind
```

### Snapshot Mode

Creates an isolated copy of your workspace in a Docker volume. Host files remain untouched.

```bash
clawker start --mode=snapshot
```

Use snapshot mode when you want Claude to experiment freely without affecting your working directory.

## Security

Clawker prioritizes security by default:

- **Firewall enabled** - Outbound network traffic is blocked by default
- **Docker socket disabled** - No Docker-in-Docker unless explicitly enabled
- **Host read-only** - In snapshot mode, your host files are never modified
- **Sensitive env filtering** - Passwords and secrets from `.env` files are filtered

### Allowing Network Access

To allow specific domains through the firewall:

```yaml
security:
  enable_firewall: true
  allowed_domains:
    - "api.github.com"
    - "registry.npmjs.org"
```

### Enabling Docker-in-Docker

Only enable if you need Claude to run Docker commands:

```yaml
security:
  docker_socket: true
```

## Ignore Patterns

The `.clawkerignore` file controls which files are excluded in snapshot mode. It follows `.gitignore` syntax.

Default exclusions include:

- `node_modules/`, `vendor/`, `.venv/`
- Build outputs (`dist/`, `build/`)
- IDE files (`.idea/`, `.vscode/`)
- Secrets (`.env`, `*.pem`, `*.key`)
- Large archives (`*.zip`, `*.tar.gz`)

## Development

```bash
# Build CLI
go build -o bin/clawker ./cmd/clawker

# Build standalone generate binary
make cli-generate

# Run tests
go test ./...

# Run with debug logging
./bin/clawker --debug start
```

## License

MIT
