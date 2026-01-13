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

# 2. Start the container
clawker start

# 3. Claude Code is now running in the container
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
# Start agents with specific names
clawker start --agent ralph
clawker start --agent writer

# If no --agent specified, a random name is generated
clawker start    # Creates clawker.myapp.clever-fox

# List all containers
clawker list

# Work with specific agents
clawker logs --agent ralph
clawker shell --agent ralph
clawker stop --agent ralph

# Stop all agents for a project
clawker stop

# Remove all containers for a project
clawker remove -p myapp
```

Each agent has isolated volumes for workspace (snapshot mode), config, and command history.

## Authentication

Clawker automatically passes Anthropic authentication from your host environment to the container:

| Environment Variable | Purpose |
|---------------------|---------|
| `ANTHROPIC_API_KEY` | API key for Claude authentication |
| `ANTHROPIC_AUTH_TOKEN` | Custom authorization token |
| `ANTHROPIC_BASE_URL` | Custom API endpoint |
| `ANTHROPIC_CUSTOM_HEADERS` | Additional HTTP headers |

Simply set `ANTHROPIC_API_KEY` on your host before running `clawker start`:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
clawker start
```

Claude Code will authenticate automatically without requiring browser login.

## CLI Commands

| Command | Description |
|---------|-------------|
| `clawker init` | Create `clawker.yaml` and `.clawkerignore` in current directory |
| `clawker build` | Build the container image |
| `clawker start` | Build image (if needed), create container, and attach to Claude Code |
| `clawker run` | Run a one-shot command in an ephemeral container |
| `clawker stop` | Stop containers for the project |
| `clawker restart` | Restart containers to pick up environment changes |
| `clawker shell` | Open a bash shell in a running container |
| `clawker logs` | View container logs |
| `clawker list` | List all clawker containers |
| `clawker remove` | Remove containers and their volumes |
| `clawker prune` | Remove unused clawker resources |
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

### clawker start

Builds the container (if needed) and runs Claude Code. This is an idempotent operation - it reuses existing containers.

```bash
clawker start [-- <claude-args>...]

# Examples:
clawker start                          # Run Claude interactively
clawker start --agent ralph            # Start a named agent
clawker start -- -p "build a feature"  # Pass args to Claude CLI
clawker start -- --resume              # Resume previous session
clawker start --build                  # Force rebuild before running
clawker start -p 8080:8080             # Publish port 8080 to host
clawker start -p 24282:24282           # Access MCP dashboard from host

Flags:
  --agent <name>        Agent name for the container (default: random)
  --mode=bind|snapshot  Workspace mode (default: from config)
  --build               Force rebuild of container image
  --detach              Run container in background
  --clean               Remove existing container/volumes before starting
  -p, --publish <port>  Publish container port to host (e.g., -p 8080:8080)
```

**Note:** To build without Docker cache, run `clawker build --no-cache` first.

### clawker run

Runs a command in a new ephemeral container. Container and volumes are removed on exit by default (like `docker run --rm`).

```bash
clawker run [flags] [-- <command>...]

# Examples:
clawker run                           # Run Claude, remove on exit
clawker run --agent worker            # Run with a named agent
clawker run -- -p "quick question"    # Claude with args, remove on exit
clawker run --shell                   # Run shell, remove on exit
clawker run -- npm test               # Run arbitrary command
clawker run --keep                    # Keep container after exit
clawker run -p 8080:8080              # Publish port to host

Flags:
  --agent <name>        Agent name for the container (default: random)
  --mode=bind|snapshot  Workspace mode (default: from config)
  --build               Force rebuild of container image
  --shell               Run shell instead of Claude
  --keep                Keep container after exit (default: remove)
  -p, --publish <port>  Publish container port to host (e.g., -p 8080:8080)
```

**Note:** To build without Docker cache, run `clawker build --no-cache` first.

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

Removes clawker containers and their associated volumes.

```bash
clawker remove [flags]

# Examples:
clawker remove -n clawker.myapp.ralph   # Remove specific container
clawker remove -p myapp                   # Remove all containers for project
clawker remove -p myapp -f                # Force remove running containers

Flags:
  -n, --name <name>      Container name to remove
  -p, --project <name>   Remove all containers for project
  -f, --force            Force remove running containers
```

### clawker prune

Removes unused clawker resources (stopped containers, dangling images).

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
