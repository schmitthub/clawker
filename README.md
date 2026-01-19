# Clawker

<p align="center">
  <a href="https://golang.org"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go" alt="Go"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License"></a>
  <a href="#"><img src="https://img.shields.io/badge/Platform-macOS-lightgrey?logo=apple" alt="macOS"></a>
</p>

Claude Code in YOLO mode can wreak havoc on your system. Setting up Docker manually is tedious - Dockerfiles, volumes, networking. OAuth doesn't work because container localhost isn't host localhost. Git credentials from your keychain don't exist inside containers. And you have no visibility into what's happening.

**Clawker** (claude + docker) wraps Claude Code in Docker containers with a familiar CLI. It handles auth seamlessly via a host proxy, forwards your git credentials, and provides optional monitoring - so you can let Claude Code loose without worrying about your system.

> **Status:** Alpha - macOS tested. Contributions welcome.

## Quick Start

**Prerequisites:** Docker running, Go 1.25+

```bash
# Install
git clone https://github.com/schmitthub/clawker.git
cd clawker && go build -o ./bin/clawker ./cmd/clawker
export PATH="$PWD/bin:$PATH"

# Start a project
cd your-project
clawker init
clawker build
clawker start --agent ralph
```

## Seamless Authentication & Git

**The problem:** Containers have their own localhost. When Claude Code opens a browser for OAuth, the callback goes to the wrong place. Git credentials from your keychain don't exist inside containers.

**The solution:** Clawker runs a lightweight host proxy that bridges the gap.

### Subscription Auth Flow

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

Clawker uses `clawker.yaml` for project configuration. Run `clawker init` to generate a template.

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

**Workaround:** Press any key after re-attaching to trigger a redraw.

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
