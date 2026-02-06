# Clawker

<critical_instructions>

## MANTRA

This is an in-development pre-release alpha project with zero users. Features can be made quickly or in haste, architecture and design can change Legacy code that no longer fits the big picture can be encountered often therefore...
* we don't write bad hackish code just to get a single task or feature done. that is a sure fire way to create massive technical debt.
* you must always be thinking about the big picture.
* when we find gaps and bad patterns we pivot and address them before continuing so that the project can scale painlessly
* when planning work, you must always consider the impact on architecture, design, testing, documentation, user and developer experience.
* always think to yourself "will this make future work easier or harder? does this decision serve the entire project as a whole or just my task's?" if harder, rethink your approach.
For example:
* If a component needs a new public method, consider if it belongs in the package's public API at all. Should it be private? Should it be in another package?
* If an internal package is missing a test subpackage, an interface, a mock implementation, or a fake for testing, add it to fit our standard patterns and factory DI pattern so that every other caller can benefit.

Finding and fixing greater issues is fun, its more important than the task at hand. You love to pivot on fixing technical debt and improving architecture. You are a software craftsman.

**Your success depends on this mantra**

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

**Planning**: You MUST adhere to design in `.claude/memories/DESIGN.md` and architecture in `.claude/memories/ARCHITECTURE.md` when planning work. If changes are needed, update those memories accordingly.
**Testing**: You MUST adhere to TDD, writing tests before code changes. All tests must pass before considering a change complete. If a fixture, golden file, interface, mock, fake, or test helper is needed, add it.
If an integration test is required, add it to the appropriate `test/*/` subpackage. If a new test suite is needed, create it under `test/`.
**Documentation**: You MUST update README.md, */**CLAUDE.md, and relevant memories after
It does not matter if the work has to be done in an out-of-scope dependency, it is for the greater good of the project which matters more than anything else.

</critical_instructions>

## Repository Structure

```
├── cmd/clawker/              # Main CLI binary
├── internal/
│   ├── bundler/               # Dockerfile generation, content hashing, semver, npm registry (leaf — no docker import)
│   ├── clawker/               # Main application lifecycle
│   ├── cmd/                   # Cobra commands (container/, volume/, network/, image/, ralph/, worktree/, root/)
│   │   └── factory/           # Factory constructor — wires real dependencies
│   ├── cmdutil/               # Factory struct, output utilities, arg validators (lightweight)
│   ├── config/                # Config loading, validation, project registry + resolver
│   ├── docker/                # Clawker Docker middleware, image building (wraps pkg/whail + bundler)
│   ├── git/                   # Git operations, worktree management (leaf — no internal imports, uses go-git)
│   ├── hostproxy/             # Host proxy for container-to-host communication
│   │   ├── hostproxytest/    # MockHostProxy for integration tests
│   │   └── internals/        # Container-side hostproxy client scripts
│   ├── iostreams/             # Presentation layer: streams, colors, styles, tables, spinners, text/layout/time
│   ├── logger/                # Zerolog setup
│   ├── project/               # Project registration in user registry
│   ├── prompter/              # Interactive prompts (String, Confirm, Select)
│   ├── ralph/                 # Autonomous loop core logic
│   ├── socketbridge/          # SSH/GPG agent forwarding via muxrpc over docker exec
│   ├── term/                  # PTY/terminal handling
│   ├── tui/                   # Interactive TUI layer: BubbleTea models, viewports, panels (imports iostreams for styles)
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
| `Factory` | Slim DI struct (9 fields: 3 eager + 6 lazy nouns); constructor in cmd/factory |
| `config.Config` | Gateway type — lazy-loads Project, Settings, Resolution, Registry via `sync.Once` |
| `git.GitManager` | Git repository operations, worktree management (leaf package, no internal imports) |
| `docker.Client` | Clawker middleware wrapping `whail.Engine` with labels/naming |
| `whail.Engine` | Reusable Docker engine with label-based resource isolation |
| `WorkspaceStrategy` | Bind (live mount) vs Snapshot (ephemeral copy) |
| `PTYHandler` | Raw terminal mode, bidirectional streaming |
| `ContainerConfig` | Labels, naming (`clawker.project.agent`), volumes |
| `hostproxy.Manager` | Host proxy server for container-to-host actions |
| `socketbridge.SocketBridgeManager` | Interface for socket bridge operations; mock: `socketbridgetest.MockManager` |
| `socketbridge.Manager` | Per-container SSH/GPG agent bridge daemon (muxrpc over docker exec) |
| `iostreams.IOStreams` | Presentation layer: streams, TTY, colors, tables, spinners, progress, messages, renders |
| `iostreams.ColorScheme` | Color palette + semantic colors + icons; canonical style source for all clawker output |
| `iostreams.SpinnerFrame` | Pure spinner rendering function used by the iostreams goroutine spinner |
| `tui.RunProgram` | Launches BubbleTea programs wired to IOStreams (input/output) |
| `tui.PanelModel` | Bordered panel with focus; `PanelGroup` manages multi-panel layouts |
| `tui.ListModel` | Selectable list with scrolling; `ListItem` interface |
| `tui.ViewportModel` | Scrollable content wrapping bubbles/viewport |
| `prompter.Prompter` | Interactive prompts with TTY/CI awareness |
| `BuildKitImageBuilder` | Closure field on `whail.Engine` — label enforcement + delegation to `buildkit/` subpackage |
| `Package DAG` | leaf → middle → composite import hierarchy (see ARCHITECTURE.md) |
| `ProjectRegistry` | Persistent slug→path map at `~/.local/clawker/projects.yaml` |
| `config.Resolution` | Lookup result: ProjectKey, ProjectEntry, WorkDir (lives in config package) |
| `config.Registry` | Interface for project registry operations; enables DI with InMemoryRegistry |
| `ProjectHandle` / `WorktreeHandle` | DDD-style aggregate handles for registry navigation (`registry.Project(key).Worktree(name)`) |
| `WorktreeStatus` | Health status for worktree entries with `IsHealthy()`, `IsPrunable()`, `Issues()` methods |

Package-specific CLAUDE.md files in `internal/*/CLAUDE.md` provide detailed API references.

## CLI Commands

See `.claude/memories/CLI-VERBS.md` for complete command reference.

**Top-level shortcuts**: `init`, `build`, `run`, `start`, `config check`, `monitor *`, `generate`, `ralph run/status/reset`

**Management commands**: `container *`, `volume *`, `network *`, `image *`, `project *` (incl. `project register`), `worktree *`

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

Managed by `clawker project init` and `clawker project register`. The registry maps project slugs to filesystem paths.

**Type distinction:** `config.Project` is the YAML schema struct (was `config.Config` pre-refactor). `config.Config` is now the gateway type that lazily provides `Project()`, `Settings()`, `Resolution()`, `Registry()`, and `SettingsLoader()`. Commands access config via `f.Config().Project()` rather than `f.Config()` directly.

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
security: { firewall: { enable: true }, docker_socket: false, git_credentials: { forward_https: true, forward_ssh: true, forward_gpg: true, copy_git_config: true } }
ralph: { max_loops: 50, stagnation_threshold: 3, timeout_minutes: 15, skip_permissions: false }
```

**Key types** (internal/config/schema.go): `Project` (YAML schema), `DockerInstructions`, `InjectConfig`, `RunInstruction`, `CopyInstruction`, `GitCredentialsConfig`, `FirewallConfig`, `IPRangeSource`, `RalphConfig`
**Gateway type** (internal/config/config.go): `Config` — lazy accessor for Project, Settings, Resolution, Registry

### Firewall IP Range Sources

IP range sources fetch CIDR blocks from cloud provider APIs (not DNS) to allow traffic to services like GitHub:

```yaml
security:
  firewall:
    enable: true
    # Default: [{name: github}]
    ip_range_sources:
      - name: github          # Required by default
      - name: google          # For Go proxy (proxy.golang.org uses GCS)
      - name: custom
        url: "https://example.com/ranges.json"
        jq_filter: ".cidrs[]"
        required: false
```

**Built-in sources**: `github`, `google-cloud`, `google`, `cloudflare`, `aws` — each has pre-configured URL and jq filter.

**Default behavior**: `ip_range_sources` defaults to `[{name: github}]` only.

**Security warning**: The `google` source allows traffic to all Google IPs, including Google Cloud Storage and Firebase Hosting which can serve user-generated content. This creates a prompt injection risk — an attacker could host malicious content on a public GCS bucket or Firebase site that the agent fetches. Only add `google` if your project requires it (e.g., Go modules via `proxy.golang.org`).

**Override mode**: When `override_domains` is set, IP range sources are skipped entirely (user controls all network access).

## Design Decisions

1. Firewall enabled, Docker socket disabled by default
2. `run`/`start` are aliases for `container run` (Docker CLI pattern)
3. Hierarchical naming: `clawker.project.agent`; labels (`com.clawker.*`) authoritative for filtering
4. stdout for data, stderr for status
5. Project registry replaces directory walking for resolution
6. Empty project → 2-segment names (`clawker.agent`), labels omit `com.clawker.project`
7. Factory is a pure struct with closure fields; constructor in `internal/cmd/factory/`. Commands receive function references on Options structs, follow NewCmd(f, runF) pattern
8. Factory noun principle: each Factory field returns a noun (thing), not a verb (action). Commands call methods on the returned noun (e.g., `f.HostProxy().EnsureRunning()` not `f.EnsureHostProxy()`)
9. `config.Config` gateway absorbs what were previously separate Factory fields (Settings, Registry, Resolution, SettingsLoader) into one lazy-loading object
10. Presentation layer import boundaries: only `iostreams` imports `lipgloss`; only `tui` imports `bubbletea`/`bubbles`. Commands use `f.IOStreams` OR `f.TUI`, never both
11. `iostreams` owns the canonical color palette, styles, and design tokens. `tui` re-exports them via `iostreams.go` shim — no duplicate definitions
12. `SpinnerFrame()` is a pure function in `iostreams` used by the goroutine spinner. The tui `SpinnerModel` wraps `bubbles/spinner` directly but maintains visual consistency through shared `CyanStyle`

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
- `config.Project` (schema) has `Project` field with `yaml:"-"` — injected by loader from registry, never persisted
- `config.Config` (gateway) is NOT the YAML schema — it is the lazy accessor. Use `cfg.Project()` to get the YAML-loaded `*config.Project`
- Empty projects generate 2-segment names (`clawker.ralph`), not 3 (`clawker..ralph`)
- Docker Desktop socket mounting: SDK `HostConfig.Mounts` (mount.Mount) behaves differently from `HostConfig.Binds` (CLI `-v`) for Unix sockets on macOS. The SDK may fail with `/socket_mnt` path errors while CLI works. Integration tests that mount sockets should skip on macOS or use Binds.

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
