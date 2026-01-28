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
│   ├── cmd/                   # Cobra commands organized as:
│   │   ├── container/         # Docker CLI-compatible container management
│   │   ├── volume/            # Volume management
│   │   ├── network/           # Network management
│   │   ├── image/             # Image management
│   │   ├── ralph/             # Autonomous loop commands
│   │   ├── root/              # Root command and aliases
│   │   └── ...                # Top-level shortcuts (run, start, init, build, etc.)
│   ├── cmdutil/               # Factory, error handling, output utilities
│   ├── config/                # Viper config loading + validation
│   ├── credentials/           # Env vars, .env parsing, OTEL
│   ├── docker/                # Clawker-specific Docker middleware (wraps pkg/whail)
│   ├── hostproxy/             # Host proxy server for container-to-host communication
│   ├── iostreams/             # Testable I/O: TTY detection, colors, progress, pager
│   ├── logger/                # Zerolog setup
│   ├── monitor/               # Observability stack (Prometheus, Grafana)
│   ├── prompts/               # Interactive user prompts (String, Confirm, Select)
│   ├── ralph/                 # Ralph autonomous loop core logic
│   ├── term/                  # PTY/terminal handling
│   ├── testutil/              # Test utilities
│   │   └── integration/       # Testcontainers integration tests
│   ├── tui/                   # Reusable TUI components (BubbleTea/Lipgloss)
│   └── workspace/             # Bind vs Snapshot strategies
├── pkg/
│   ├── build/                 # Dockerfile templates, semver, npm registry
│   └── whail/                 # Reusable Docker engine with label-based isolation
└── templates/                 # clawker.yaml scaffolding
```

## Build Commands

```bash
go build -o bin/clawker ./cmd/clawker  # Build CLI
go test ./...                             # Run tests
./bin/clawker --debug run @              # Debug logging
./bin/clawker generate latest 2.1        # Generate versions.json

# Regenerate CLI documentation (after updating Cobra Example fields)
go run ./cmd/gen-docs --doc-path docs --markdown

# Acceptance tests (requires Docker, tests CLI workflows)
go test -tags=acceptance ./acceptance -v -timeout 15m
# Or: make acceptance
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
| `iostreams.IOStreams` | Testable I/O with TTY detection, colors, progress indicators |
| `iostreams.ColorScheme` | Color formatting that respects NO_COLOR and terminal theme |
| `prompts.Prompter` | Interactive prompts (String, Confirm, Select) with TTY awareness |
| `tui.ListModel` | Selectable list component with scrolling |
| `tui.PanelModel` | Bordered panel with focus management |
| `tui.SpinnerModel` | Animated spinner component |

See @.claude/docs/ARCHITECTURE.md for detailed abstractions.

## TUI Components (`internal/tui/`)

Reusable BubbleTea components for building terminal user interfaces. See `tui_components_package.md` memory for complete API reference.

### When to Use

- Building any TUI feature (e.g., `clawker ralph tui`)
- Need consistent styling across terminal interfaces
- Want responsive layouts that adapt to terminal size
- Building interactive components (lists, panels, spinners)

### Quick Reference

| Component | Usage |
|-----------|-------|
| `tui.ListModel` | Selectable lists with `SetItems()`, `SelectNext()`, `SelectedItem()` |
| `tui.PanelModel` | Bordered containers with `SetContent()`, `SetFocused()` |
| `tui.SpinnerModel` | Loading indicators with `Init()`, `Update()`, `View()` |
| `tui.StatusBarModel` | Status bars with `SetLeft()`, `SetCenter()`, `SetRight()` |
| `tui.HelpModel` | Help bars from `[]key.Binding` |

### Layout Helpers

```go
leftW, rightW := tui.SplitHorizontal(width, tui.SplitConfig{Ratio: 0.4})
content := tui.Stack(0, header, body, footer)  // Vertical stack
row := tui.Row(1, col1, col2, col3)            // Horizontal row
```

### Text & Time

```go
tui.Truncate(s, 20)              // "hello..." truncation
tui.FormatRelative(t)            // "2 hours ago"
tui.FormatDuration(d)            // "2m 30s"
tui.FormatUptime(d)              // "01:15:42"
```

### Input Handling

```go
case tea.KeyMsg:
    if tui.IsQuit(msg) { return m, tea.Quit }
    if tui.IsUp(msg)   { m.list = m.list.SelectPrev() }
    if tui.IsDown(msg) { m.list = m.list.SelectNext() }
```

### Styles

All components use consistent styles from `tui.ColorPrimary`, `tui.ColorSuccess`, etc. Use `tui.HeaderStyle`, `tui.PanelStyle`, `tui.ListItemSelectedStyle` for component-specific styling.

## IOStreams & Output (`internal/iostreams/`)

Testable I/O abstraction following the GitHub CLI pattern. See `iostreams_package.md` memory for complete API reference.

### When to Use

- **All CLI commands** should access I/O through `f.IOStreams` from Factory
- **Color output** that respects `NO_COLOR` env var and terminal capabilities
- **Progress indicators** (spinners) during long operations
- **TTY detection** for conditional interactive behavior

### Quick Reference

```go
ios := f.IOStreams  // *iostreams.IOStreams

// TTY Detection
ios.IsInputTTY()      // stdin is a terminal
ios.IsOutputTTY()     // stdout is a terminal
ios.IsInteractive()   // both stdin and stdout are TTYs
ios.CanPrompt()       // interactive AND not CI mode

// Color Output
cs := ios.ColorScheme()
cs.Green("Success")   // Returns unmodified string if colors disabled
cs.SuccessIcon()      // "✓" or "[ok]" based on color support

// Progress Indicators (animated spinner or text fallback)
ios.StartProgressIndicatorWithLabel("Building...")
defer ios.StopProgressIndicator()
// Or: ios.RunWithProgress("Building", func() error { ... })
// Text mode: set CLAWKER_SPINNER_DISABLED=1 or ios.SetSpinnerDisabled(true)

// Terminal Size
width := ios.TerminalWidth()
width, height := ios.TerminalSize()
```

### Environment Variables

| Variable | Effect |
|----------|--------|
| `NO_COLOR` | Disables color output when set |
| `CI` | Disables interactive prompts when set |
| `CLAWKER_PAGER` | Custom pager command (highest priority) |
| `PAGER` | Standard pager command |
| `CLAWKER_SPINNER_DISABLED` | Uses static text instead of animated spinner |

### Testing

```go
import "github.com/schmitthub/clawker/internal/iostreams"

ios := iostreams.NewTestIOStreams()
ios.SetInteractive(true)         // Simulate TTY
ios.SetColorEnabled(true)        // Enable colors
ios.SetTerminalSize(120, 40)     // Set terminal size
ios.SetProgressEnabled(true)     // Enable progress indicators
ios.SetSpinnerDisabled(true)     // Use text mode instead of animation
ios.InBuf.SetInput("user input") // Simulate stdin
// Verify output:
ios.OutBuf.String()              // stdout content
ios.ErrBuf.String()              // stderr content
```

## Prompts (`internal/prompts/`)

Interactive user prompts with TTY and CI awareness. See `prompts_package.md` memory for complete API reference.

### Quick Reference

```go
prompter := f.Prompter()  // *prompts.Prompter

// String prompt with default
name, err := prompter.String(prompts.PromptConfig{
    Message: "Project name",
    Default: "my-project",
})

// Confirmation prompt
proceed, err := prompter.Confirm("Continue?", false)  // [y/N]

// Selection prompt
options := []prompts.SelectOption{
    {Label: "Option A", Description: "First choice"},
    {Label: "Option B", Description: "Second choice"},
}
idx, err := prompter.Select("Choose:", options, 0)
```

**Non-interactive behavior:** In CI or non-TTY environments, prompts return defaults without user interaction.

## Host Proxy Architecture

The host proxy (`internal/hostproxy`) is a service mesh that mediates interactions between containers and the host.

### Components

| Component | File | Purpose |
|-----------|------|---------|
| `Server` | `server.go` | HTTP server handling proxy requests |
| `SessionStore` | `session.go` | Generic session management with TTL and cleanup |
| `CallbackChannel` | `callback.go` | OAuth callback registration, capture, and retrieval |
| `Manager` | `manager.go` | Lifecycle management of the proxy server |
| `GitCredential` | `git_credential.go` | Git credential forwarding handler |
| `SSHAgent` | `ssh_agent.go` | SSH agent forwarding handler |

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
| `/git/credential` | POST | Forward git credential get/store/erase to host |
| `/ssh/agent` | POST | Forward SSH agent requests to host (macOS) |
| `/callback/register` | POST | Register OAuth callback session |
| `/callback/{session}/data` | GET | Poll for captured callback data |
| `/callback/{session}` | DELETE | Cleanup session |
| `/cb/{session}/{path...}` | GET | Receive OAuth callbacks from browser |

### Container Scripts

| Script | Purpose |
|--------|---------|
| `host-open` | Opens URLs, detects OAuth flows, rewrites callbacks |
| `callback-forwarder` | Polls proxy and forwards callbacks to local server |
| `git-credential-clawker` | Git credential helper that forwards to host proxy |
| `ssh-agent-proxy` | SSH agent proxy binary that forwards via host proxy (macOS) |

### Git Credential Forwarding

Git credentials from the host are forwarded to containers via two mechanisms:

**HTTPS Credentials** (via host proxy):
```
CONTAINER                          HOST PROXY (:18374)                    HOST
    │                                     │                                  │
    │ git clone https://...               │                                  │
    │    ↓                                │                                  │
    │ git-credential-clawker get ────────►│ POST /git/credential             │
    │                                     │    ↓                             │
    │                                     │ git credential fill ────────────►│
    │                                     │    ↓                             │
    │                                     │◄── OS Keychain/Credential Manager│
    │◄────────────────────────────────────│                                  │
    │ credentials returned                │                                  │
```

**SSH Keys** (via agent forwarding):
- Linux: Bind mount `$SSH_AUTH_SOCK` to `/tmp/ssh-agent.sock`
- macOS: SSH agent proxy via host proxy (avoids Docker Desktop socket permission issues)

**macOS SSH Agent Flow** (via host proxy):
```
CONTAINER                              HOST PROXY (:18374)               HOST
    │                                         │                             │
    │ ssh-add -l                              │                             │
    │    ↓                                    │                             │
    │ ssh-agent-proxy (Go binary) ───────────►│ POST /ssh/agent            │
    │ creates ~/.ssh/agent.sock               │    ↓                        │
    │ (user-owned)                            │ net.Dial(SSH_AUTH_SOCK) ───►│
    │                                         │    ↓                        │
    │◄────────────────────────────────────────│◄── Agent response           │
    │ response returned                       │                             │
```

**Host Git Config**:
- Host `~/.gitconfig` mounted read-only to `/tmp/host-gitconfig`
- Entrypoint copies to container's `~/.gitconfig` (filtering credential.helper)
- Container's credential.helper configured to use `git-credential-clawker`

## Code Style

- **Logging**: `zerolog` only (never `fmt.Print` for debug)
- **Whail Client Enforcement**: No go package should be using the `github.com/moby/moby/client` library directly except for `@pkg/whail`. And no go package should be using `@pkg/whail` directly except for `@internal/docker`
- **Whail Client is a Decorator**: `@pkg/whail` decorates `github.com/moby/moby/client` and exposes the same interface so higher-level code can remain agnostic. All of the methods offered through `github.com/moby/moby/client` are available through `@pkg/whail` regardless of whether they are explicitly defined in `@pkg/whail` or not.
- **Whail Types in Tests**: When writing unit tests with mocks, use `whail.ImageListResult`, `whail.ImageSummary` etc. from `pkg/whail/types.go` - never import moby types directly in test files.
- **User output**: `cmdutil.PrintError()`, `cmdutil.PrintNextSteps()` to stderr
- **Data output**: stdout only for scripting (e.g., `ls` table)
- **Errors**: `cmdutil.HandleError(err)` for Docker errors
- **Cobra**: `PersistentPreRunE` (never `PersistentPreRun`), always include `Example` field

### Logging Details

**File Logging**: Logs are written to `~/.local/clawker/logs/clawker.log` with rotation (default 50MB, 7 days, 3 backups). Configure via `~/.local/clawker/settings.yaml`:

```yaml
logging:
  file_enabled: true   # default: true
  max_size_mb: 50      # default: 50
  max_age_days: 7      # default: 7
  max_backups: 3       # default: 3
```

**Interactive Mode**: During TUI sessions, console logs are suppressed but file logs capture everything. Use `logger.SetInteractiveMode(true)` BEFORE starting operations that log.

**Project/Agent Context**: Add context to logs for multi-container debugging:

```go
logger.SetContext(projectName, agentName)  // Set context for all subsequent logs
defer logger.ClearContext()                 // Clear when done
```

Log output with context:
```json
{"level":"info","project":"myapp","agent":"ralph","time":"...","message":"started"}
```

**Log Functions**:
- `logger.Debug()` - Never suppressed, for debugging
- `logger.Info()`, `logger.Warn()`, `logger.Error()` - Suppressed on console in interactive mode, always written to file
- `logger.Fatal()` - Never suppressed, exits program

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
| `init` | User-level setup with optional base image build |
| `project init` | Project-level setup (creates `clawker.yaml`) |
| `build` | Build container image |
| `run`, `start` | Aliases for `container run`, `container start` |
| `config check`, `monitor *` | Configuration/observability |
| `generate` | Generate versions.json for releases |
| `ralph run/status/reset` | Autonomous loop execution |

### Management Commands (Docker CLI-compatible)

| Command Group | Description |
|---------------|-------------|
| `container *` | Container lifecycle, inspection, interaction |
| `volume *` | Volume management |
| `network *` | Network management |
| `image *` | Image management |
| `project *` | Project management |

Example container commands:
- `clawker container list` (aliases: `ls`, `ps`)
- `clawker container run/create/start/stop/restart/kill`
- `clawker container logs/inspect/top/stats`
- `clawker container exec/attach/cp`
- `clawker container pause/unpause/rename/wait/update`
- `clawker container remove` (alias: `rm`)

These commands use positional arguments for resource names (e.g., `clawker container stop clawker.myapp.ralph`) matching Docker's interface.

### Image Resolution (@ Symbol)

The `@` symbol in `clawker run @` or `clawker container create @` triggers automatic image resolution:

1. **Project image** - Looks for `clawker-<project>:latest` with managed labels
2. **Default image** - Falls back to `default_image` from config/settings
3. **Error** - If neither found, prompts user with next steps

Resolution logic in `internal/cmdutil/resolve.go`:
- `ResolveImageWithSource()` - Returns image reference + source (project/default)
- `FindProjectImage()` - Searches for labeled project images
- `ResolveAndValidateImage()` - Validates default images exist, prompts for rebuild

When `opts.Image == "@"` (or empty), call `ResolveAndValidateImage()` without passing the "@" as explicit image.

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
  firewall:
    enable: true           # Enable network firewall (default: true)
    # add_domains:         # Add to default allowed domains
    #   - "api.openai.com"
    # remove_domains:      # Remove from default allowed domains
    #   - "registry.npmjs.org"
    # override_domains:    # Replace entire domain list (ignores add/remove)
    #   - "github.com"
  docker_socket: false
  git_credentials:
    forward_https: true    # Forward HTTPS credentials via host proxy (default: follows host_proxy)
    forward_ssh: true      # Forward SSH agent for git+ssh (default: true)
    copy_git_config: true  # Copy host ~/.gitconfig (default: true)

ralph:                            # Autonomous loop configuration
  max_loops: 50                   # Maximum loops before stopping (default: 50)
  stagnation_threshold: 3         # Loops without progress before circuit trips (default: 3)
  timeout_minutes: 15             # Per-loop timeout in minutes (default: 15)
  calls_per_hour: 100             # Rate limit: max calls per hour, 0 to disable (default: 100)
  completion_threshold: 2         # Completion indicators required for strict mode (default: 2)
  session_expiration_hours: 24    # Session TTL, auto-reset if older (default: 24)
  same_error_threshold: 5         # Same error repetitions before circuit trips (default: 5)
  output_decline_threshold: 70    # Output decline percentage that triggers trip (default: 70)
  max_consecutive_test_loops: 3   # Test-only loops before circuit trips (default: 3)
  loop_delay_seconds: 3           # Seconds to wait between loop iterations (default: 3)
  safety_completion_threshold: 5  # Force exit after N loops with completion indicators (default: 5)
  skip_permissions: false         # Pass --dangerously-skip-permissions to claude (default: false)
```

**Key types** (internal/config/schema.go): `DockerInstructions`, `InjectConfig`, `RunInstruction`, `CopyInstruction`, `GitCredentialsConfig`, `FirewallConfig`, `RalphConfig`

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
- Terminal visual state (alternate screen, cursor visibility, colors) must be reset separately from termios mode - `term.Restore()` handles both by sending escape sequences `\x1b[?1049l\x1b[?25h\x1b[0m\x1b(B` before restoring raw/cooked mode
- Terminal resize +1/-1 trick: When attaching to containers, first resize to (height+1, width+1) then to actual size. This forces a SIGWINCH event that triggers TUI apps to redraw. Matches Docker CLI's approach in attach.go. Implemented in `StreamWithResize`.
- Acceptance test assertions are case-sensitive - check actual command output (e.g., `building container image` not `Building image`)
- Acceptance tests need `mkdir $HOME/.local/clawker` and `security.firewall.enable: false` for isolation

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
go test -tags=integration ./internal/cmd/... -v -timeout 10m

# E2E tests (requires Docker, builds binary)
go test -tags=e2e ./internal/cmd/... -v -timeout 15m

# Acceptance tests (requires Docker, tests CLI workflows)
go test -tags=acceptance ./acceptance -v -timeout 15m
# Or: make acceptance
```

**Test Utilities:** The `internal/testutil` package provides:
- `Harness` - Isolated test environments with automatic cleanup
- `ConfigBuilder` - Fluent API for test configs
- `NewTestClient` / `NewRawDockerClient` - Docker client helpers
- `NewMockDockerClient` - Mock client for unit tests without Docker (use `whail.ImageListResult`, `whail.ImageSummary` for return types)
- `WaitForReadyFile` / `WaitForHealthy` - Container readiness detection
- `CleanupProjectResources` - Resource cleanup with error collection

See @.claude/rules/TESTING.md for detailed testing guidelines.

## Documentation

| File | Purpose |
|------|---------|
| @.claude/docs/CLI-VERBS.md | CLI command reference with flags and examples |
| @.claude/docs/ARCHITECTURE.md | Detailed abstractions and interfaces |
| @.claude/docs/CONTRIBUTING.md | Adding commands, updating docs |
| @.claude/rules/TESTING.md | CLI testing guidelines (**only access when writing command tests**) |
| @acceptance/README.md | Acceptance test authoring guide (txtar format, custom commands) |
| @README.md | see @.serena/readme_design_direction.md |

**Serena Memory:** `acceptance_testing_progress.md` - Implementation status, gotchas, and tips for writing acceptance tests.

**Critical**: After code changes, update README.md (user-facing) and CLAUDE.md (developer-facing) and memories (serena) as appropriate.

## Ralph Autonomous Loop Integration

When running in an autonomous loop via `clawker ralph run`, output a RALPH_STATUS
block at the end of EVERY response. This tells the loop controller your progress.

### RALPH_STATUS Block Format

```
---RALPH_STATUS---
STATUS: IN_PROGRESS | COMPLETE | BLOCKED
TASKS_COMPLETED_THIS_LOOP: <number>
FILES_MODIFIED: <number>
TESTS_STATUS: PASSING | FAILING | NOT_RUN
WORK_TYPE: IMPLEMENTATION | TESTING | DOCUMENTATION | REFACTORING
EXIT_SIGNAL: false | true
RECOMMENDATION: <one line summary>
---END_RALPH_STATUS---
```

### Field Definitions

| Field | Values | Description |
|-------|--------|-------------|
| STATUS | IN_PROGRESS, COMPLETE, BLOCKED | Current work state |
| TASKS_COMPLETED_THIS_LOOP | 0-N | Discrete tasks finished this iteration |
| FILES_MODIFIED | 0-N | Files changed this iteration |
| TESTS_STATUS | PASSING, FAILING, NOT_RUN | Current test suite state |
| WORK_TYPE | IMPLEMENTATION, TESTING, DOCUMENTATION, REFACTORING | What you did |
| EXIT_SIGNAL | true/false | Set true when ALL work is complete |
| RECOMMENDATION | text | Brief note on progress or next steps |

### Important Rules

1. **Always output the block** - Every response must end with RALPH_STATUS
2. **Be honest about progress** - TASKS_COMPLETED must reflect real work
3. **Signal completion clearly** - Set EXIT_SIGNAL: true only when truly done
4. **Include completion phrases** - When done, use phrases like:
  - "all tasks complete"
  - "project ready"
  - "work is done"
  - "implementation complete"
  - "no more work"
  - "finished"
  - "task complete"
  - "all done"
  - "nothing left to do"
  - "completed successfully"

### Example: Work in Progress

```
---RALPH_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 2
FILES_MODIFIED: 4
TESTS_STATUS: PASSING
WORK_TYPE: IMPLEMENTATION
EXIT_SIGNAL: false
RECOMMENDATION: Continue with user authentication module
---END_RALPH_STATUS---
```

### Example: All Work Complete

```
All tasks are now complete. The feature has been fully implemented and tested.

---RALPH_STATUS---
STATUS: COMPLETE
TASKS_COMPLETED_THIS_LOOP: 1
FILES_MODIFIED: 2
TESTS_STATUS: PASSING
WORK_TYPE: IMPLEMENTATION
EXIT_SIGNAL: true
RECOMMENDATION: All work complete, ready for review
---END_RALPH_STATUS---
```

### Example: Blocked

```
---RALPH_STATUS---
STATUS: BLOCKED
TASKS_COMPLETED_THIS_LOOP: 0
FILES_MODIFIED: 0
TESTS_STATUS: FAILING
WORK_TYPE: TESTING
EXIT_SIGNAL: false
RECOMMENDATION: Tests failing due to missing database fixture
---END_RALPH_STATUS---
```
