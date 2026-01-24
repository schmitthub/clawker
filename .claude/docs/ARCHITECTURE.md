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
- `Factory` - Lazy-initialized dependencies (Docker client, config, settings, host proxy)
- `IOStreams` - Testable I/O with TTY detection, color support, progress indicators
- `ColorScheme` - Color formatting that bridges to `tui/styles.go`
- `Prompter` - Interactive prompts respecting TTY and CI detection
- Error handling utilities (`HandleError`, `PrintNextSteps`, `PrintError`)

**IOStreams features:**
- TTY detection (`IsInputTTY`, `IsOutputTTY`, `IsInteractive`, `CanPrompt`)
- Color support with `NO_COLOR` env var compliance
- Progress indicators (spinners) for long operations
- Pager support (`CLAWKER_PAGER`, `PAGER` env vars)
- Alternate screen buffer for full-screen TUIs
- Terminal size detection with caching

## Other Key Components

| Package | Purpose |
|---------|---------|
| `internal/workspace` | Bind vs Snapshot strategies for host-container file sharing |
| `internal/term` | PTY handling for interactive sessions |
| `internal/config` | Viper config loading and validation |
| `internal/credentials` | Environment variable construction with allow/deny lists |
| `internal/monitor` | Observability stack (Prometheus, Grafana, OTel) |
| `internal/logger` | Zerolog setup |
| `internal/cmdutil` | Factory, IOStreams, error handling, output utilities |
| `internal/tui` | Reusable TUI components (BubbleTea/Lipgloss) - lists, panels, spinners, layouts |
| `internal/ralph/tui` | Ralph-specific TUI dashboard (uses `internal/tui` components) |
| `pkg/build` | Dockerfile generation, semver, npm registry client |

## Design Principles

1. **All Docker SDK calls go through pkg/whail** - Never bypass this layer
2. **Labels are authoritative** - `com.clawker.managed=true` determines ownership
3. **Naming is secondary** - `clawker.*` prefix for readability, not filtering
4. **stdout for data, stderr for status** - Enables scripting/composability
5. **User-friendly errors** - All errors include "Next Steps" guidance
