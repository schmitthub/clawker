# Architecture

> For detailed method/type exploration, use Serena or your IDE's Go tooling. This document provides the high-level picture.

## System Layers

Clawker is organized in four layers, from user-facing CLI down to the Docker SDK:

```
┌──────────────────────────────────────────────────────────┐
│                    cmd/clawker                            │
│                  (Cobra commands)                         │
└────────────────────┬─────────────────────────────────────┘
                     │
┌────────────────────▼─────────────────────────────────────┐
│                 internal/cmd/*                            │
│           (Command implementations)                       │
└────────────────────┬─────────────────────────────────────┘
                     │
┌────────────────────▼─────────────────────────────────────┐
│                 internal/docker                           │
│           (Clawker-specific middleware)                    │
│        - Label conventions (dev.clawker.*)                │
│        - Naming schemes (clawker.project.agent)           │
└────────────────────┬─────────────────────────────────────┘
                     │
┌────────────────────▼─────────────────────────────────────┐
│                  pkg/whail                                │
│             (Reusable Docker engine library)              │
│        - Label-based selector injection                   │
│        - Managed resource isolation                       │
└────────────────────┬─────────────────────────────────────┘
                     │
┌────────────────────▼─────────────────────────────────────┐
│             github.com/moby/moby                          │
│                 (Docker SDK)                              │
└──────────────────────────────────────────────────────────┘
```

## Package Dependency Graph

Packages follow a strict DAG (Directed Acyclic Graph) organized in four tiers:

### Tier 1: Leaf Packages (Pure Utilities)

Import only the standard library or external-only dependencies. Importable by anyone.

- `build` — Build-time metadata (version, date)
- `logger` — Zerolog setup
- `term` — Terminal capabilities + raw mode (sole `x/term` gateway)
- `text` — Pure ANSI-aware text utilities
- `signals` — OS signal utilities
- `git` — Git operations, worktree management (stdlib + go-git only)
- `docs` — CLI documentation generation

### Tier 2: Foundation Packages (Infrastructure)

Import leaves only. Universally imported as infrastructure.

- `config` — Config loading, validation, project registry + resolver
- `iostreams` — I/O streams, TTY detection, colors, styles, spinners, progress
- `cmdutil` — Factory struct (DI container), error types, arg validators

### Tier 3: Middle Packages (Core Domain Services)

Import leaves + foundation.

- `bundler` — Dockerfile generation, content hashing, semver, npm registry
- `tui` — BubbleTea models, viewports, panels, progress display
- `containerfs` — Host config preparation for container init
- `hostproxy` — Host proxy for container-to-host communication
- `socketbridge` — SSH/GPG agent forwarding via muxrpc
- `prompter` — Interactive prompts with TTY/CI awareness
- `project` — Project registration in user registry

### Tier 4: Composite Packages (Subsystems)

Import all lower tiers. Imported by commands only.

- `docker` — Clawker middleware wrapping whail Engine with labels/naming
- `workspace` — Bind vs Snapshot strategies
- `dev` — Autonomous loop engine with circuit breaker

### Import Rules

```
  OK:  foundation → leaf           (config imports logger)
  OK:  middle → leaf               (bundler imports logger)
  OK:  middle → foundation         (bundler imports config)
  OK:  composite → middle          (docker imports bundler)
  OK:  composite → foundation      (docker imports config)

  BAD: leaf → foundation           (logger must never import config)
  BAD: middle ↔ middle (sibling)   (bundler must never import prompter)
  BAD: foundation ↔ foundation     (config must never import iostreams)
  BAD: any cycle                   (A → B → A is always wrong)
```

## Dependency Injection: The Factory Pattern

Clawker follows the GitHub CLI's three-layer Factory pattern:

### Layer 1: Wiring (`internal/cmd/factory/default.go`)

Creates `*cmdutil.Factory` with all dependencies wired as closures. Called once at entry point. Tests never import this package.

### Layer 2: Contract (`internal/cmdutil/factory.go`)

`Factory` is a pure struct with closure fields — no methods. Defines what dependencies exist. Importable by all command packages without cycles.

**Eager fields** (set directly): `Version`, `IOStreams`, `TUI`

**Lazy fields** (closures with `sync.Once`): `Config`, `Client`, `GitManager`, `HostProxy`, `SocketBridge`, `Prompter`

### Layer 3: Consumers (`internal/cmd/*`)

Commands cherry-pick Factory closures into per-command `Options` structs. Run functions accept `*Options` only — never see Factory.

```go
// Constructor receives Factory, cherry-picks into Options
func NewCmdStop(f *cmdutil.Factory, runF func(*StopOptions) error) *cobra.Command {
    opts := &StopOptions{
        IOStreams: f.IOStreams,
        Client:   f.Client,  // closure, not call
    }
    // ...
}

// Run function only sees Options
func stopRun(ctx context.Context, opts *StopOptions) error {
    client, err := opts.Client(ctx)
    // ...
}
```

## Key Abstractions

| Abstraction | Package | Purpose |
|-------------|---------|---------|
| `whail.Engine` | `pkg/whail` | Reusable Docker engine with label-based resource isolation |
| `docker.Client` | `internal/docker` | Clawker middleware (labels, naming) wrapping whail |
| `config.Config` | `internal/config` | Gateway type — lazy-loads Project, Settings, Resolution, Registry |
| `Factory` | `internal/cmdutil` | DI struct (closure fields); constructor in `cmd/factory` |
| `WorkspaceStrategy` | `internal/workspace` | Bind (live mount) vs Snapshot (ephemeral copy) |
| `CreateContainer()` | `internal/cmd/container/shared` | Single entry point for container creation — workspace, config, env, create, inject |
| `tui.TUI` | `internal/tui` | Factory noun for presentation layer |
| `tui.RunProgress` | `internal/tui` | Generic progress display (BubbleTea TTY + plain text) |
| `hostproxy.HostProxyService` | `internal/hostproxy` | Interface for host proxy operations; `Manager` is concrete impl |
| `loop.Runner` | `internal/loop` | Autonomous loop orchestrator with circuit breaker |

## Container Naming and Labels

**Container names**: `clawker.project.agent` (3-segment) or `clawker.agent` (2-segment when project is empty)

**Volume names**: `clawker.project.agent-purpose` (purposes: `workspace`, `config`, `history`)

**Labels** (all under `dev.clawker.*`):

| Label | Purpose |
|-------|---------|
| `managed` | `true` — authoritative ownership marker |
| `project` | Project name (omitted when empty) |
| `agent` | Agent name |
| `version` | Clawker version |
| `image` | Source image reference |

Labels are the authoritative source of truth for resource ownership. Names are secondary, for human readability. Clawker refuses to operate on resources without `dev.clawker.managed=true`.

## Presentation Layer

Commands follow a 4-scenario output model:

| Scenario | Description | Packages |
|----------|-------------|----------|
| Static | Print and done | `iostreams` + `fmt` |
| Static-interactive | Output with y/n prompts | `iostreams` + `prompter` |
| Live-display | Continuous rendering, no input | `iostreams` + `tui` |
| Live-interactive | Full keyboard input, navigation | `iostreams` + `tui` |

**Import boundaries** (enforced):
- Only `iostreams` imports `lipgloss`
- Only `tui` imports `bubbletea`/`bubbles`
- Only `term` imports `golang.org/x/term`

## Host Proxy Architecture

The host proxy bridges container-to-host communication:

- **URL opening**: Container script → POST to proxy → host browser
- **OAuth**: Intercepts auth URL, rewrites callback, captures redirect
- **Git HTTPS**: Container credential helper → proxy → host credential store
- **SSH/GPG**: `socketbridge.Manager` → `docker exec` muxrpc → Unix sockets
