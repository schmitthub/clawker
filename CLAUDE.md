# Clawker

Clawker is a Go CLI tool that wraps the Claude Code agent in secure, reproducible Docker containers. The goal is to create a solution to easily spin up claude code agents in containers to isolate them from damaging the user's host system, especially when running in unsafe unattended modes. You can distill clawker users into two groups

1. **Group Name: Vibers**: Users new to software development and its tooling who have absolutely no understanding what they are doing and yern for a good harness with features to abstract away the complexity of running and monitoring agents using advanced techniques without harming their machines
2. **Group Name: Wizards**: Users who are very experienced with software development and its tooling who would enjoy the convenience of clawker's features

The assumption right now is the majority of users will most likely fall in the first group as `clawker` is an abstraction that simplifies creating and managing containers. `docker` experienced users will be less apt to pursue a solution like this, but may want to adopt for other convenience features like monitoring etc.

**`clawker` should prioritize being intuitive for those new to container management and just want to intuitively run claude code, but do its best to also make docker users feel right at home whenever possible**

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

### SHOULD USE

- **ast-grep** - Structural code search when Serena unavailable

</critical_instructions>

## Repository Structure

```
├── cmd/clawker/              # Main CLI binary
├── internal/
│   ├── build/                 # Image building orchestration
│   ├── config/                # Viper config loading + validation
│   ├── credentials/           # Env vars, .env parsing, OTEL
│   ├── engine/                # Docker SDK wrappers
│   ├── monitor/               # Observability stack (Prometheus, Grafana)
│   ├── term/                  # PTY/terminal handling
│   └── workspace/             # Bind vs Snapshot strategies
├── pkg/
│   ├── build/                 # Dockerfile templates, semver, npm registry
│   ├── cmd/                   # Cobra commands (start, run, stop, ls, etc.)
│   ├── cmdutil/               # Factory, error handling, output utilities
│   └── logger/                # Zerolog setup
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
| `WorkspaceStrategy` | Bind (live mount) vs Snapshot (ephemeral copy) |
| `DockerEngine` | Docker SDK with user-friendly errors + "Next Steps" |
| `PTYHandler` | Raw terminal mode, bidirectional streaming |
| `ContainerConfig` | Labels, naming (`clawker.project.agent`), volumes |

See @.claude/docs/ARCHITECTURE.md for detailed abstractions.

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

| Command | Description |
|---------|-------------|
| `init`, `build`, `run` (alias: `start`), `stop` | Lifecycle |
| `ls`, `logs` | Inspection |
| `rm` (alias: `prune`), `rm --unused` | Cleanup |
| `config check`, `monitor *` | Configuration/observability |

## Configuration (clawker.yaml)

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
3. Volumes preserved unless `--clean` or `--remove`
4. `run` is idempotent (reattaches to existing container); `start` is alias
5. `run` preserves containers by default; use `--remove` for ephemeral
6. Hierarchical naming: `clawker.project.agent`
7. Labels (`com.clawker.*`) are authoritative for filtering
8. stdout for data, stderr for status
9. Shell path via Viper: CLI flag → `CLAWKER_SHELL` env → config → `/bin/sh`

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

All `internal/engine` methods accept `ctx context.Context` as their first parameter:

- `Engine`: `ContainerCreate(ctx, ...)`, `VolumeExists(ctx, ...)`, `ImagePull(ctx, ...)`
- `ContainerManager`: `Create(ctx, ...)`, `Start(ctx, ...)`, `FindOrCreate(ctx, ...)`
- `VolumeManager`: `EnsureVolume(ctx, ...)`, `CopyToVolume(ctx, ...)`
- `ImageManager`: `EnsureImage(ctx, ...)`, `BuildImage(ctx, ...)`

For cleanup in deferred functions, use `context.Background()` since the original context may be cancelled:

```go
defer func() {
    cleanupCtx := context.Background()
    containerMgr.Remove(cleanupCtx, containerID, true)
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

| Test Type | Location | Purpose |
|-----------|----------|---------|
| Unit tests | `*_test.go` | Flag parsing, option defaults, function logic |
| Integration tests | `*_integration_test.go` | Docker state verification, end-to-end flows |
| Regression tests | Add to test files | Prevent fixed bugs from reoccurring |

**Before completing any code change:**

1. Run `go test ./...` - all unit tests must pass
2. Run `go test ./pkg/cmd/... -tags=integration` - all integration tests must pass
3. Add regression tests for bug fixes
4. Update existing tests when behavior changes

See @.claude/rules/TESTING.md for detailed testing guidelines.

## Documentation

| File | Purpose |
|------|---------|
| @.claude/docs/CLI-VERBS.md | CLI command reference with flags and examples |
| @.claude/docs/ARCHITECTURE.md | Detailed abstractions and interfaces |
| @.claude/docs/CONTRIBUTING.md | Adding commands, updating docs |
| @.claude/rules/TESTING.md | CLI testing guildelines (**only access when writing command tests**) |
| @README.md | User-facing documentation |

**Critical**: After code changes, update README.md (user-facing) and CLAUDE.md (developer-facing) and memories (serena) as appropriate.
