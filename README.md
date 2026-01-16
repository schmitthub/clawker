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
