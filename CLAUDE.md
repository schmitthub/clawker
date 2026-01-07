# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This repository provides Docker containers for running Claude Code in isolated environments with support for multiple programming languages. The setup enables portable development environments with customizable skills, rules, and configurations.

Key features:
- **Debian Bookworm base**: Modern, stable Linux distribution
- **Multi-language support**: Node.js, Python, Go, and Rust toolchains
- **Network isolation**: Optional firewall for controlled outbound access
- **Enhanced shell**: zsh with Oh My Zsh for improved developer experience
- **Smart entry point**: Automatic command wrapping via docker-entrypoint.sh

## Architecture

The project uses a **single multi-stage Dockerfile** at `claude-container/Dockerfile` with all build targets defined in one file:

- **Base stage** (`--target base`):
  - Debian Bookworm foundation
  - Claude Code installed globally via npm
  - Node.js and npm (from Debian repositories)
  - Development tools: git, zsh, Oh My Zsh (agnoster theme), git-delta, fzf
  - Security: init-firewall.sh for network isolation
  - Smart wrapper: docker-entrypoint.sh for command handling
  - User: `claude` (uid 1001, gid 1001)
  - Working directory: `/workspace`

- **Language-specific stages** (extend base):
  - **Node stage** (`--target node`): Adds yarn and pnpm package managers
  - **Python stage** (`--target python`): Adds Python 3, pip, poetry, and uv
  - **Go stage** (`--target go`): Adds Go toolchain from Debian repositories
  - **Rust stage** (`--target rust`): Adds Rust via rustup with cargo, rust-analyzer, and clippy

All images are built from the same Dockerfile using different `--target` flags.

## Available Image Tags

Images are available on DockerHub (requires `DOCKER_USERNAME` to be set):

- `${DOCKER_USERNAME}/claude-container:base` - Base image with Claude Code
- `${DOCKER_USERNAME}/claude-container:node` - Node.js/TypeScript development
- `${DOCKER_USERNAME}/claude-container:python` - Python development
- `${DOCKER_USERNAME}/claude-container:go` - Go development
- `${DOCKER_USERNAME}/claude-container:rust` - Rust development

Each image also has `-latest` and SHA-tagged versions for version control.

## Building Images Locally

Set your DockerHub username:

```bash
export DOCKER_USERNAME=your-dockerhub-username
```

Build commands using the consolidated Dockerfile:

```bash
# Build all images
make build-all

# Build specific images (uses claude-container/Dockerfile with --target flag)
make build-base         # Base image only
make build-node         # Node.js image
make build-python       # Python image
make build-go           # Go image
make build-rust         # Rust image
```

Manual build examples:

```bash
# Base image
docker build -t ${DOCKER_USERNAME}/claude-container:base \
  --target base \
  -f claude-container/Dockerfile .

# Node.js image
docker build -t ${DOCKER_USERNAME}/claude-container:node \
  --target node \
  -f claude-container/Dockerfile .

# Python image
docker build -t ${DOCKER_USERNAME}/claude-container:python \
  --target python \
  -f claude-container/Dockerfile .
```

Push to DockerHub:

```bash
make push-all           # Push all images
make push-node          # Push specific image
make push-python
make push-go
make push-rust
```

Test interactively:

```bash
make test-node          # Run Node.js container
make test-python        # Run Python container
make test-go            # Run Go container
make test-rust          # Run Rust container
```

## Running Containers

Basic usage:

```bash
docker run -v $(pwd):/workspace -it ${DOCKER_USERNAME}/claude-container:node
```

With custom Claude Code configs:

```bash
docker run \
  -v $(pwd):/workspace \
  -v ~/.claude:/home/claude/.claude \
  -it ${DOCKER_USERNAME}/claude-container:node
```

Mount points:

- `/workspace` - Your project code (working directory)
- `/home/claude/.claude` - Claude Code configurations, skills, and rules

### Entry Point Wrapper

The `docker-entrypoint.sh` script automatically wraps commands with `claude` when needed:

- Running `docker run image npm install` becomes `claude npm install` internally
- Commands starting with `claude` are passed through unchanged
- Default command: `claude` (interactive mode)

This allows seamless integration of development tools with Claude Code.

## Security & Network Isolation

### Firewall Feature

The containers include `init-firewall.sh` which sets up iptables rules for network isolation. When enabled, it:

**Allowed domains:**
- GitHub (github.com, api.github.com, objects.githubusercontent.com)
- npm registry (registry.npmjs.org)
- Anthropic API (api.anthropic.com)
- Statsig (api.statsig.com)
- VS Code Marketplace (marketplace.visualstudio.com)

**Blocked:**
- All other outbound connections

**Enabling the firewall:**

The firewall can be initialized with proper capabilities:

```bash
docker run --cap-add=NET_ADMIN \
  -v $(pwd):/workspace \
  -it ${DOCKER_USERNAME}/claude-container:node \
  bash -c "sudo /usr/local/bin/init-firewall.sh && claude"
```

**Testing the firewall:**

```bash
# Should be blocked
curl example.com

# Should work
curl https://api.github.com
curl https://registry.npmjs.org
```

**Note:** Firewall requires `--cap-add=NET_ADMIN` and must be run as root. The claude user has passwordless sudo access for the firewall script only.

## Technical Details

- **Base OS**: Debian Bookworm (bookworm)
- **User**: `claude` (uid: 1001, gid: 1001)
- **Working Directory**: `/workspace`
- **Shell**: zsh with Oh My Zsh (agnoster theme)
- **Entry Point**: `/usr/local/bin/docker-entrypoint.sh`
- **Default Command**: `claude` (interactive mode)
- **Multi-architecture Support**:
  - `linux/amd64` (Intel/AMD x86_64)
  - `linux/arm64` (Apple Silicon, AWS Graviton)

### Installed Tools (Base Image)

- **Claude Code**: Latest version via npm (@anthropic-ai/claude-code)
- **Node.js & npm**: From Debian repositories
- **Shell**: zsh, bash
- **Git**: With git-delta for enhanced diffs
- **Development**: fzf, less, vim, nano, jq, gh (GitHub CLI)
- **Network**: iptables, ipset, iproute2, dnsutils for firewall functionality
- **Other**: procps, sudo, unzip, gnupg2, ca-certificates

## GitHub Actions Workflow

⚠️ **Current Status**: The GitHub Actions workflow is being refactored from `.github/workflows/docker-build.yml` to `.github/workflows/build-test.yml`.

### Planned Workflow Behavior

The new workflow will implement selective building to optimize CI/CD performance:

#### PR Builds (Selective)
- **Change Detection**: Detects which parts of `claude-container/Dockerfile` changed
- **Selective Building**: Only builds affected images
  - If `base` stage changes: Rebuilds base + all language images (dependency)
  - If only a language stage changes: Rebuilds only that specific image
  - If `init-firewall.sh` or `docker-entrypoint.sh` changes: Rebuilds all images
- **Platform**: `linux/amd64` only (faster validation)
- **Registry**: Uses local registry to share base image between jobs
- **Push**: Does NOT push to DockerHub (validation only)
- **Blocking**: Build failures block PR merges

#### Main Branch Builds (Always All)
- **Trigger**: Push to main or merge
- **Scope**: Always builds ALL images (base, node, python, go, rust)
- **Platforms**: `linux/amd64` and `linux/arm64` (multi-architecture)
- **Push**: Pushes all images to DockerHub
- **Tags**:
  - `latest` - Most recent build
  - `<git-sha>` - SHA-tagged for version control

#### Matrix Generation

The workflow uses `genMatrix.js` to generate the build matrix based on changed files.

**Note**: The current `genMatrix.js` is from the Node.js Docker repository and needs to be rewritten for this project. The new version should:
- Detect changes in `claude-container/Dockerfile`
- Parse which build stages were modified (base, node, python, go, rust)
- Generate matrix entries for affected stages
- Handle base stage dependency (if base changes, include all stages)

### Required GitHub Secrets

- `DOCKERHUB_USERNAME` - Your DockerHub username
- `DOCKERHUB_TOKEN` - DockerHub access token

### Setting Up Required Status Checks

To prevent merging PRs with failed builds:

1. Go to repository Settings → Branches → Branch protection rules
2. Add a rule for the `main` branch
3. Enable "Require status checks to pass before merging"
4. Select these required checks:
   - `Build base Image` (or equivalent check names from workflow)
   - `Build node Image`
   - `Build python Image`
   - `Build go Image`
   - `Build rust Image`

**Note**: Exact check names will be finalized once `build-test.yml` is complete.

## Related Work Items

After completing the workflow refactoring, these items need attention:

1. **Makefile**: Update all build targets to consistently reference `claude-container/Dockerfile`
2. **genMatrix.js**: Rewrite for this project's structure (currently from Node.js Docker repo)
3. **build-test.yml**: Complete the workflow implementation
4. **README.md**: Update to reflect Debian Bookworm base and consolidated architecture

## License

This project is licensed under the MIT License.
