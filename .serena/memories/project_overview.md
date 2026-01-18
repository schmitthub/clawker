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

## Configuration

### User Settings (~/.local/clawker/settings.yaml)
User-level settings that apply across all projects:
- `project.default_image` - Default image for container create/run
- `projects` - List of registered project directories (managed by `clawker init`)

### Project Config (clawker.yaml)
Project-specific configuration. Local project config takes precedence over user settings.
- `default_image` - Project-specific default image (overrides user settings)

### Image Resolution Order
For `container create` and `container run`, image is resolved in this order:
1. Explicit IMAGE argument from CLI
2. `default_image` from project's clawker.yaml
3. `default_image` from user settings
4. Project image with :latest tag (by label lookup)

## Key Packages

### Architecture (Post-Migration)
```
cmd/clawker → pkg/cmd/* → internal/docker → pkg/whail → Docker SDK
```

- `cmd/clawker/` - Main entry point
- `pkg/cmd/` - Cobra commands organized as:
  - Top-level shortcuts: `run`, `start`, `init`, `build`, `config`, `monitor`, `generate`
  - Management commands: `container/*`, `volume/*`, `network/*`, `image/*`
- `pkg/whail/` - **Reusable** Docker engine library with label-based isolation
  - `engine.go` - Core Engine with configurable labels
  - `container.go` - All container operations (Create, Start, Stop, Kill, Pause, etc.)
  - `volume.go` - Volume operations with label injection
  - `network.go` - Network operations with label injection
  - `image.go` - Image operations with label injection
  - `copy.go` - File copy operations (CopyToContainer, CopyFromContainer)
  - `errors.go` - Generic Docker errors (22+ types)
  - `labels.go` - Label utilities
- `internal/docker/` - **Clawker-specific** thin layer over whail
  - `client.go` - Client wrapper configuring whail with clawker labels
  - `labels.go` - Clawker label constants (`com.clawker.*`)
  - `names.go` - Container/volume naming (`clawker.project.agent`)
  - `volume.go` - Volume helpers (EnsureVolume, CopyToVolume)
- `internal/config/` - Viper configuration loading and validation
- `internal/workspace/` - Bind vs Snapshot strategies
- `internal/build/` - Image building orchestration
- `internal/credentials/` - .env parsing, EnvBuilder, OTEL injection
- `internal/monitor/` - Observability stack (Prometheus, Grafana, OTel)
- `internal/term/` - PTY/terminal handling
- `pkg/build/` - Version generation, Dockerfile templates, and ProjectGenerator
- `pkg/logger/` - Zerolog setup
- `pkg/cmdutil/` - Factory pattern with `Client(ctx)` for lazy docker.Client

## CLI Commands

### Top-Level Shortcuts
| Command | Purpose |
|---------|---------|
| `clawker init` | Initialize project configuration |
| `clawker build` | Build container image |
| `clawker run` | Alias for `container run` |
| `clawker start` | Alias for `container start` |
| `clawker config check` | Validate configuration |
| `clawker monitor init/up/down/status` | Manage observability stack |
| `clawker generate` | Generate versions.json for releases |

### Management Commands (Docker CLI-style)
| Command | Purpose |
|---------|---------|
| `clawker container list` | List containers (aliases: ls, ps) |
| `clawker container run` | Create and run container |
| `clawker container create` | Create container without starting |
| `clawker container start/stop/restart` | Lifecycle management |
| `clawker container kill/pause/unpause` | Process control |
| `clawker container logs/inspect/top/stats` | Inspection |
| `clawker container exec/attach/cp` | Interactive operations |
| `clawker container remove` | Remove containers (alias: rm) |
| `clawker volume list/create/inspect/remove/prune` | Volume management |
| `clawker network list/create/inspect/remove/prune` | Network management |
| `clawker image list/inspect/remove/build/prune` | Image management |
