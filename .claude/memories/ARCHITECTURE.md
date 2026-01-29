# Clawker Architecture

> High-level architecture overview. Use Serena for detailed method/type exploration.

## System Layers

```
┌─────────────────────────────────────────────────────────────┐
│                     cmd/clawker                              │
│                   (Cobra commands)                           │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│                  internal/cmd/*                              │
│            (Command implementations)                         │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│                  internal/docker                             │
│            (Clawker-specific middleware)                     │
│         - Label conventions (com.clawker.*)                  │
│         - Naming schemes (clawker.project.agent)             │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│                   pkg/whail                                  │
│              (Reusable Docker engine library)                │
│         - Label-based selector injection                     │
│         - Managed resource isolation                         │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│              github.com/moby/moby                            │
│                  (Docker SDK)                                │
└─────────────────────────────────────────────────────────────┘
```

## Key Packages

### pkg/whail - Docker Engine Library

Reusable library with label-based resource isolation. Standalone for use in other projects.

**Core behavior:**
- Injects managed label filter on list operations
- Refuses to operate on resources without managed label
- Wraps Docker SDK methods with label enforcement

### internal/docker - Clawker Middleware

Thin layer configuring whail with clawker's conventions.

**Key abstractions:**
- Labels: `com.clawker.managed`, `com.clawker.project`, `com.clawker.agent`
- Names: `clawker.project.agent` (containers), `clawker.project.agent-purpose` (volumes)
- Client embeds `whail.Engine`, adding clawker-specific operations

### internal/cmd/* - CLI Commands

Two parallel command interfaces:

1. **Project Commands** (`clawker run/stop/logs`) - Project-aware, uses `--agent` flag
2. **Management Commands** (`clawker container/volume/network/image *`) - Docker CLI mimicry, positional args

Management command structure:
```
clawker container [list|inspect|logs|start|stop|kill|pause|unpause|restart|rename|wait|top|stats|update|exec|attach|cp|remove]
clawker volume    [list|inspect|create|remove|prune]
clawker network   [list|inspect|create|remove|prune]
clawker image     [list|inspect|build|remove|prune]
```

### internal/cmdutil - CLI Utilities

Shared utilities for all CLI commands.

**Key abstractions:**
- `Factory` - Lazy-initialized dependencies (Docker client, config, settings, registry, resolution, host proxy, IOStreams, Prompter)
- Error handling utilities (`HandleError`, `PrintNextSteps`, `PrintError`)
- Image resolution (`ResolveImageWithSource`, `FindProjectImage`)
- Project utilities

### internal/iostreams - Testable I/O

Testable I/O abstraction following the GitHub CLI pattern.

**Key types:**
- `IOStreams` - Core I/O with TTY detection, color support, progress indicators
- `ColorScheme` - Color formatting that bridges to `tui/styles.go`
- `TestIOStreams` - Test helper with in-memory buffers

**Features:**
- TTY detection (`IsInputTTY`, `IsOutputTTY`, `IsInteractive`, `CanPrompt`)
- Color support with `NO_COLOR` env var compliance
- Progress indicators (spinners) for long operations
- Pager support (`CLAWKER_PAGER`, `PAGER` env vars)
- Alternate screen buffer for full-screen TUIs
- Terminal size detection with caching

### internal/prompts - Interactive Prompts

User interaction utilities with TTY and CI awareness.

**Key types:**
- `Prompter` - Interactive prompts using IOStreams
- `PromptConfig` - Configuration for string prompts
- `SelectOption` - Options for selection prompts

**Methods:**
- `String(cfg)` - Text input with default and validation
- `Confirm(msg, defaultYes)` - Yes/no confirmation
- `Select(msg, options, defaultIdx)` - Selection from list

## Other Key Components

| Package | Purpose |
|---------|---------|
| `internal/workspace` | Bind vs Snapshot strategies for host-container file sharing |
| `internal/term` | PTY handling for interactive sessions |
| `internal/config` | Config loading, validation, project registry (`registry.go`) + resolver (`resolver.go`) |
| `internal/credentials` | Environment variable construction with allow/deny lists |
| `internal/monitor` | Observability stack (Prometheus, Grafana, OTel) |
| `internal/logger` | Zerolog setup |
| `internal/cmdutil` | Factory, error handling, output utilities |
| `internal/iostreams` | Testable I/O with TTY detection, colors, progress, pager |
| `internal/prompts` | Interactive prompts (String, Confirm, Select) |
| `internal/tui` | Reusable TUI components (BubbleTea/Lipgloss) - lists, panels, spinners, layouts |
| `internal/ralph/tui` | Ralph-specific TUI dashboard (uses `internal/tui` components) |
| `pkg/build` | Dockerfile generation, semver, npm registry client |

### internal/hostproxy - Host Proxy Service

HTTP service mesh mediating container-to-host interactions. See `internal/hostproxy/CLAUDE.md` for detailed architecture diagrams.

**Components:**
- `Server` - HTTP server on localhost (:18374)
- `SessionStore` - Generic session management with TTL
- `CallbackChannel` - OAuth callback interception/forwarding
- `Manager` - Lifecycle management (lazy init via Factory)
- `GitCredential` / `SSHAgent` - Credential forwarding handlers

**Key flows:**
- URL opening: Container → `host-open` script → POST /open/url → host browser
- OAuth: Container detects auth URL → registers callback session → rewrites URL → captures redirect
- Git HTTPS: `git-credential-clawker` → POST /git/credential → host credential store
- SSH (macOS): `ssh-agent-proxy` binary → POST /ssh/agent → host SSH agent

### internal/ralph - Autonomous Loop Engine

Runs Claude Code in non-interactive Docker exec with circuit breaker protection. See `internal/ralph/CLAUDE.md` for implementation details.

**Core types:**
- `Runner` - Main loop orchestrator
- `CircuitBreaker` - CLOSED/TRIPPED with multiple trip conditions
- `Session` / `SessionStore` - Persistent session state
- `RateLimiter` - Sliding window rate limiting
- `Analyzer` - RALPH_STATUS parser and completion detection

## Command Dependency Injection Pattern

Commands receive function references on their Options structs rather than `*Factory` directly. `NewCmd` still takes `*Factory` and wires the function references during command setup:

```go
type StopOptions struct {
    IOStreams   *iostreams.IOStreams
    Client     func(context.Context) (*docker.Client, error)
    Resolution func() *config.Resolution
    // ... command-specific fields
}
```

Run functions only accept `*Options`, keeping them testable without a full Factory.

## Container Naming & Labels

**Container names**: `clawker.project.agent` (3-segment) or `clawker.agent` (2-segment when project is empty)
**Volume names**: `clawker.project.agent-purpose` (purposes: `workspace`, `config`, `history`)

**Labels** (all `com.clawker.*`):

| Label | Purpose |
|-------|---------|
| `managed` | `true` — authoritative ownership marker |
| `project` | Project name (omitted when project is empty) |
| `agent` | Agent name |
| `version` | Clawker version |
| `image` | Source image reference |
| `workdir` | Host working directory |
| `created` | RFC3339 timestamp |
| `purpose` | Volume purpose (volumes only) |

**Filtering**: `ClawkerFilter()`, `ProjectFilter(project)`, `AgentFilter(project, agent)` generate Docker filter args.

**Strict ownership**: Clawker refuses to operate on resources without `com.clawker.managed=true`, even with the `clawker.` name prefix.

## Design Principles

1. **All Docker SDK calls go through pkg/whail** - Never bypass this layer
2. **Labels are authoritative** - `com.clawker.managed=true` determines ownership
3. **Naming is secondary** - `clawker.*` prefix for readability, not filtering
4. **stdout for data, stderr for status** - Enables scripting/composability
5. **User-friendly errors** - All errors include "Next Steps" guidance
