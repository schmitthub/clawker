# Clawker Project Overview

## Purpose

Clawker is a Go CLI tool that wraps the Claude Code agent in secure, reproducible Docker containers. Core philosophy: "Safe Autonomy" - host system is read-only by default.

## Tech Stack

- **Language**: Go
- **CLI Framework**: Cobra
- **Logging**: Zerolog
- **Configuration**: Viper (YAML)
- **Container Runtime**: Docker SDK

## Multi-Container Support

Clawker supports multiple containers per project using **agents**:

- Container naming: `clawker.project.agent` (e.g., `clawker.myapp.ralph`)
- Volume naming: `clawker.project.agent-purpose` (e.g., `clawker.myapp.ralph-workspace`)
- Docker labels (`com.clawker.*`) enable reliable filtering and identification
- Random agent names generated if `--agent` flag not specified

## Key Packages

- `cmd/clawker/` - Main entry point
- `pkg/cmd/` - Cobra commands (start, run, stop, ls, rm, sh, logs, etc.)
- `internal/config/` - Viper configuration loading and validation
- `internal/engine/` - Docker SDK abstractions
  - `client.go` - Docker client wrapper, container listing
  - `container.go` - ContainerManager, ContainerConfig
  - `labels.go` - Label constants (`LabelManaged`, `LabelProject`, etc.)
  - `names.go` - Container/volume naming, `GenerateRandomName()`
  - `volume.go` - VolumeManager
- `internal/workspace/` - Bind vs Snapshot strategies
- `internal/credentials/` - .env parsing, EnvBuilder, OTEL injection
- `internal/monitor/` - Observability stack (Prometheus, Grafana, OTel)
- `pkg/build/` - Version generation, Dockerfile templates, and ProjectGenerator
- `internal/term/` - PTY/terminal handling
- `pkg/logger/` - Zerolog setup
- `pkg/cmdutil/` - Factory pattern for command dependencies

## CLI Commands

| Command | Purpose |
|---------|---------|
| `clawker start --agent <name>` | Start named container |
| `clawker run --agent <name>` | Run ephemeral container |
| `clawker stop --agent <name>` | Stop specific or all containers |
| `clawker ls -a -p <project>` | List containers |
| `clawker rm -n <name> -p <project>` | Remove containers |
| `clawker sh/logs --agent <name>` | Shell/logs for specific agent |
| `clawker monitor init/up/down/status` | Manage observability stack |
