# Claucker - Claude Container Orchestration

## Project Overview

Claucker is a Go CLI tool that wraps the Claude Code agent in secure, reproducible Docker containers.
Core philosophy: "Safe Autonomy" - host system is read-only by default.

## Repository Structure

```
/workspace/
├── claucker/              # Go CLI source code
│   ├── cmd/               # Cobra commands (init, up, down, sh, logs)
│   ├── internal/          # Private packages
│   │   ├── config/        # Viper configuration loading
│   │   ├── engine/        # Docker SDK abstractions
│   │   ├── workspace/     # Bind vs Snapshot strategies
│   │   ├── dockerfile/    # Dynamic Dockerfile generation
│   │   ├── term/          # PTY/terminal handling
│   │   └── credentials/   # .env parsing and injection
│   └── pkg/logger/        # Zerolog setup
├── build/templates/       # Docker image templates
│   ├── Dockerfile.template
│   ├── docker-entrypoint.sh
│   └── docker-init-firewall.sh
├── dockerfiles/           # Generated Dockerfiles (do not edit)
└── .devcontainer/         # Development container config
```

## Build Commands

```bash
# Build the CLI
cd claucker && go build -o bin/claucker .

# Run tests
cd claucker && go test ./...

# Run with debug logging
./bin/claucker --debug up

# Generate Docker images (existing infrastructure)
make build VERSION=2.1.2 VARIANT=trixie
```

## Key Abstractions

### WorkspaceStrategy Interface
Two implementations: BindStrategy (live host mount) and SnapshotStrategy (ephemeral volume copy).

### DockerEngine
Wraps Docker SDK with user-friendly errors including "Next Steps" guidance.

### PTYHandler
Manages raw terminal mode and bidirectional streaming for interactive Claude sessions.

## Code Style

- Use `zerolog` for all logging (never fmt.Print for debug)
- Errors must include actionable "Next Steps" for users
- Follow standard Go project layout (cmd/, internal/, pkg/)
- Use interfaces for testability (especially Docker client)

## Common Tasks

### Adding a new CLI command
1. Create `cmd/newcmd.go`
2. Define cobra.Command with Run function
3. Register in `cmd/root.go` init()

### Modifying Dockerfile generation
1. Edit templates in `claucker/templates/`
2. Update `internal/dockerfile/generator.go`

### Testing container operations
Integration tests require Docker daemon. Use `go test -short` to skip.

## CLI Commands

| Command | Description |
|---------|-------------|
| `claucker init` | Scaffold `claucker.yaml` and `.clauckerignore` |
| `claucker up [--mode=bind\|snapshot]` | Build image, create volume, run container, attach TTY |
| `claucker down [--clean]` | Stop containers; `--clean` destroys volumes |
| `claucker sh` | Open raw bash shell in running container |
| `claucker logs [-f]` | Stream container logs |
| `claucker config check` | Validate `claucker.yaml` |

## Configuration (claucker.yaml)

```yaml
version: "1"
project: "my-app"

build:
  image: "node:20-slim"
  packages: ["git", "ripgrep", "make"]

agent:
  includes: ["./docs/architecture.md"]
  env:
    NODE_ENV: "development"

workspace:
  remote_path: "/workspace"
  default_mode: "snapshot"

security:
  enable_firewall: true
  docker_socket: false
```

## Design Decisions

1. **Firewall enabled by default** - Network isolation for security
2. **Docker socket disabled by default** - Opt-in for Docker-in-Docker
3. **Config volume preserved by default** - Use `--clean` to remove all
4. **Idempotent `up` command** - Attaches to existing container if running
