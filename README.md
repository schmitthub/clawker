# Clawker

<p align="center">
  <a href="https://golang.org"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go" alt="Go"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License"></a>
  <a href="#"><img src="https://img.shields.io/badge/Platform-macOS-lightgrey?logo=apple" alt="macOS"></a>
</p>

Claude Code in YOLO mode can wreak havoc on your system. Setting up Docker manually is tedious - Dockerfiles, volumes, networking. OAuth doesn't work because container localhost isn't host localhost. Git credentials from your keychain don't exist inside containers. And you have no visibility into what's happening.

**Clawker** (claude + docker) wraps Claude Code in Docker containers with a familiar CLI. It handles auth seamlessly via a host proxy, forwards your git credentials, and provides optional monitoring - so you can let Claude Code loose without worrying about your system.

> **Status:** Alpha - macOS tested. Please report issues and contributions welcome! If you find clawker useful, please star the repo!
> **Planned features and fixes:**
> * Linux support not yet tested but might work
> * Windows support
> * Wiggum and worktree example scripts / commands
> * Versioning and releases with CI/CD integration
> * File logging
> * Terminal UI improvements (redraw on reattach, status indicators, progress bars, styling), everything is just raw output that often can conflict with what is already on the terminal, especially Claude's Ink-based TUI (see Known Issues)
> * Auto pruning to manage disk usage
> * Man pages and helper docs
> * Docker MCP Toolkit Support (currently there is a known "feature not a bug" where the mcp plugin inside of a container won't work because it doesn't detect docker desktop)
> * Host proxy to open browser won't work if you detach and then re-attach before authenticating (low priority)
> * Grafana pre-built dashboard improvements, right now it's very basic for POC purposes
> * Improved host claude directory mounting strategy to avoid permission issues and settings

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
clawker build
clawker start --agent ralph
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

## Workflows

### Starting containers

```bash
# Bind mode (default) - changes sync to host immediately
clawker start --agent dev

# Snapshot mode - isolated copy, use git to sync
clawker start --agent sandbox --mode snapshot
```

### Passing Claude Code options

```bash
# Run with a prompt
clawker run -it --rm myimage:latest -p "Fix the tests"

# Skip permission prompts (careful!)
clawker run -it --rm myimage:latest --dangerously-skip-permissions

# Using --agent? Use -- to separate clawker flags from Claude flags
clawker run -it --rm --agent ralph -- -p "Refactor auth module"
```

### Detach and reattach

```bash
# Detach from running container: Ctrl+P, Ctrl+Q

# Reattach later
clawker container attach --agent ralph
# or
clawker container attach clawker.myproject.ralph

# Note: Press any key after reattach to redraw Claude's TUI
```

### List and manage containers

```bash
clawker container ls              # List all clawker containers
clawker container stop --agent ralph
clawker container logs --agent ralph --follow
```

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

### Claude Code TUI doesn't redraw after re-attach

When you detach from a container (Ctrl+P, Ctrl+Q) and re-attach, Claude Code's terminal UI may appear blank or frozen. This is a **Claude Code limitation** (its Ink-based React terminal renderer), not a clawker or Docker issue.

**Workaround:** Press any key after re-attaching to trigger a redraw, eventually the terminal will work itself out.

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
