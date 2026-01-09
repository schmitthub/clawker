# Claucker Project Overview

## Purpose
Claucker is a Go CLI tool that wraps the Claude Code agent in secure, reproducible Docker containers. Core philosophy: "Safe Autonomy" - host system is read-only by default.

## Tech Stack
- **Language**: Go
- **CLI Framework**: Cobra
- **Logging**: Zerolog
- **Configuration**: Viper (YAML)
- **Container Runtime**: Docker SDK

## Key Packages
- `cmd/` - Cobra commands (init, up, down, sh, logs)
- `internal/config/` - Viper configuration loading
- `internal/engine/` - Docker SDK abstractions
- `internal/workspace/` - Bind vs Snapshot strategies
- `internal/dockerfile/` - Dynamic Dockerfile generation
- `internal/term/` - PTY/terminal handling
- `internal/credentials/` - .env parsing and injection
- `pkg/logger/` - Zerolog setup
- `build/templates/` - Docker image templates
