# Clawker

<critical_instructions>

## Required Tooling

### MUST USE

1. **Serena** - Code exploration, symbol search, semantic editing:
   - `initial_instructions` → `check_onboarding_performed` → `list_memories`
   - `search_for_pattern`,`find_symbol`,`get_symbols_overview`,`find_referencing_symbols` for navigation
   - `think_about_collected_information` after research
   - `think_about_task_adherence` before changes
   - `replace_symbol_body`, `insert_after_symbol`,`insert_before_symbol`,`rename_symbol` for edits
   - `think_about_whether_you_are_done` after task
   - `write_memory`, `edit_memory`, `delete_memory` to update memories with current status before completion

2. **Context7** - Library/API docs without explicit requests:
   - `resolve-library-id` first, then `get-library-docs`
   - For: Docker SDK, spf13/cobra, spf13/viper, rs/zerolog, gopkg.in/yaml.v3

3. **ripgrep** - Use `ripgrep` instead of `grep`
4. **exa-search** - When making web searches use `web_search_exa`

### Workflow Requirements

**Planning**: You MUST adhere to @.claude/docs/DESIGN.md

</critical_instructions>

## Repository Structure

```
├── cmd/clawker/              # Main CLI binary
├── internal/
│   ├── build/                 # Image building orchestration
│   ├── clawker/               # Main application lifecycle
│   ├── config/                # Viper config loading + validation
│   ├── credentials/           # Env vars, .env parsing, OTEL
│   ├── docker/                # Clawker-specific Docker middleware (wraps pkg/whail)
│   ├── hostproxy/             # Host proxy server for container-to-host communication
│   ├── monitor/               # Observability stack (Prometheus, Grafana)
│   ├── term/                  # PTY/terminal handling
│   └── workspace/             # Bind vs Snapshot strategies
├── pkg/
│   ├── build/                 # Dockerfile templates, semver, npm registry
│   ├── cmd/                   # Cobra commands organized as:
│   │   ├── container/         # Docker CLI-compatible container management
│   │   ├── volume/            # Volume management
│   │   ├── network/           # Network management
│   │   ├── image/             # Image management
│   │   └── ...                # Top-level shortcuts (run, start, init, build, etc.)
│   ├── cmdutil/               # Factory, error handling, output utilities
│   ├── logger/                # Zerolog setup
│   └── whail/                 # Reusable Docker engine with label-based isolation
└── templates/                 # clawker.yaml scaffolding
```

## Build Commands

```bash
go build -o bin/clawker ./cmd/clawker  # Build CLI
go test ./...                             # Run tests
./bin/clawker --debug run                # Debug logging
./bin/clawker generate latest 2.1        # Generate versions.json
```

## Key Concepts

| Abstraction | Purpose |
|-------------|---------|
| `docker.Client` | Clawker middleware wrapping `whail.Engine` with labels/naming |
| `whail.Engine` | Reusable Docker engine with label-based resource isolation |
| `WorkspaceStrategy` | Bind (live mount) vs Snapshot (ephemeral copy) |
| `PTYHandler` | Raw terminal mode, bidirectional streaming |
| `ContainerConfig` | Labels, naming (`clawker.project.agent`), volumes |
| `hostproxy.Manager` | Host proxy server for container-to-host actions (e.g., opening URLs) |
| `hostproxy.SessionStore` | Generic session management for proxy channels |
| `hostproxy.CallbackChannel` | OAuth callback interception and forwarding |

See @.claude/docs/ARCHITECTURE.md for detailed abstractions.

## Host Proxy Architecture

The host proxy (`internal/hostproxy`) is a service mesh that mediates interactions between containers and the host.

### Components

| Component | File | Purpose |
|-----------|------|---------|
| `Server` | `server.go` | HTTP server handling proxy requests |
| `SessionStore` | `session.go` | Generic session management with TTL and cleanup |
| `CallbackChannel` | `callback.go` | OAuth callback registration, capture, and retrieval |
| `Manager` | `manager.go` | Lifecycle management of the proxy server |

### OAuth Callback Flow

```
CONTAINER                              HOST PROXY (:18374)                    BROWSER
    │                                         │                                  │
    │ 1. Claude Code starts auth server       │                                  │
    │                                         │                                  │
    │ 2. host-open detects OAuth URL ────────►│                                  │
    │    POST /callback/register              │                                  │
    │         │                               │                                  │
    │         │◄────────────────────────────── │ Returns session_id              │
    │         │                               │                                  │
    │    Rewrites callback URL ───────────────┼─────────────────────────────────►│
    │                                         │              3. Opens in browser │
    │                                         │                                  │
    │                                         │◄─────────────────────────────────│
    │                                         │ 4. Redirect to proxy callback    │
    │                                         │    GET /cb/SESSION/callback      │
    │                                         │                                  │
    │    callback-forwarder polls ───────────►│                                  │
    │    GET /callback/SESSION/data           │                                  │
    │         │                               │                                  │
    │         │◄────────────────────────────── │ Returns callback data           │
    │         │                               │                                  │
    │ 5. Forwards to localhost:PORT           │                                  │
    │    Claude Code receives callback!       │                                  │
```

### API Endpoints

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/open/url` | POST | Open URL in host browser |
| `/health` | GET | Health check |
| `/callback/register` | POST | Register OAuth callback session |
| `/callback/{session}/data` | GET | Poll for captured callback data |
| `/callback/{session}` | DELETE | Cleanup session |
| `/cb/{session}/{path...}` | GET | Receive OAuth callbacks from browser |

### Container Scripts

| Script | Purpose |
|--------|---------|
| `host-open` | Opens URLs, detects OAuth flows, rewrites callbacks |
| `callback-forwarder` | Polls proxy and forwards callbacks to local server |

## Code Style

- **Logging**: `zerolog` only (never `fmt.Print` for debug)
- **User output**: `cmdutil.PrintError()`, `cmdutil.PrintNextSteps()` to stderr
- **Data output**: stdout only for scripting (e.g., `ls` table)
- **Errors**: `cmdutil.HandleError(err)` for Docker errors
- **Cobra**: `PersistentPreRunE` (never `PersistentPreRun`), always include `Example` field

```go
cmd := &cobra.Command{
    Use:     "mycommand",
    Short:   "One-line description",
    Example: `  clawker mycommand
  clawker mycommand --flag`,
    RunE:    func(cmd *cobra.Command, args []string) error { ... },
}
```

## CLI Commands

See @.claude/docs/CLI-VERBS.md for complete command reference.

### Top-Level Shortcuts
| Command | Description |
|---------|-------------|
| `init`, `build` | Project setup |
| `run`, `start` | Aliases for `container run`, `container start` |
| `config check`, `monitor *` | Configuration/observability |
| `generate` | Generate versions.json for releases |

### Management Commands (Docker CLI-compatible)

| Command Group | Description |
|---------------|-------------|
| `container *` | Container lifecycle, inspection, interaction |
| `volume *` | Volume management |
| `network *` | Network management |
| `image *` | Image management |

Example container commands:
- `clawker container list` (aliases: `ls`, `ps`)
- `clawker container run/create/start/stop/restart/kill`
- `clawker container logs/inspect/top/stats`
- `clawker container exec/attach/cp`
- `clawker container pause/unpause/rename/wait/update`
- `clawker container remove` (alias: `rm`)

These commands use positional arguments for resource names (e.g., `clawker container stop clawker.myapp.ralph`) matching Docker's interface.

## Configuration

### User Settings (~/.local/clawker/settings.yaml)

User-level defaults that apply across all projects:

```yaml
project:
  default_image: "node:20-slim"  # Default image for container create/run
projects: []  # Managed by 'clawker init'
```

### Project Config (clawker.yaml)

```yaml
version: "1"
project: "my-app"

build:
  image: "buildpack-deps:bookworm-scm"
  packages: ["git", "ripgrep"]
  instructions:
    env: { NODE_ENV: "production" }
    copy: [{ src: "./config.json", dest: "/etc/app/" }]
    root_run: [{ cmd: "mkdir -p /opt/app" }]
    user_run: [{ cmd: "npm install -g typescript" }]
  inject:          # Raw Dockerfile injection (escape hatch)
    after_from: []
    after_packages: []

agent:
  includes: ["./docs/architecture.md"]
  env: { NODE_ENV: "development" }

workspace:
  remote_path: "/workspace"
  default_mode: "snapshot"

security:
  enable_firewall: true
  docker_socket: false
```

**Key types** (internal/config/schema.go): `DockerInstructions`, `InjectConfig`, `RunInstruction`, `CopyInstruction`

## Design Decisions

1. Firewall enabled by default
2. Docker socket disabled by default
3. `run` and `start` are aliases for `container run` (Docker CLI pattern)
4. Hierarchical naming: `clawker.project.agent`
5. Labels (`com.clawker.*`) are authoritative for filtering
6. stdout for data, stderr for status

## Important Gotchas

- `os.Exit()` does NOT run deferred functions - restore terminal state explicitly
- Raw terminal mode: Ctrl+C goes to container, not as SIGINT
- Never use `logger.Fatal()` in Cobra hooks - return errors instead
- Don't wait for stdin goroutine on container exit (may block on Read)
- Docker hijacked connections need cleanup of both read and write sides

## Context Management (Critical)

**NEVER** store `context.Context` in struct fields. This is an antipattern that breaks cancellation and timeouts.

```go
// ❌ WRONG - Static context antipattern
type Engine struct {
    ctx context.Context  // DO NOT DO THIS
}

// ✅ CORRECT - Per-operation context
func (e *Engine) ContainerStart(ctx context.Context, id string) error {
    return e.cli.ContainerStart(ctx, id, container.StartOptions{})
}
```

All `pkg/whail` and `internal/docker` methods accept `ctx context.Context` as their first parameter:

- `whail.Engine`: `ContainerCreate(ctx, ...)`, `ContainerStart(ctx, ...)`, `ContainerStop(ctx, ...)`, etc.
- `docker.Client`: Wraps `whail.Engine` with clawker labels, same context pattern

For cleanup in deferred functions, use `context.Background()` since the original context may be cancelled:

```go
defer func() {
    cleanupCtx := context.Background()
    client.ContainerRemove(cleanupCtx, containerID, true)
}()
```

See `context_management` memory for detailed patterns and examples.

## Testing Requirements

**CRITICAL: All tests must pass before any change is complete.**

```bash
# Unit tests (fast, no Docker required)
go test ./...

# Integration tests (requires Docker)
go test ./pkg/cmd/... -tags=integration -v -timeout 10m
```

See @.claude/rules/TESTING.md for detailed testing guidelines.

## Documentation

| File | Purpose |
|------|---------|
| @.claude/docs/CLI-VERBS.md | CLI command reference with flags and examples |
| @.claude/docs/ARCHITECTURE.md | Detailed abstractions and interfaces |
| @.claude/docs/CONTRIBUTING.md | Adding commands, updating docs |
| @.claude/rules/TESTING.md | CLI testing guildelines (**only access when writing command tests**) |
| @README.md | Marketing doc to give an exciting summary, quick start, etc. This document is not to serve as technical documentation that overwhelms the user with a wall of text, it should be fun and to the point with the bare minimum basics on how to get started fixing the problems clawker solves|

**Critical**: After code changes, update README.md (user-facing) and CLAUDE.md (developer-facing) and memories (serena) as appropriate.
