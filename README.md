# Clawker

Tired of Claude Code YOLO mode nuking your system from orbit? Resorting to complicated costly cloud setups? Maintaining local backups? Intimitaded by docker? Too lazy to keep creating new dockerfiles for project build dependencies?

Clawker (claude + docker) provides docker resource management and automation of [Claude Code](https://docs.anthropic.com/en/docs/claude-code) in safe, reproducible, monitored, isolated Docker containers using a familiar "docker-like" command line interface.

At its core clawker uses a reusable package, `whail` (whale jail), that decorates a [docker client](https://github.com/moby/moby) to apply management labels during resource creation, and perform management label checks during resource state changes and lookups. This prevents clawker from being able to operate on docker resources it didn't create. The idea is that `whail` might be viable to streamline building clawker-like tools for other AI coding agents.

ex
```bash
[~/Code/clawker]$ docker image list
IMAGE                         ID             DISK USAGE   CONTENT SIZE   EXTRA
buildpack-deps:bookworm-scm   9be56c3e5291        371MB             0B
clawker-clawker:latest        83a204a19dcb       1.95GB             0B

[~/Code/clawker]$ clawker image list
IMAGE                   ID            CREATED         SIZE
clawker-clawker:latest  83a204a19dcb  10 minutes ago  1.81GB
```

If you want to use docker proper without clawker's management, check out `clawker-generate` to generate dockerfiles using clode code npm build tags. Tweak and then `docker build` it yourself. ex `clawker-generate -o dockerfiles/ latest next stable 2.1 1.1`

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

Start the monitoring stack to keep track of resource usage and agent activity across all clawker projects and containers on your system. This is optional but recommended for better visibility. You can view the monitoring dashboard at `http://localhost:3000`.

```bash
clawker monitor start
```

### Option 1: Start claude agent in container and enjoy a safe Claude Code experience

Your workspace files will be bind mounted into the container by default, allowing Claude Code to directly modify your project files only. The rest of your system is isolated from Claude Code.

```bash
clawker start --agent ralph
```

### Option 2: Start claude agent in container with a snapshot of your project

This mode creates a snapshot (ie a copy) of your project files, providing an fully isolated environment for Claude to work in without affecting your actual files. Use git to push and pull changes back to your project as needed.

```bash
clawker start --agent ralph --mode snapshot
```

### Option 3: Run ad-hoc claude commands in container

Run individual Claude commands in the container without starting an interactive agent session. Claude Code commands and flags are passed directly to the container after the image name. This is useful for quick tasks or scripts, but lacks persistence as the container and its volumes are removed after the command completes.

```bash
# Run a prompt (specify image to pass claude's -p flag)
clawker run -it --rm clawker-myproject:latest -p "Fix the bugs in my README.md"

# Run with --allow-dangerously-skip-permissions
clawker run -it --rm clawker-myproject:latest --allow-dangerously-skip-permissions

# Using --agent with default image? Use -- to pass flags to container
clawker run -it --rm --agent ralph -- -p "Fix the bugs"
```

**Note:** Clawker's `-p` flag is for port publishing (like Docker). To use Claude Code's `-p` (prompt) flag, either specify the image name first, or use `--` to stop clawker flag parsing.

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
  # Host proxy for browser auth (enabled by default)
  # enable_host_proxy: true
  # Allowed domains when firewall enabled
  # allowed_domains:
  #   - "api.github.com"
  #   - "registry.npmjs.org"
```

## Host Proxy

Clawker runs a lightweight HTTP proxy on the host that enables containers to perform host-side actions, such as opening URLs in your browser. This is essential for Claude Code's subscription authentication flow.

**How it works:**
- When you run or start a container, clawker automatically starts a host proxy server on port 18374
- The container receives the `CLAWKER_HOST_PROXY` environment variable pointing to the proxy
- The `BROWSER` environment variable is set to `/usr/local/bin/host-open`, which calls the proxy
- When Claude Code needs to open a URL for authentication, it uses the host browser

**Disable host proxy:**

If you don't need browser authentication (e.g., using API keys only), you can disable the host proxy:

```yaml
security:
  enable_host_proxy: false
```

**Manual URL opening:**

From inside a container, you can manually open URLs on the host:

```bash
host-open https://example.com
```
