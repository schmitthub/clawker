# Clawker

Go CLI wrapping Claude Code in secure, reproducible Docker containers. Core philosophy: **Safe Autonomy** - host is read-only by default.

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
   - `write_memory`, `edit_memory`, `delete_memory` with updates of the task before completion

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
./bin/clawker --debug start              # Debug logging
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
| `init`, `build`, `start`, `run`, `stop` | Lifecycle |
| `ls`, `logs`, `sh` | Inspection |
| `rm`, `prune` | Cleanup |
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
3. Volumes preserved unless `--clean`
4. `start` is idempotent (reattaches to existing container)
5. Hierarchical naming: `clawker.project.agent`
6. Labels (`com.clawker.*`) are authoritative for filtering
7. stdout for data, stderr for status

## Important Gotchas

- `os.Exit()` does NOT run deferred functions - restore terminal state explicitly
- Raw terminal mode: Ctrl+C goes to container, not as SIGINT
- Never use `logger.Fatal()` in Cobra hooks - return errors instead
- Don't wait for stdin goroutine on container exit (may block on Read)
- Docker hijacked connections need cleanup of both read and write sides

## Documentation

| File | Purpose |
|------|---------|
| @.claude/docs/CLI-VERBS.md | CLI command reference with flags and examples |
| @.claude/docs/ARCHITECTURE.md | Detailed abstractions and interfaces |
| @.claude/docs/CONTRIBUTING.md | Adding commands, updating docs |
| @.claude/rules/TESTING.md | CLI testing guildelines (**only access when writing command tests**) |
| @README.md | User-facing documentation |

**Critical**: After code changes, update README.md (user-facing) and CLAUDE.md (developer-facing) as appropriate.
