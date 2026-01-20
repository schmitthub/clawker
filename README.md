# Clawker

<p align="center">
  <a href="https://golang.org"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go" alt="Go"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License"></a>
  <a href="#"><img src="https://img.shields.io/badge/Platform-macOS-lightgrey?logo=apple" alt="macOS"></a>
  <a href="#"><img src="https://img.shields.io/badge/Claude-D97757?logo=claude&logoColor=fff" alt="Claude"></a>
  <img alt="Vibe coded with love" src="https://img.shields.io/badge/Vibe%20coded%20with-%F0%9F%92%97-1f1f1f?labelColor=ff69b4">
</p>

Claude Code in YOLO mode can wreak havoc on your system. Setting up Docker manually is tedious - Dockerfiles, volumes, networking. OAuth doesn't work because container localhost isn't host localhost. Git credentials from your keychain don't exist inside containers. And you have no visibility into what's happening.

**Clawker** (claude + docker) wraps Claude Code in Docker containers with a familiar CLI. It handles auth seamlessly via a host proxy, forwards your git credentials, and provides optional monitoring - so you can let Claude Code loose without worrying about your system.

> **Status:** Alpha (macOS tested). Issues and PRs welcome — if clawker helps you, please star the repo.
>
> **Planned features and fixes**
> - Linux support (untested)
> - Windows support
> - Versioning and releases with CI/CD integration
> - Terminal UI improvements (redraw on reattach, status indicators, progress bars, styling); current output can conflict with Claude's Ink-based TUI (see Known Issues)
> - Auto pruning to manage disk usage
> - Man pages and helper docs
> - Docker MCP Toolkit support (currently a known “feature-not-a-bug”: MCP plugin inside a container doesn’t detect Docker Desktop)
> - Host proxy browser auth: re-attach before authenticating can break the flow (low priority)
> - Grafana pre-built dashboard improvements (currently basic POC)
> - Improved host Claude directory mounting strategy to avoid permission issues and settings

## Quick Start

**Prerequisites:** Docker running, Go 1.25+

```bash
# Install
git clone https://github.com/schmitthub/clawker.git
cd clawker && go build -o ./bin/clawker ./cmd/clawker
export PATH="$PWD/bin:$PATH"

# One-time user setup (creates ~/.local/clawker/settings.yaml)
clawker init

# Start a project
cd your-project
clawker project init  # Creates clawker.yaml
```

Cuztomize your build in `clawker.yaml` (see below), then build your project's image:

```bash
clawker build
```

Create and run some agents. In this example you'll create a main agent for interactive working sessions, and another for YOLO unattended work.

```bash
# Create a fresh container, start it, and connect interactively.
# Use Claude Code as normal, This will feel just like running it on your host.
# Clawker adds a status line so you know this session is containerized by clawker and which agent you're using.
# Subscription users will need authenticate via browser.
clawker run -it --agent main

# hit crtl+p, ctrl+q to detach without stopping the container. You can leave sessions running

# Re-attach to the main agent
clawker attach --agent main

# Stop the agent with ctrl-c, this is like exiting Claude Code normally. When Claude exits, the container stops.

# Start the main agent and attach to it in interactive mode
clawker start -a -i --agent main

# Detach with crtl+p, ctrl+q

# Stop the main agent from the host
clawker stop --agent main

# Lets start a new agent in interactive mode and give it a modified start command for YOLOing
# authenticate if needed, then use Claude Code as normal. hit crtl+p, ctrl+q to detach, or ctlr+c to stop the container
clawker run -it --agent ralph -- --dangerously-skip-permissions

clawker stop --agent ralph # if you detached instead of exiting

# start up ralph (subscription users need to run interactively first to authenticate like above)
clawker start --agent ralph
# send a prompt to run your ralph agent, you can use this in scripts
echo "hi" | clawker container exec --agent ralph claude -p
```

You now have two specialized claude code containers that can be attached to or started/stopped as needed for different purposes

## Customizing Your Build

The default clawker image includes essentials for most projects: git, curl, vim, zsh, ripgrep, and more. But your project likely needs language-specific tools. Here's how to customize your `clawker.yaml`.

### Example: TypeScript/React Project

Let's add Node.js (via nvm) and pnpm to a project:

```yaml
version: "1"
project: "my-react-app"

build:
  # Start with Debian bookworm (has build essentials)
  image: "buildpack-deps:bookworm-scm"

  # System packages (apt-get install)
  packages:
    - git
    - curl
    - ripgrep

  instructions:
    # Environment variables baked into the image
    env:
      NVM_DIR: "/home/claude/.nvm"
      NODE_VERSION: "22"

    # Commands run as the claude user
    user_run:
      # Install nvm
      - cmd: |
          curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash

      # Install Node.js and pnpm
      - cmd: |
          . "$NVM_DIR/nvm.sh" && \
          nvm install $NODE_VERSION && \
          npm install -g pnpm

      # Add nvm to shell profile
      - cmd: |
          echo '. "$NVM_DIR/nvm.sh"' >> ~/.bashrc
```

### Build Properties

| Property | Description |
|----------|-------------|
| `build.image` | Base Docker image (e.g., `buildpack-deps:bookworm-scm`, `node:22-bookworm`) |
| `build.packages` | System packages installed via apt-get |
| `build.instructions.env` | Environment variables set in the image |
| `build.instructions.root_run` | Commands run as root (system-level setup) |
| `build.instructions.user_run` | Commands run as claude user (language tools, global packages) |
| `build.dockerfile` | Path to custom Dockerfile (skips generation entirely) |

### More Examples

See the [`examples/`](./examples/) directory for complete configurations:

- **[typescript-react.yaml](./examples/typescript-react.yaml)** - Node.js via nvm, pnpm
- **[python.yaml](./examples/python.yaml)** - Python via uv (fast package manager)
- **[rust.yaml](./examples/rust.yaml)** - Rust via rustup
- **[go.yaml](./examples/go.yaml)** - Go with gopls and delve
- **[csharp.yaml](./examples/csharp.yaml)** - .NET SDK
- **[php.yaml](./examples/php.yaml)** - PHP with Composer

## Workflows

### Starting containers

```bash
# Bind mode (default) - changes workspace sync to host immediately
clawker start --agent dev

# Snapshot mode - isolated copy, use git to sync changes back to host
clawker start --agent sandbox --mode snapshot
```

### Passing Claude Code options

```bash
# Using --agent to select the container? Use -- to separate clawker flags from Claude flags
clawker run -it --rm --agent ralph -- --dangerously-skip-permissions

# or give it an explicit image to use
clawker image list
clawker run -it --rm clawker-myproject:latest --dangerously-skip-permissions
```

### Detach and reattach

```bash
# Detach from running container: Ctrl+P, Ctrl+Q

# Reattach later
clawker container attach --agent ralph
# or
clawker container attach clawker.myproject.ralph
```

### List and manage containers

```bash
clawker container ls              # List all clawker containers
clawker ps                     # Alias for container ls
clawker container stop --agent ralph
```

### Scripted Workflows (Wiggum Pattern)

The wiggum pattern runs Claude Code in autonomous loops with scripted prompts.

> **Subscription users:** You must authenticate interactively first. Run
> `clawker run -it --agent <name>`, complete browser OAuth, then exit. After
> that, scripted usage works because credentials persist in the config volume.

#### Keep container running and use exec

```bash
# Create and auth, then detach with Ctrl+P, Ctrl+Q
clawker run -it --agent ralph -- --dangerously-skip-permissions

# Send prompts via exec (new claude process per task)
echo "Fix the tests" | clawker exec -i --agent ralph claude -p

# Stop when done
clawker stop --agent ralph
```

#### Continuous loop (wiggum style)

```bash
#!/bin/bash
# wiggum.sh - Run Claude in a loop until task complete. Requires prior container creation and auth.

AGENT="worker"
TASK="Review the codebase and fix any bugs you find"

while true; do
  echo "$TASK" | clawker exec --agent "$AGENT" claude -p

  read -p "Continue? [y/N] " -n 1 -r
  echo
  [[ ! $REPLY =~ ^[Yy]$ ]] && break
done
```

#### Parallel agents with worktrees

```bash
#!/bin/bash
# parallel-agents.sh - Run multiple agents on different branches. Requires prior container creation and auth.

# Create worktrees
git worktree add ../myapp-w1 -b feature/auth
git worktree add ../myapp-w2 -b feature/tests

# Auth each agent first (one-time, interactive)
# cd ../myapp-w1 && clawker run -it --agent w1  # complete OAuth, then exit with Ctrl+C
# cd ../myapp-w2 && clawker run -it --agent w2  # complete OAuth, then exit with Ctrl+C

# Run tasks in parallel
cd ../myapp-w1 && echo "Implement user auth" | clawker exec --agent w1 claude -p &
cd ../myapp-w2 && echo "Write integration tests" | clawker exec --agent w2 claude -p &
wait

echo "Both agents finished"
```

## Dockerfile Generation

Want to use Docker directly without clawker's management? The `generate` command creates clawker boilerplate Dockerfiles using any Claude Code npm tag or version.

```bash
# Generate Dockerfiles for latest version
clawker generate latest

# Generate for multiple versions
clawker generate latest stable 2.1

# Output to specific directory
clawker generate --output ./dockerfiles latest
```

Files are saved to `~/.local/clawker/build/dockerfiles/$claudeCodeVersion-$baseImage.dockerfile` by default. Then build with Docker:

```bash
docker build -t my-claude:latest ~/.local/clawker/build/dockerfiles/2.1.12-bookworm.dockerfile
```

You can also use the standalone `clawker-generate` in `./cmd/clawker-generate/` if all you need is Dockerfile generation.

```bash
go build -o ./bin/clawker-generate ./cmd/clawker-generate
./bin/clawker-generate latest next stable 2.1
```

## Commands

> **LLM-friendly docs:** Complete command documentation with all flags and examples is available in [`docs/markdown/`](./docs/markdown/) for AI assistants and tooling.

Clawker mirrors Docker's CLI structure for familiarity. If you know Docker, you know clawker.

| Command | Description |
|---------|-------------|
| `clawker init` | Set up user settings (~/.local/clawker/settings.yaml) |
| `clawker project init` | Initialize project with clawker.yaml |
| `clawker build` | Build container image |
| `clawker start --agent NAME` | Start a named agent container |
| `clawker run` | Build and run (one-shot) |
| `clawker container ls` | List containers |
| `clawker container stop` | Stop container |
| `clawker container logs` | View logs |
| `clawker container attach` | Attach to running container |
| `clawker image ls` | List images |
| `clawker volume ls` | List volumes |
| `clawker monitor start/stop` | Control monitoring stack |

Management commands (`container`, `image`, `volume`, `network`, `project`) support the same verbs as Docker: `ls`, `inspect`, `rm`, `prune`.

## Isolation Features

Clawker is a port of the Docker CLI, not just a passthrough. It adds isolation features to keep your system safe from rogue Claude Code agents, and provides clawker-only isolation when running docker-like commands.

* clawker-only resource isolation when running docker-like commands, you don't have to worry about filters. Clawker only sees its own resources, so you don't accidentally delete or modify other docker resources. This is done via labels under the hood.
* per-project resource namespacing, so you can have multiple projects on the same host without conflicts. Each project gets its own set of containers, images, volumes, and networks, identified by labels.
* per-agent containerization, so each agent runs in its own isolated container. You can have multiple agents running simultaneously without interference.
* Network firewalling, so you can restrict outbound network access from containers. By default, all outbound traffic is blocked except for allowed domains in a firewall init script.
* All clawker resources are added to a docker network `clawker-net` so they can communicate if needed.

## Authentication & Git

### API Key Users

Using an API key? Just pass it as an environment variable:

```bash
clawker run -it --rm -e ANTHROPIC_API_KEY myimage:latest
```

Or set it in your shell and clawker forwards it automatically.

### Subscription Users

**The problem:** Containers have their own localhost. When Claude Code opens a browser for OAuth, the callback goes to the wrong place.

**The solution:** Clawker runs a lightweight host proxy that bridges the gap.

**Auth Flow:**

```
Container                    Host Proxy (:18374)              Browser
    |                               |                             |
    | Claude needs auth             |                             |
    | ---------------------------->| intercepts OAuth URL         |
    |                              | rewrites callback ---------->|
    |                              |                  user logs in|
    |                              |<-------- callback redirect   |
    |<-- forwards to container     |                             |
    | Auth complete!               |                             |
```

### Git Credentials

- **HTTPS**: Forwarded via host proxy (GitHub CLI, macOS Keychain, Git Credential Manager all work)
- **SSH**: Agent forwarding (your keys never leave the host)
- **Config**: `~/.gitconfig` copied automatically

**Zero config** - if git works on your host, it works in containers.

## Monitoring

Optional observability stack for tracking resource usage across all your clawker containers.

```bash
clawker monitor start    # Starts Prometheus + Grafana
clawker monitor stop     # Stops the stack
clawker monitor status   # Check if running
```

**Protip** You can also monitor your host's claude sessions with this stack just by setting these env vars:

```bash
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_METRICS_EXPORTER=otlp
OTEL_RESOURCE_ATTRIBUTES=agent=host.clawker # ie host.project name
OTEL_LOGS_EXPORT_INTERVAL=5000
OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://localhost:4318/v1/metrics
OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://localhost:4318/v1/logs
OTEL_METRIC_EXPORT_INTERVAL=10000
OTEL_LOGS_EXPORTER=otlp
OTEL_METRICS_INCLUDE_ACCOUNT_UUID=true
OTEL_METRICS_INCLUDE_SESSION_ID=true
CLAUDE_CODE_ENABLE_TELEMETRY=1
```

**Dashboard:** http://localhost:3000

## System Overview

```
+---------------------------------------------------------------------+
|                              HOST                                    |
|                                                                      |
|  +--------------+         +--------------------------------------+  |
|  |              |         |            CONTAINER                  |  |
|  |  clawker CLI |-------->|  +--------------------------------+  |  |
|  |              |         |  |         Claude Code            |  |  |
|  +--------------+         |  +--------------------------------+  |  |
|         |                 |                                       |  |
|         |                 |  /workspace (bind or snapshot)        |  |
|         |                 |  /home/claude/.claude (persisted)     |  |
|         |                 |  /commandhistory (persisted)          |  |
|         v                 |                                       |  |
|  +--------------+         |  Scripts:                             |  |
|  |  Host Proxy  |<--------|  - host-open (browser URLs)           |  |
|  |   (:18374)   |         |  - callback-forwarder (OAuth)         |  |
|  |              |         |  - git-credential-clawker (HTTPS)     |  |
|  | - OAuth      |         |  - ssh-agent-proxy (SSH keys)         |  |
|  | - Git creds  |         +--------------------------------------+  |
|  | - URL open   |                                                    |
|  +--------------+         +--------------------------------------+  |
|         |                 |         MONITORING (optional)         |  |
|         |                 |  Prometheus + Grafana (:3000)         |  |
|         v                 +--------------------------------------+  |
|  +--------------+                                                    |
|  |    Docker    |                                                    |
|  |    Daemon    |                                                    |
|  +--------------+                                                    |
+---------------------------------------------------------------------+
```

## Configuration

### User Settings (~/.local/clawker/settings.yaml)

Global defaults that apply across all projects. Local `clawker.yaml` takes precedence.

```yaml
project:
  # Default image when not specified in project config or CLI
  default_image: "node:20-slim"

# Registered project directories (managed by 'clawker init')
projects:
  - /Users/you/Code/project-a
  - /Users/you/Code/project-b

# Logging configuration (all values shown are defaults)
logging:
  file_enabled: true   # Enable file logging
  max_size_mb: 50      # Max size before rotation
  max_age_days: 7      # Days to keep old logs
  max_backups: 3       # Number of rotated files to keep
```

**Log location:** `~/.local/clawker/logs/clawker.log`

Logs are JSON-formatted and include project/agent context when available, making it easy to filter logs when running multiple containers:

```bash
# View recent logs
cat ~/.local/clawker/logs/clawker.log | jq .

# Filter by project
cat ~/.local/clawker/logs/clawker.log | jq 'select(.project == "myapp")'

# Filter by agent
cat ~/.local/clawker/logs/clawker.log | jq 'select(.agent == "ralph")'
```

### Project Config (clawker.yaml)

Project-specific configuration. Run `clawker init` to generate a template.

```yaml
version: "1"
project: "my-app"

build:
  # Base image for the container
  image: "node:20-slim"
  # Optional: path to custom Dockerfile (skips generation)
  # dockerfile: "./.devcontainer/Dockerfile"
  # System packages to install
  packages:
    - git
    - curl
    - ripgrep
  # Build arguments
  # build_args:
  #   NODE_VERSION: "20"
  # Dockerfile instructions
  instructions:
    env:
      NODE_ENV: "production"
    # copy:
    #   - { src: "./config.json", dest: "/etc/app/" }
    # root_run:
    #   - { cmd: "mkdir -p /opt/app" }
    # user_run:
    #   - { cmd: "npm install -g typescript" }
  # Raw Dockerfile injection (escape hatch)
  # inject:
  #   after_from: []
  #   after_packages: []

agent:
  # Files to include in Claude's context
  includes:
    - "./README.md"
    - "./.claude/memory.md"
  # Environment variables for Claude
  env:
    NODE_ENV: "development"
  # Shell, editor, visual settings
  # shell: "/bin/bash"
  # editor: "vim"
  # visual: "vim"

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
  # Host proxy for browser auth (enabled by default)
  # enable_host_proxy: true
  # Git credential forwarding (all enabled by default)
  # git_credentials:
  #   forward_https: true   # Forward HTTPS credentials via host proxy
  #   forward_ssh: true     # Forward SSH agent for git+ssh
  #   copy_git_config: true # Copy host ~/.gitconfig
  # Allowed domains when firewall enabled
  # allowed_domains:
  #   - "api.github.com"
  #   - "registry.npmjs.org"
  # Add Linux capabilities
  # cap_add:
  #   - NET_ADMIN
```

## Security Defaults

| Setting | Default | Why |
|---------|---------|-----|
| Firewall | Enabled | Blocks outbound except allowed domains |
| Docker socket | Disabled | Container can't control Docker |
| Git credentials | Forwarded | Agent access only - keys stay on host |

## The whail Engine

Under the hood, clawker uses **whail** (whale jail) - a reusable Go package that decorates the Docker client with label-based resource isolation.

**What it does:**
- Applies `com.clawker.managed=true` labels during resource creation
- Injects label filters on list/inspect operations
- Refuses to operate on resources it didn't create

This means clawker can never accidentally touch your other Docker resources.

```bash
# Docker sees everything
docker image list
# IMAGE                         ID             ...
# buildpack-deps:bookworm-scm   9be56c3e5291   ...
# clawker-myproject:latest      83a204a19dcb   ...
# postgres:15                   abc123def456   ...

# Clawker only sees its own resources
clawker image list
# IMAGE                    ID            ...
# clawker-myproject:latest 83a204a19dcb  ...
```

`whail` is designed to be reusable for building similar containerized AI agent wrappers.

## Known Issues

### Exiting a container (ctrl+c) pre-authenticating breaks input

If you start a container and try to exit it with ctrl+c before authenticating and accepting use risk warnings, claude code will sometimes hijack the input, not letting you actually exit. Workaround is to detach with ctrl+p, ctrl+q instead, then stop the container with `clawker container stop --agent NAME`.

### Docker MCP Gateway doesn't work inside containers

The Docker MCP Gateway doesn't start inside of containers. This is a known "feature not a bug" situation. see: [https://github.com/docker/mcp-gateway/issues/112#issuecomment-3263238111](https://github.com/docker/mcp-gateway/issues/112#issuecomment-3263238111)

## Contributing

```bash
# Development setup
git clone https://github.com/schmitthub/clawker.git
cd clawker
go build -o ./bin/clawker ./cmd/clawker
go test ./...
```

Issues and PRs welcome at [github.com/schmitthub/clawker](https://github.com/schmitthub/clawker).

## License

MIT
