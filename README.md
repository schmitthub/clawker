# Clawker

Tired of: Claude Code YOLO mode nuking your system from orbit? Having to resort to YOLO in complicated cloud setups? Mainting backups? Intimitaded by docker? Too lazy to keep creating new dockerfiles for project build dependencies?

Clawker (claude + docker) provides docker resource management and automation of [Claude Code](https://docs.anthropic.com/en/docs/claude-code) in safe, reproducible, monitored, isolated Docker containers using a familiar "docker-like" command line interface.

At its core clawker uses a reusable package, `whail` (whale jail), that decorates a [docker client](https://github.com/moby/moby) to apply management labels during resource creation, and perform management label checks during resource state changes and lookups. This prevents clawker from being able to operate on unrelated docker resources from other projects. The idea is that `whail` might be viable to streamline building clawker-like tools for other AI coding agents.

**Disclaimer** `clawker` is currently a WIP I have been building for myself ad-hoc to suit my personal needs. Currently only tested on MacOS. Feel free to report issues, feature requests, or make contributions. If enough people are enjoying `clawker` I'll give it more time and setup proper releases, OS support, and features.

## Quick Start

### Prerequisites

- Docker installed and running
- Go 1.25+ (for building from source)

### Installation

Currently, there are no pre-built binaries. You can build `clawker` from source:

### Step 1: Clone

```shell
git clone https://github.com/schmitthub/clawker.git
cd clawker
```

### Step 2: Build from source

```shell
# local binary
go build -o ./bin/clawker ./cmd/clawker

# optional move somewhere in $PATH

# system
mv ./bin/clawker /usr/local/bin/clawker

# user
mkdir -p ~/.local/bin
export PATH="$HOME/.local/bin:$PATH"
mv ./bin/clawker ~/.local/bin/clawker
```

## Workflow

### Initialize Project

```bash
cd my-project
clawker init
```

### Review Config / Customize Project Image

Open `clawker.yaml` and customize the project image, packages, and agent settings as needed. See the Configuration section below for more details.

### Optional: Start monitoring stack

Start the monitoring stack to keep track of resource usage and agent activity across all clawker projects on your system. This is optional but recommended for better visibility. You can view the monitoring dashboard at `http://localhost:3000`.

```bash
clawker monitor start
```

### Option 1: Start claude agent in container and enjoy a safe Claude Code experience

Your workspace files will be bind mounted into the container by default, allowing Claude to access your code without nuking your system.

```bash
clawker start --agent ralph
```

### Option 2: Start claude agent in container with a snapshot of your workspace

This mode creates a snapshot of your workspace files and mounts it into the container, providing an isolated environment for Claude to work in without affecting your actual files. This is useful for testing changes or experiments without risking your real codebase, especially when using YOLO mode. Use git to push and pull changes back to your project as needed.

```bash
clawker start --agent ralph --mode snapshot
```

### Option 3: Run ad-hoc claude commands in container

Run individual Claude commands in the container without starting an interactive agent session. Claude code commands and flags are passed directly to the container's claude-code CLI. This is useful for quick tasks or scripts, but lacks the persistence as the container and its volumes are removed after the command completes.

```bash
clawker run --p "Fix the bugs in my README.md"
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
