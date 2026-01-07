# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This repository provides Docker containers for running Claude Code in isolated environments with support for multiple programming languages. The setup enables portable development environments with customizable skills, rules, and configurations.

## Architecture

The project uses a layered Docker image approach:

- **Base image** (`dockerfiles/base.Dockerfile`): Node official image (debian) + Claude Code
- **Language-specific images**: Build from base and add language toolchains
  - `dockerfiles/nodejs.Dockerfile`: Node.js/TypeScript with npm, yarn, pnpm
  - `dockerfiles/python.Dockerfile`: Python with pip, poetry, uv
  - `dockerfiles/go.Dockerfile`: Go with toolchain and common tools
  - `dockerfiles/rust.Dockerfile`: Rust with cargo and rust-analyzer

## Available Image Tags

Images are available on DockerHub (requires `DOCKER_USERNAME` to be set):

- `${DOCKER_USERNAME}/claude-container:base` - Base image with Claude Code
- `${DOCKER_USERNAME}/claude-container:nodejs` - Node.js/TypeScript development
- `${DOCKER_USERNAME}/claude-container:python` - Python development
- `${DOCKER_USERNAME}/claude-container:go` - Go development
- `${DOCKER_USERNAME}/claude-container:rust` - Rust development

Each image also has `-latest` and SHA-tagged versions for version control.

## Building Images Locally

Set your DockerHub username:

```bash
export DOCKER_USERNAME=your-dockerhub-username
```

Build commands:

```bash
make build-all          # Build all images
make build-base         # Build base image only
make build-nodejs       # Build Node.js image
make build-python       # Build Python image
make build-go           # Build Go image
make build-rust         # Build Rust image
```

Push to DockerHub:

```bash
make push-all           # Push all images
make push-nodejs        # Push specific image
```

Test interactively:

```bash
make test-nodejs        # Run Node.js container
make test-python        # Run Python container
```

## Running Containers

Basic usage:

```bash
docker run -v $(pwd):/workspace -it ${DOCKER_USERNAME}/claude-container:nodejs
```

With custom Claude Code configs:

```bash
docker run \
  -v $(pwd):/workspace \
  -v ~/.claude:/root/.claude \
  -it ${DOCKER_USERNAME}/claude-container:nodejs
```

Mount points:

- `/workspace` - Your project code
- `/root/.claude` - Claude Code configurations, skills, and rules

## GitHub Actions Workflow

The `.github/workflows/docker-build.yml` workflow:

- Triggers on pull requests to main, pushes to main, and manual dispatch
- **Optimized PR builds**: Only builds images when their Dockerfiles change
  - If `base.Dockerfile` changes: rebuilds base + all language images
  - If only a language Dockerfile changes: rebuilds only that language image
  - If base didn't change: pulls existing base image from DockerHub for language builds
- PRs use a local registry to share the base image between jobs (linux/amd64 only)
- PR builds do NOT push to DockerHub - only validate that images build successfully
- On merge to main: Always builds ALL multi-architecture images (linux/amd64, linux/arm64) and pushes to DockerHub
- Uses Docker Buildx for cross-platform builds
- Caches layers for faster builds
- Build failures will block PR merges (configure as required status checks in GitHub)

Required GitHub Secrets:

- `DOCKERHUB_USERNAME` - Your DockerHub username
- `DOCKERHUB_TOKEN` - DockerHub access token

### Setting Up Required Status Checks

To prevent merging PRs with failed builds:

1. Go to repository Settings → Branches → Branch protection rules
2. Add a rule for the `main` branch
3. Enable "Require status checks to pass before merging"
4. Select these required checks:
   - `Build Base Image`
   - `Build nodejs Image`
   - `Build python Image`
   - `Build go Image`
   - `Build rust Image`

## License

This project is licensed under the MIT License.
