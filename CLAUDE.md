# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This repository provides Docker containers for running Claude Code in isolated environments with support for multiple programming languages. The setup enables portable development environments with customizable skills, rules, and configurations.

## Architecture

The project uses a layered Docker image approach:
- **Base image** (`dockerfiles/base.Dockerfile`): Alpine Linux + Node.js + Claude Code
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
- Triggers on push to main, pull requests, and manual dispatch
- Builds multi-architecture images (linux/amd64, linux/arm64)
- Uses Docker Buildx for cross-platform builds
- Pushes to DockerHub on main branch commits
- Caches layers for faster builds

Required GitHub Secrets:
- `DOCKERHUB_USERNAME` - Your DockerHub username
- `DOCKERHUB_TOKEN` - DockerHub access token

## License

This project is licensed under the MIT License.
