# Clawker Project Overview

## Purpose

Clawker is a Go CLI tool that wraps the Claude Code agent in secure, reproducible Docker containers. The goal is to create a solution to easily spin up claude code agents in containers to isolate them from damaging the user's host system, especially when running in unsafe unattended modes. You can distill clawker users into two groups

1. **Group Name: Vibers**: Users new to software development and its tooling who have absolutely no understanding what they are doing and yern for a good harness with features to abstract away the complexity of running and monitoring agents using advanced techniques without harming their machines
2. **Group Name: Wizards**: Users who are very experienced with software development and its tooling who would enjoy the convenience of clawker's features

The assumption right now is the majority of users will most likely fall in the first group as `clawker` is an abstraction that simplifies creating and managing containers. `docker` experienced users will be less apt to pursue a solution like this, but may want to adopt for other convenience features like monitoring etc.

**`clawker` should prioritize being intuitive for those new to container management and just want to intuitively run claude code, but do its best to also make docker users feel right at home whenever possible**

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
- `internal/engine/` - Docker SDK abstractions (all methods take `ctx context.Context` as first param)
  - `client.go` - Docker client wrapper (~26 methods with context)
  - `container.go` - ContainerManager (13 methods with context)
  - `labels.go` - Label constants (`LabelManaged`, `LabelProject`, etc.)
  - `names.go` - Container/volume naming, `GenerateRandomName()`
  - `volume.go` - VolumeManager (3 methods with context)
  - `image.go` - ImageManager (context passed through)
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
| `clawker run --agent <name>` | Run container |
| `clawker run --remove --agent <name>` | Run ephemeral container |
| `clawker stop --agent <name>` | Stop specific or all containers |
| `clawker ls -a -p <project>` | List containers |
| `clawker rm -n <name> -p <project>` | Remove containers |
| `clawker logs --agent <name>` | logs for specific agent |
| `clawker run --agent <name> --shell -s /bin/zsh/ --user <claude/root>` | shell for specific agent |
| `clawker monitor init/up/down/status` | Manage observability stack |
