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

**Planning**: You MUST adhere to design in `.claude/memories/DESIGN.md`

</critical_instructions>

## Repository Structure

```
├── cmd/clawker/              # Main CLI binary
├── internal/
│   ├── build/                 # Image building, Dockerfile generation, semver, npm registry
│   ├── clawker/               # Main application lifecycle
│   ├── cmd/                   # Cobra commands (container/, volume/, network/, image/, ralph/, root/)
│   │   └── factory/           # Factory constructor — wires real dependencies
│   ├── cmdutil/               # Factory struct, output utilities, arg validators (lightweight)
│   ├── config/                # Config loading, validation, project registry + resolver
│   ├── credentials/           # Env vars, .env parsing, OTEL
│   ├── docker/                # Clawker Docker middleware (wraps pkg/whail)
│   ├── hostproxy/             # Host proxy for container-to-host communication
│   │   └── hostproxytest/    # MockHostProxy for integration tests
│   ├── iostreams/             # Testable I/O: TTY, colors, progress, pager
│   ├── logger/                # Zerolog setup
│   ├── project/               # Project registration in user registry
│   ├── prompts/               # Interactive prompts (String, Confirm, Select)
│   ├── ralph/                 # Autonomous loop core logic
│   ├── resolver/              # Image resolution (project image, default, validation)
│   ├── term/                  # PTY/terminal handling
│   ├── tui/                   # Reusable TUI components (BubbleTea/Lipgloss)
│   └── workspace/             # Bind vs Snapshot strategies
├── pkg/
│   └── whail/                 # Reusable Docker engine with label-based isolation
│       └── buildkit/          # BuildKit client (moby/buildkit) — isolated heavy deps
├── test/
│   ├── harness/               # Test harness, config builders, helpers (golden files, docker)
│   ├── whail/                 # Whail BuildKit integration tests (Docker + BuildKit)
│   ├── cli/                   # Testscript-based CLI workflow tests (Docker)
│   ├── commands/              # Command integration tests (Docker)
│   ├── internals/             # Container scripts/services tests (Docker)
│   └── agents/                # Full agent E2E tests (Docker)
└── templates/                 # clawker.yaml scaffolding
```

## Build Commands

```bash
go build -o bin/clawker ./cmd/clawker  # Build CLI
make test                                 # Unit tests (no Docker, excludes test/cli,internals,agents)
./bin/clawker --debug run @              # Debug logging
go run ./cmd/gen-docs --doc-path docs --markdown  # Regenerate CLI docs

# Docker-required tests (directory separation, no build tags)
go test ./test/whail/... -v -timeout 5m          # Whail BuildKit integration tests
go test ./test/cli/... -v -timeout 15m           # CLI workflow tests
go test ./test/commands/... -v -timeout 10m      # Command integration tests
go test ./test/internals/... -v -timeout 10m     # Internal integration tests
go test ./test/agents/... -v -timeout 15m        # Agent E2E tests
```

## Key Concepts

| Abstraction | Purpose |
|-------------|---------|
| `Factory` | DI container struct (cmdutil) + constructor (cmd/factory) |
| `docker.Client` | Clawker middleware wrapping `whail.Engine` with labels/naming |
| `whail.Engine` | Reusable Docker engine with label-based resource isolation |
| `WorkspaceStrategy` | Bind (live mount) vs Snapshot (ephemeral copy) |
| `PTYHandler` | Raw terminal mode, bidirectional streaming |
| `ContainerConfig` | Labels, naming (`clawker.project.agent`), volumes |
| `hostproxy.Manager` | Host proxy server for container-to-host actions |
| `iostreams.IOStreams` | Testable I/O with TTY detection, colors, progress |
| `prompts.Prompter` | Interactive prompts with TTY/CI awareness |
| `BuildKitImageBuilder` | Closure field on `whail.Engine` — label enforcement + delegation to `buildkit/` subpackage |
| `Package DAG` | leaf → middle → composite import hierarchy (see ARCHITECTURE.md) |
| `ProjectRegistry` | Persistent slug→path map at `~/.local/clawker/projects.yaml` |
| `Resolver` | Resolves working directory to registered project via longest-prefix match |
| `Resolution` | Lookup result: ProjectKey, ProjectEntry, WorkDir |

Package-specific CLAUDE.md files in `internal/*/CLAUDE.md` provide detailed API references.

## CLI Commands

See `.claude/memories/CLI-VERBS.md` for complete command reference.

**Top-level shortcuts**: `init`, `build`, `run`, `start`, `config check`, `monitor *`, `generate`, `ralph run/status/reset`

**Management commands**: `container *`, `volume *`, `network *`, `image *`, `project *` (incl. `project register`)

Commands use positional arguments for resource names (e.g., `clawker container stop clawker.myapp.ralph`) matching Docker's interface.

## Configuration

### User Settings (~/.local/clawker/settings.yaml)

```yaml
default_image: "node:20-slim"
```

### Project Registry (~/.local/clawker/projects.yaml)

```yaml
projects:
  my-app:
    name: "my-app"
    root: "/Users/dev/my-app"
```

Managed by `clawker project init` and `clawker project register`. The registry maps project slugs to filesystem paths. `Config.Project` is computed from the registry (never read from YAML — it is `yaml:"-"`).

### Project Config (clawker.yaml)

```yaml
version: "1"
project: "my-app"
build:
  image: "buildpack-deps:bookworm-scm"
  packages: ["git", "ripgrep"]
  instructions: { env: {}, copy: [], root_run: [], user_run: [] }
  inject: { after_from: [], after_packages: [] }
agent: { includes: [], env: {} }
workspace: { remote_path: "/workspace", default_mode: "snapshot" }
security: { firewall: { enable: true }, docker_socket: false, git_credentials: { forward_https: true, forward_ssh: true, copy_git_config: true } }
ralph: { max_loops: 50, stagnation_threshold: 3, timeout_minutes: 15, skip_permissions: false }
```

**Key types** (internal/config/schema.go): `DockerInstructions`, `InjectConfig`, `RunInstruction`, `CopyInstruction`, `GitCredentialsConfig`, `FirewallConfig`, `RalphConfig`

## Design Decisions

1. Firewall enabled, Docker socket disabled by default
2. `run`/`start` are aliases for `container run` (Docker CLI pattern)
3. Hierarchical naming: `clawker.project.agent`; labels (`com.clawker.*`) authoritative for filtering
4. stdout for data, stderr for status
5. Project registry replaces directory walking for resolution
6. Empty project → 2-segment names (`clawker.agent`), labels omit `com.clawker.project`
7. Factory is a pure struct with closure fields; constructor in `internal/cmd/factory/`. Commands receive function references on Options structs, follow NewCmd(f, runF) pattern

## Important Gotchas

- `os.Exit()` does NOT run deferred functions — restore terminal state explicitly
- Raw terminal mode: Ctrl+C goes to container, not as SIGINT
- Never use `logger.Fatal()` in Cobra hooks — return errors instead
- Don't wait for stdin goroutine on container exit (may block on Read)
- Docker hijacked connections need cleanup of both read and write sides
- Terminal visual state (alternate screen, cursor visibility, colors) must be reset separately from termios mode — `term.Restore()` sends escape sequences before restoring raw/cooked mode
- Terminal resize +1/-1 trick: Resize to (height+1, width+1) then actual size to force SIGWINCH for TUI redraw
- CLI test assertions (test/cli/) are case-sensitive; tests need `mkdir $HOME/.local/clawker` and `security.firewall.enable: false`
- Go import cycles: `internal/cmd/container/opts/` exists because parent imports subcommands and subcommands need shared types
- After modifying a package's public API, update its `CLAUDE.md` and corresponding `.claude/rules/` file
- `Config.Project` is `yaml:"-"` — injected by loader from registry, never persisted
- Empty projects generate 2-segment names (`clawker.ralph`), not 3 (`clawker..ralph`)

## Context Management (Critical)

**NEVER** store `context.Context` in struct fields. Pass as first parameter to I/O methods. Use `context.Background()` for cleanup in deferred functions.

## Testing Requirements

**All tests must pass before any change is complete.** Run `make test` (unit) or `make test-all` (all suites). See Build Commands above for individual test suites. See `.claude/rules/testing.md` for conventions.

## Documentation

- `.claude/rules/` — Auto-loaded guidelines (code style, testing, path-scoped package rules)
- `.claude/memories/` — On-demand reference docs (architecture, CLI verbs, design)
- `.claude/prds/` — Product requirement documents
- `internal/*/CLAUDE.md` — Package-specific API references (lazy-loaded)
- `.serena/memories/` — Active work-in-progress tracking

**Critical**: After code changes, update README.md (user-facing), CLAUDE.md (developer-facing), and memories as appropriate.

## Documentation Maintenance

- `bash scripts/check-claude-freshness.sh` — Check if CLAUDE.md files are stale vs Go source
- `/audit-memory` — Comprehensive documentation health audit (in Claude Code)
- `bash scripts/install-hooks.sh` — Install advisory pre-commit freshness check
