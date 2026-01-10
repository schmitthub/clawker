# Claucker Project Overview

## Purpose
Claucker is a Go CLI tool that wraps the Claude Code agent in secure, reproducible Docker containers. Core philosophy: "Safe Autonomy" - host system is read-only by default.

## Tech Stack
- **Language**: Go
- **CLI Framework**: Cobra
- **Logging**: Zerolog
- **Configuration**: Viper (YAML)
- **Container Runtime**: Docker SDK

## Multi-Container Support
Claucker supports multiple containers per project using **agents**:
- Container naming: `claucker.project.agent` (e.g., `claucker.myapp.ralph`)
- Volume naming: `claucker.project.agent-purpose` (e.g., `claucker.myapp.ralph-workspace`)
- Docker labels (`com.claucker.*`) enable reliable filtering and identification
- Random agent names generated if `--agent` flag not specified

## Key Packages
- `cmd/claucker/` - Main entry point
- `pkg/cmd/` - Cobra commands (start, run, stop, ls, rm, sh, logs, etc.)
- `internal/config/` - Viper configuration loading and validation
- `internal/engine/` - Docker SDK abstractions
  - `client.go` - Docker client wrapper, container listing
  - `container.go` - ContainerManager, ContainerConfig
  - `labels.go` - Label constants (`LabelManaged`, `LabelProject`, etc.)
  - `names.go` - Container/volume naming, `GenerateRandomName()`
  - `volume.go` - VolumeManager
- `internal/workspace/` - Bind vs Snapshot strategies
- `internal/dockerfile/` - Dynamic Dockerfile generation
- `internal/term/` - PTY/terminal handling
- `internal/credentials/` - .env parsing and injection
- `pkg/logger/` - Zerolog setup
- `pkg/cmdutil/` - Factory pattern for command dependencies

## CLI Commands
| Command | Purpose |
|---------|---------|
| `claucker start --agent <name>` | Start named container |
| `claucker run --agent <name>` | Run ephemeral container |
| `claucker stop --agent <name>` | Stop specific or all containers |
| `claucker ls -a -p <project>` | List containers |
| `claucker rm -n <name> -p <project>` | Remove containers |
| `claucker sh/logs --agent <name>` | Shell/logs for specific agent |
