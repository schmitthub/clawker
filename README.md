# Claucker

Manage full-featured Claude Code development container environments.

Claucker wraps [Claude Code](https://docs.anthropic.com/en/docs/claude-code) in secure, reproducible Docker containers. Core philosophy: **Safe Autonomy** - your host system is read-only by default.

## Quick Start

### Prerequisites

- Docker installed and running
- Go 1.21+ (for building from source)
- An Anthropic API key

### Installation

```bash
# Build from source
git clone https://github.com/schmitthub/claucker.git
cd claucker
go build -o bin/claucker ./cmd/claucker
```

### Basic Workflow

```bash
# 1. Set your API key
export ANTHROPIC_API_KEY="sk-ant-..."

# 2. Initialize a project
cd your-project
claucker init

# 3. Start the container
claucker up

# 4. Claude Code is now running in the container
# Press Ctrl+C to exit when done

# 5. Stop the container
claucker down
```

## Authentication

Claucker automatically passes Anthropic authentication from your host environment to the container:

| Environment Variable | Purpose |
|---------------------|---------|
| `ANTHROPIC_API_KEY` | API key for Claude authentication |
| `ANTHROPIC_AUTH_TOKEN` | Custom authorization token |
| `ANTHROPIC_BASE_URL` | Custom API endpoint |
| `ANTHROPIC_CUSTOM_HEADERS` | Additional HTTP headers |

Simply set `ANTHROPIC_API_KEY` on your host before running `claucker up`:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
claucker up
```

Claude Code will authenticate automatically without requiring browser login.

## CLI Commands

| Command | Description |
|---------|-------------|
| `claucker init` | Create `claucker.yaml` and `.clauckerignore` in current directory |
| `claucker up` | Build image, create container, and attach to Claude Code |
| `claucker run` | Run a one-shot command in an ephemeral container |
| `claucker down` | Stop the running container |
| `claucker sh` | Open a bash shell in the running container |
| `claucker logs` | View container logs |
| `claucker config check` | Validate your `claucker.yaml` |

### claucker up

Builds the container (if needed) and runs Claude Code. This is an idempotent operation - it reuses existing containers.

```bash
claucker up [-- <claude-args>...]

# Examples:
claucker up                          # Run Claude interactively
claucker up -- -p "build a feature"  # Pass args to Claude CLI
claucker up -- --resume              # Resume previous session

Flags:
  --mode=bind|snapshot  Workspace mode (default: from config)
  --build               Force rebuild of container image
  --no-cache            Build without Docker cache (implies --build)
  --detach              Run container in background
  --clean               Remove existing container/volumes before starting
```

### claucker run

Runs a command in a new ephemeral container. Container is removed on exit by default (like `docker run --rm`).

```bash
claucker run [flags] [-- <command>...]

# Examples:
claucker run                           # Run Claude, remove on exit
claucker run -- -p "quick question"    # Claude with args, remove on exit
claucker run --shell                   # Run shell, remove on exit
claucker run -- npm test               # Run arbitrary command
claucker run --keep                    # Keep container after exit

Flags:
  --mode=bind|snapshot  Workspace mode (default: from config)
  --build               Force rebuild of container image
  --no-cache            Build without Docker cache (implies --build)
  --shell               Run shell instead of Claude
  --keep                Keep container after exit (default: remove)
```

### claucker down

```bash
claucker down [flags]

Flags:
  --clean    Also remove volumes (workspace, config, history)
```

## Configuration

Claucker uses `claucker.yaml` for project configuration. Run `claucker init` to generate a template.

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

Claucker provides two ways to customize the generated Dockerfile: **type-safe instructions** and **raw injection points**.

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

Claucker detects the base image OS and uses the appropriate command.

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
claucker up --mode=bind
```

### Snapshot Mode

Creates an isolated copy of your workspace in a Docker volume. Host files remain untouched.

```bash
claucker up --mode=snapshot
```

Use snapshot mode when you want Claude to experiment freely without affecting your working directory.

## Security

Claucker prioritizes security by default:

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

The `.clauckerignore` file controls which files are excluded in snapshot mode. It follows `.gitignore` syntax.

Default exclusions include:
- `node_modules/`, `vendor/`, `.venv/`
- Build outputs (`dist/`, `build/`)
- IDE files (`.idea/`, `.vscode/`)
- Secrets (`.env`, `*.pem`, `*.key`)
- Large archives (`*.zip`, `*.tar.gz`)

## Development

```bash
# Build
go build -o bin/claucker ./cmd/claucker

# Run tests
go test ./...

# Run with debug logging
./bin/claucker --debug up
```

## License

MIT
