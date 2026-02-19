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

2. **deepwiki** - Always use deepwiki MCP for documentation about GitHub repositories and open source software configurations, functionality, features, code architecture, infrastructure, and code design without the user having to ask for it. If you can't find an answer use context7. If that fails then use default tools. Use the following commands:
   - read_wiki_structure - Get a list of documentation topics for a GitHub repository
   - read_wiki_contents - View documentation about a GitHub repository
   - ask_question - Ask any question about a GitHub repository and get an AI-powered, context-grounded response

3. **Context7** - When I need library/API documentation, code generation, setup or configuration steps without me having to explicitly ask.
   - `resolve-library-id` first, then `get-library-docs`
   - For: Docker SDK, spf13/cobra, spf13/viper, rs/zerolog, gopkg.in/yaml.v3
   
4. **github mcp** - Use github's mcp for repository-specific information like PR status, issues, code search, and commit history. Use the following commands:


### Workflow Requirements

**Planning**: You MUST adhere to design in `.claude/docs/DESIGN.md` and architecture in `.claude/docs/ARCHITECTURE.md` when planning work. If changes are needed, update those docs accordingly.
**Testing**: You MUST adhere to TDD, writing tests before code changes. All tests must pass before considering a change complete. If a fixture, golden file, interface, mock, fake, or test helper is needed, add it.
If an integration test is required, add it to the appropriate `test/*/` subpackage. If a new test suite is needed, create it under `test/`.
**Documentation**: You MUST update README.md, */**CLAUDE.md, and relevant memories after
It does not matter if the work has to be done in an out-of-scope dependency, it is for the greater good of the project which matters more than anything else.

</critical_instructions>

## Repository Structure

```
├── cmd/clawker/              # Main CLI binary
├── cmd/fawker/               # Demo CLI — faked deps, recorded scenarios, no Docker
├── internal/
│   ├── build/                 # Build-time metadata (version, date) — leaf, stdlib only
│   ├── bundler/               # Dockerfile generation, content hashing, semver, npm registry (leaf — no docker import)
│   ├── clawker/               # Main application lifecycle
│   ├── cmd/                   # Cobra commands (container/, volume/, network/, image/, version/, loop/, worktree/, root/)
│   │   └── factory/           # Factory constructor — wires real dependencies
│   ├── cmdutil/               # Factory struct, error types, arg validators (lightweight)
│   ├── config/                # Viper-backed config: schema types, multi-file loading, constants (REFACTORING — see internal/config/CLAUDE.md)
│   ├── containerfs/           # Host Claude config preparation for container init
│   ├── docker/                # Clawker Docker middleware, image building (wraps pkg/whail + bundler)
│   │   └── dockertest/        # FakeClient, test helpers
│   ├── docs/                  # CLI doc generation (man, markdown, rst, yaml)
│   ├── git/                   # Git operations, worktree management (leaf — no internal imports, uses go-git)
│   │   └── gittest/           # InMemoryGitManager for testing
│   ├── hostproxy/             # Host proxy for container-to-host communication
│   │   ├── hostproxytest/     # MockHostProxy for integration tests
│   │   └── internals/         # Container-side hostproxy client scripts
│   ├── iostreams/             # I/O streams, colors, styles, spinners, progress, layout
│   │   └── iostreamstest/     # TestIOStreams constructor: New()
│   ├── keyring/               # Keyring service for credential storage
│   ├── logger/                # Zerolog setup (file + optional OTEL bridge)
│   │   └── loggertest/        # Test doubles: TestLogger, New(), NewNop()
│   ├── monitor/               # Monitoring stack templates (Grafana, Prometheus, Loki)
│   ├── project/               # Project registration in user registry
│   ├── prompter/              # Interactive prompts (String, Confirm, Select)
│   ├── signals/               # OS signal utilities (leaf — stdlib only)
│   ├── socketbridge/          # SSH/GPG agent forwarding via muxrpc over docker exec
│   │   └── socketbridgetest/  # MockManager for testing
│   ├── term/                  # Terminal capabilities + raw mode (leaf — sole x/term gateway)
│   ├── text/                  # Pure text utilities (leaf — stdlib only)
│   ├── tui/                   # Interactive TUI layer: BubbleTea models, viewports, panels (imports iostreams for styles)
│   ├── update/                # Background update checker — GitHub releases API, 24h cached notifications (foundation — no internal imports)
│   └── workspace/             # Bind vs Snapshot strategies
├── pkg/
│   └── whail/                 # Reusable Docker engine with label-based isolation
│       └── buildkit/          # BuildKit client (moby/buildkit) — isolated heavy deps
├── test/
│   ├── harness/               # Test harness, config builders, helpers (docker)
│   │   └── golden/            # Golden file utilities (leaf — stdlib + testify only)
│   ├── whail/                 # Whail BuildKit integration tests (Docker + BuildKit)
│   ├── cli/                   # Testscript-based CLI workflow tests (Docker)
│   ├── commands/              # Command integration tests (Docker)
│   ├── internals/             # Container scripts/services tests (Docker)
│   └── agents/                # Full agent E2E tests (Docker)
├── scripts/
│   ├── install.sh             # curl|bash installer (downloads pre-built binary from GitHub releases)
│   └── check-claude-freshness.sh  # CLAUDE.md staleness checker
└── templates/                 # clawker.yaml scaffolding
```

## Build Commands

```bash
# Install via Homebrew
brew install schmitthub/tap/clawker

# Install pre-built binary (no Go required)
curl -fsSL https://raw.githubusercontent.com/schmitthub/clawker/main/scripts/install.sh | bash
bash scripts/install.sh --version v0.1.3 --dir $HOME/.local/bin  # Pin version + custom dir

go build -o bin/clawker ./cmd/clawker  # Build CLI
make fawker                               # Build fawker demo CLI (faked deps, no Docker)
make test                                 # Unit tests (no Docker, excludes test/cli,internals,agents)
./bin/clawker --debug run @              # Debug logging
go run ./cmd/gen-docs --doc-path docs --markdown            # Regenerate CLI docs
go run ./cmd/gen-docs --doc-path docs --markdown --website   # Regenerate CLI docs for Mintlify (MDX-safe + frontmatter)
npx mintlify dev --docs-directory docs                       # Local Mintlify preview (http://localhost:3000)

# Fawker demo CLI (visual UAT without Docker)
./bin/fawker image build                          # Default scenario (multi-stage)
./bin/fawker image build --scenario error         # Error scenario
./bin/fawker image build --progress plain         # Plain mode
./bin/fawker container run -it --agent test @      # Interactive run with init progress tree
./bin/fawker container run --detach --agent test @ # Detached run with init progress tree
./bin/fawker container create --agent test @       # Create container (real flow)
./bin/fawker container ls                         # List fake containers
./bin/fawker image ls                             # List fake images

# Golden file tests
GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeed -v          # Regenerate JSON testdata
GOLDEN_UPDATE=1 go test ./internal/tui/... -run TestProgressPlain_Golden -v # Regenerate TUI golden files
GOLDEN_UPDATE=1 go test ./internal/cmd/image/build/... -run TestBuildProgress_Golden -v  # Regenerate command golden files

# Docker-required tests (directory separation, no build tags)
go test ./test/whail/... -v -timeout 5m          # Whail BuildKit integration tests
go test ./test/cli/... -v -timeout 15m           # CLI workflow tests
go test ./test/commands/... -v -timeout 10m      # Command integration tests
go test ./test/internals/... -v -timeout 10m     # Internal integration tests
go test ./test/agents/... -v -timeout 15m        # Agent E2E tests

# Pre-commit hooks (mirrors CI quality gates locally)
bash scripts/install-hooks.sh          # Install pre-commit hooks (run once after clone)
make pre-commit                        # Run all hooks against entire repo
pre-commit run gitleaks --all-files    # Run a single hook

# Semgrep version: 1.146.0
# Pre-commit uses system semgrep with --baseline-commit HEAD (diff-only scanning).
# CI uses semgrep/semgrep:1.146.0 Docker image.
# When upgrading, update both .pre-commit-config.yaml comment AND security.yml image tag.
```

## Key Concepts

| Abstraction | Purpose |
|-------------|---------|
| `Factory` | Slim DI struct (9 fields: 3 eager + 6 lazy nouns); constructor in cmd/factory |
| `git.GitManager` | Git repository operations, worktree management (leaf package, no internal imports) |
| `docker.Client` | Clawker middleware wrapping `whail.Engine` with labels/naming |
| `whail.Engine` | Reusable Docker engine with label-based resource isolation |
| `WorkspaceStrategy` | Bind (live mount) vs Snapshot (ephemeral copy) |
| `PTYHandler` | Raw terminal mode, bidirectional streaming (in `docker` package) |
| `ContainerConfig` | Labels, naming (`clawker.project.agent`), volumes |
| `CreateContainer()` | Single entry point for container creation (workspace, config, env, create, inject); shared by `run` and `create` via events channel for progress |
| `CreateContainerConfig` / `CreateContainerResult` | Input/output types for `CreateContainer()` — all deps and runtime values |
| `CreateContainerEvent` | Channel event: Step, Status (`StepRunning`/`StepComplete`/`StepCached`), Type (`MessageInfo`/`MessageWarning`), Message |
| `clawker-share` | Optional read-only bind mount from `$CLAWKER_HOME/.clawker-share` into containers at `~/.clawker-share` when `agent.enable_shared_dir: true`; host dir created during `clawker init`, re-created if missing during mount setup |
| `containerfs` | Host Claude config preparation for container init: copies settings, plugins (incl. cache), credentials to config volume; rewrites host paths in plugin JSON files; prepares post-init script tar |
| `ConfigVolumeResult` | Bool flags tracking which config volumes were freshly created (`ConfigCreated`, `HistoryCreated`) — returned by `workspace.EnsureConfigVolumes` |
| `InitConfigOpts` | Options for `shared.InitContainerConfig` — project/agent names, container work dir, ClaudeCodeConfig, CopyToVolumeFn (DI) |
| `InjectOnboardingOpts` | Options for `shared.InjectOnboardingFile` — container ID, CopyToContainerFn (DI) |
| `InjectPostInitOpts` | Options for `shared.InjectPostInitScript` — container ID, script content, CopyToContainerFn (DI) |
| `hostproxy.HostProxyService` | Interface for host proxy operations (EnsureRunning, IsRunning, ProxyURL); mock: `hostproxytest.MockManager` |
| `hostproxy.Manager` | Concrete host proxy daemon manager (spawns subprocess); implements `HostProxyService` |
| `socketbridge.SocketBridgeManager` | Interface for socket bridge operations; mock: `socketbridgetest.MockManager` |
| `socketbridge.Manager` | Per-container SSH/GPG agent bridge daemon (muxrpc over docker exec) |
| `iostreams.IOStreams` | I/O streams, TTY detection, colors, styles, spinners, progress, layout. `Logger` field for diagnostic file logging |
| `iostreams.Logger` | Interface matching `*zerolog.Logger` — `Debug/Info/Warn/Error() *zerolog.Event`. Decouples commands from `internal/logger` |
| `iostreams.ColorScheme` | Color palette + semantic colors + icons; canonical style source for all clawker output |
| `iostreams.SpinnerFrame` | Pure spinner rendering function used by the iostreams goroutine spinner |
| `text.*` | Pure ANSI-aware text utilities (leaf package): Truncate, PadRight, CountVisibleWidth, StripANSI, etc. |
| `tui.TablePrinter` | Table output: `bubbles/table` styled mode + tabwriter plain mode; content-aware column widths |
| `cmdutil.FlagError` | Error type triggering usage display in Main()'s centralized `printError` |
| `cmdutil.SilentError` | Sentinel error: already displayed, Main() just exits non-zero |
| `cmdutil.FormatFlags` | Reusable `--format`/`--json`/`--quiet` flag handling for list commands; populated during PreRunE. Convenience delegates (`IsJSON()`, `IsTemplate()`, etc.) avoid `Format.Format.` stutter. `ToAny[T any]` generic for template slice conversion |
| `cmdutil.FilterFlags` | Reusable `--filter key=value` flag handling; per-command key validation via `ValidateFilterKeys` |
| `cmdutil.WriteJSON` | Pretty-printed JSON output for `--json`/`--format json` modes; HTML escaping disabled (replaces deprecated `OutputJSON`) |
| `tui.TUI` | Factory noun for presentation layer; owns hooks + delegates to RunProgress. Commands capture `*TUI` eagerly, hooks registered later via `RegisterHooks()` |
| `tui.RunProgress` | Generic progress display: BubbleTea TTY mode (sliding window) + plain text; domain-agnostic via callbacks |
| `tui.ProgressStep` | Channel event type for progress display (ID, Name, Status, LogLine, Cached, Error) |
| `tui.ProgressDisplayConfig` | Configuration with CompletionVerb and callback functions: IsInternal, CleanName, ParseGroup, FormatDuration, OnLifecycle |
| `tui.LifecycleHook` | Generic hook function type for TUI lifecycle events; threaded via config structs, nil = no-op |
| `tui.HookResult` | Hook return type: Continue (bool), Message (string), Err (error) — controls post-hook execution flow |
| `whail.BuildProgressFunc` | Callback type threading build progress events through the options chain |
| `tui.RunProgram` | Launches BubbleTea programs wired to IOStreams (input/output) |
| `tui.PanelModel` | Bordered panel with focus; `PanelGroup` manages multi-panel layouts |
| `tui.ListModel` | Selectable list with scrolling; `ListItem` interface |
| `tui.ViewportModel` | Scrollable content wrapping bubbles/viewport |
| `tui.WizardField / WizardResult` | Multi-step wizard: field definitions + collected values + submit/cancel; `TUI.RunWizard` |
| `tui.SelectField / TextField / ConfirmField` | Standalone BubbleTea field models for forms; value semantics |
| `tui.RenderStepperBar` | Pure render function for horizontal step progress (icons: checkmark, filled circle, empty circle) |
| `prompter.Prompter` | Interactive prompts with TTY/CI awareness |
| `BuildKitImageBuilder` | Closure field on `whail.Engine` — label enforcement + delegation to `buildkit/` subpackage |
| `update.CheckForUpdate` | Background GitHub release check — 24h cached, suppressed in CI/DEV; wired into `Main()` via goroutine + channel |
| `update.CheckResult` | Returned when newer version available: `CurrentVersion`, `LatestVersion`, `ReleaseURL` |
| `Package DAG` | leaf → middle → composite import hierarchy (see ARCHITECTURE.md) |
| `ProjectRegistry` | Persistent slug→path map at `~/.local/clawker/projects.yaml` |
| `config.Config` | Single interface all callers receive. File names, directory paths, and constants are private — callers use `Config` methods or propose new ones. `NewConfig()` (production), `ReadFromString()` (testing/validation). Validates via `UnmarshalExact` (unknown keys rejected). Low-level mutation: `Set(key, value)` + dirty tracking, `Write(WriteOptions)` with ownership-aware routing (`ScopeSettings`/`ScopeProject`/`ScopeRegistry` → correct file), `Watch(onChange)` for file changes. Thread-safe via `sync.RWMutex`. **Consumer migration in progress** — see `internal/config/CLAUDE.md` for migration guide |
| `config.Registry` | Interface for project registry operations; enables DI with InMemoryRegistry |
| `ProjectHandle` / `WorktreeHandle` | DDD-style aggregate handles for registry navigation (`registry.Project(key).Worktree(name)`) |
| `build.Version` / `build.Date` | Build-time metadata injected via ldflags; `DEV` default with `debug.ReadBuildInfo` fallback |
| `WorktreeStatus` | Health status for worktree entries with `IsHealthy()`, `IsPrunable()`, `Issues()` methods |

Package-specific CLAUDE.md files in `internal/*/CLAUDE.md` provide detailed API references.

## CLI Commands

See `.claude/docs/CLI-VERBS.md` for complete command reference.

**Top-level shortcuts**: `init`, `build`, `run`, `start`, `config check`, `monitor *`, `generate`, `loop iterate/tasks/status/reset`, `version`

**Management commands**: `container *`, `volume *`, `network *`, `image *`, `project *` (incl. `project register`), `worktree *`

Commands use positional arguments for resource names (e.g., `clawker container stop clawker.myapp.dev`) matching Docker's interface.

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

### Project Config (clawker.yaml)

```yaml
version: "1"
project: "my-app"
build:
  image: "buildpack-deps:bookworm-scm"
  packages: ["git", "ripgrep"]
  instructions: { env: {}, copy: [], root_run: [], user_run: [] }
  inject: { after_from: [], after_packages: [] }
agent: { includes: [], env_file: [], from_env: [], env: {}, post_init: "" }
workspace: { remote_path: "/workspace", default_mode: "snapshot" }
security: { firewall: { enable: true }, docker_socket: false, git_credentials: { forward_https: true, forward_ssh: true, forward_gpg: true, copy_git_config: true } }
loop: { max_loops: 50, stagnation_threshold: 3, timeout_minutes: 15, skip_permissions: false, hooks_file: "", append_system_prompt: "" }
```

### Firewall IP Range Sources


**Security warning**: The `google` source allows traffic to all Google IPs, including Google Cloud Storage and Firebase Hosting which can serve user-generated content. This creates a prompt injection risk — an attacker could host malicious content on a public GCS bucket or Firebase site that the agent fetches. Only add `google` if your project requires it (e.g., Go modules via `proxy.golang.org`).

## Design Decisions

1. Firewall enabled, Docker socket disabled by default
2. `run`/`start` are aliases for `container run` (Docker CLI pattern)
3. Hierarchical naming: `clawker.project.agent`; labels (`dev.clawker.*`) authoritative for filtering
4. stdout for data, stderr for status/warnings/errors; `--format` flag for machine-readable output; per-scenario stream strategy (see style guide)
5. Project registry replaces directory walking for resolution
6. Empty project → 2-segment names (`clawker.agent`), labels omit `dev.clawker.project`
7. Factory is a pure struct with closure fields; constructor in `internal/cmd/factory/`. Commands receive function references on Options structs, follow NewCmd(f, runF) pattern
8. Factory noun principle: each Factory field returns a noun (thing), not a verb (action). Commands call methods on the returned noun (e.g., `f.HostProxy().EnsureRunning()` not `f.EnsureHostProxy()`)
9.  Presentation layer 4-scenario model: (1) static output = `iostreams` only, (2) static-interactive = `iostreams` + `prompter`, (3) live-display = `iostreams` + `tui`, (4) live-interactive = `iostreams` + `tui`. A command may import both `iostreams` and `tui`. Commands access TUI via `f.TUI` (Factory noun). Library boundaries: only `iostreams` imports `lipgloss`; only `tui` imports `bubbletea`/`bubbles`; only `term` imports `golang.org/x/term`
10. `iostreams` owns the canonical color palette, styles, and design tokens. `tui` accesses them via qualified imports (`iostreams.PanelStyle`), `text` utilities via `text.Truncate`
11. `SpinnerFrame()` is a pure function in `iostreams` used by the goroutine spinner. The tui `SpinnerModel` wraps `bubbles/spinner` directly but maintains visual consistency through shared `CyanStyle`
12. `zerolog` is for file logging only — user-visible output uses `fmt.Fprintf` to IOStreams streams. Command-layer code accesses logger via `ios.Logger` (IOStreams interface), library-layer code uses global `logger.Debug()`. Logger init happens in factory's `ioStreams(f)`, not in root.go

## Important Gotchas

- `os.Exit()` does NOT run deferred functions — restore terminal state explicitly
- Raw terminal mode: Ctrl+C goes to container, not as SIGINT
- Never use `logger.Fatal()` in Cobra hooks — return errors instead
- Don't wait for stdin goroutine on container exit (may block on Read)
- Docker hijacked connections need cleanup of both read and write sides
- Terminal visual state (alternate screen, cursor visibility, colors) must be reset separately from termios mode — `term.Restore()` sends escape sequences before restoring raw/cooked mode
- Terminal resize +1/-1 trick: Resize to (height+1, width+1) then actual size to force SIGWINCH for TUI redraw
- CLI test assertions (test/cli/) are case-sensitive; tests need `mkdir $HOME/.local/clawker` and `security.firewall.enable: false`
- Container flag types and domain logic consolidated in `internal/cmd/container/shared/` — `CreateContainer()` is the single creation entry point
- After modifying a package's public API, update its `CLAUDE.md` and corresponding `.claude/rules/` file
- `config.Project` (schema) has `Project` field with `yaml:"-"` — injected by loader from registry, never persisted
- Empty projects generate 2-segment names (`clawker.dev`), not 3 (`clawker..dev`)
- Docker Desktop socket mounting: SDK `HostConfig.Mounts` (mount.Mount) behaves differently from `HostConfig.Binds` (CLI `-v`) for Unix sockets on macOS. The SDK may fail with `/socket_mnt` path errors while CLI works. Integration tests that mount sockets should skip on macOS or use Binds.

## Context Management (Critical)

**NEVER** store `context.Context` in struct fields. Pass as first parameter to I/O methods. Use `context.Background()` for cleanup in deferred functions.

## Testing Requirements

**All tests must pass before any change is complete.** Run `make test` (unit) or `make test-all` (all suites). See Build Commands above for individual test suites. See `.claude/rules/testing.md` for conventions.

## Documentation

- `.claude/rules/` — Auto-loaded guidelines (code style, testing, path-scoped package rules)
- `.claude/docs/` — On-demand reference docs (architecture, CLI verbs, design)
- `.claude/prds/` — Product requirement documents
- `internal/*/CLAUDE.md` — Package-specific API references (lazy-loaded)
- `.serena/memories/` — Active work-in-progress tracking

**Critical**: After code changes, update README.md (user-facing), CLAUDE.md (developer-facing), and memories as appropriate.

### Mintlify Documentation Site (docs.clawker.dev)

User-facing docs are powered by [Mintlify](https://mintlify.com/) and live in the `docs/` directory.

- `docs/docs.json` — Mintlify site config (theme, nav, colors, integrations)
- `docs/custom.css` — Dark terminal theme overrides (surface colors, glassmorphism navbar, amber hover glow)
- `docs/favicon.svg` — `>_` terminal prompt favicon (amber on dark)
- `docs/assets/` — Image assets directory
- `docs/index.mdx` — Homepage
- `docs/*.mdx` — Hand-authored pages (quickstart, installation, configuration)
- `docs/cli-reference/*.md` — Auto-generated via Makefile, checked in, freshness verified separately in CI (**never edit directly**)
- `docs/architecture.md`, `docs/design.md`, `docs/testing.md` — Developer docs with Mintlify frontmatter
- See `.claude/rules/mintlify-docs.md` for full conventions (theming, MDX parsing, navigation)

**Regenerating CLI reference**: `go run ./cmd/gen-docs --doc-path docs --markdown --website`
- `--website` flag produces MDX-safe output (escapes bare `<word>` angle brackets) with Mintlify frontmatter
- Source: `internal/docs/markdown.go` (`GenMarkdownTreeWebsite`, `EscapeMDXProse`)

**Local preview**: `npx mintlify dev --docs-directory docs` (requires Node.js)

**Deployment**: Mintlify-hosted with GitHub App auto-deploy. Custom domain via Cloudflare CNAME → `cname.vercel-dns.com`.

## Documentation Maintenance

- `bash scripts/check-claude-freshness.sh` — Check if CLAUDE.md files are stale vs Go source
- `/audit-memory` — Comprehensive documentation health audit (in Claude Code)
- `bash scripts/install-hooks.sh` — Install pre-commit hooks (all CI quality gates)
