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

## Factory Dependency Injection (gh CLI Pattern)

Clawker follows the GitHub CLI's three-layer Factory pattern for dependency injection:

```
┌─────────────────────────────────────────────────────────────────────────┐
│  Layer 1: WIRING (internal/cmd/factory/default.go)                      │
│                                                                         │
│  factory.New(version, commit) → *cmdutil.Factory                        │
│    • Creates IOStreams, wires sync.Once closures for all dependencies    │
│    • Imports everything: config, docker, hostproxy, iostreams, prompts   │
│    • Called ONCE at entry point (internal/clawker/cmd.go)                │
│    • Tests NEVER import this package                                    │
├─────────────────────────────────────────────────────────────────────────┤
│  Layer 2: CONTRACT (internal/cmdutil/factory.go)                        │
│                                                                         │
│  Factory struct — pure data with closure fields, no methods             │
│    • Defines WHAT dependencies exist (Client, Config, Resolution, etc.) │
│    • Importable by all cmd/* packages without cycles                    │
│    • Also provides error handling, name resolution, project utilities   │
├─────────────────────────────────────────────────────────────────────────┤
│  Layer 3: CONSUMERS (internal/cmd/*)                                    │
│                                                                         │
│  NewCmdFoo(f *cmdutil.Factory) → *cobra.Command                         │
│    • Cherry-picks Factory closure fields into per-command Options struct │
│    • Run functions accept *Options only — never see Factory             │
│    • opts.Client = f.Client assigns closure, not method reference       │
└─────────────────────────────────────────────────────────────────────────┘
```

**Why this pattern:**
- **Testability**: Tests construct `&cmdutil.Factory{IOStreams: tio.IOStreams}` with only needed fields
- **Decoupling**: cmdutil has no construction logic; factory/ imports the heavy deps
- **Transparent**: `f.Client(ctx)` syntax is identical for methods and closure fields
- **Assignable**: `opts.Client = f.Client` works naturally for Options injection

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

Shared toolkit importable by all command packages.

**Key abstractions:**
- `Factory` — Pure struct with closure fields (no methods, no construction logic). Defines the dependency contract. Constructor lives in `internal/cmd/factory/default.go`.
- Error handling utilities (`HandleError`, `PrintNextSteps`, `PrintError`, `ExitError`)
- Image resolution (`ResolveImageWithSource`, `FindProjectImage`)
- Name resolution (`ResolveContainerName`, `ResolveContainerNames`)
- Project registration (`RegisterProject`)

### internal/cmd/factory - Factory Wiring

Constructor that builds a fully-wired `*cmdutil.Factory`. Imports all heavy dependencies (config, docker, hostproxy, iostreams, logger, prompts) and wires `sync.Once` closures.

**Key function:**
- `New(version, commit string) *cmdutil.Factory` — called exactly once at CLI entry point

**Dependency wiring order:**
1. IOStreams (eager) → 2. WorkDir (lazy closure) → 3. Config gateway (lazy, internal sync.Once) → 4. Client (lazy, reads Config) → 5. HostProxy (lazy) → 6. Prompter (lazy)

Tests never import this package — they construct minimal `&cmdutil.Factory{}` structs directly.

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

### internal/prompter - Interactive Prompts

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
| `internal/cmdutil` | Factory struct (closure fields), error handling, name resolution, output utilities |
| `internal/cmd/factory` | Factory constructor — wires real dependencies (sync.Once closures) |
| `internal/iostreams` | Testable I/O with TTY detection, colors, progress, pager |
| `internal/prompter` | Interactive prompts (String, Confirm, Select) |
| `internal/tui` | Reusable TUI components (BubbleTea/Lipgloss) - lists, panels, spinners, layouts |
| `internal/ralph/tui` | Ralph-specific TUI dashboard (uses `internal/tui` components) |
| `internal/build` | Image building, Dockerfile generation, semver, npm registry client |

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

Commands follow the gh CLI's NewCmd/Options/runF pattern. Factory closure fields flow through three steps:

**Step 1**: NewCmd receives Factory, cherry-picks closures into Options:
```go
func NewCmdStop(f *cmdutil.Factory, runF func(*StopOptions) error) *cobra.Command {
    opts := &StopOptions{
        IOStreams:   f.IOStreams,    // value field
        Client:     f.Client,       // closure field
        Resolution: f.Resolution,   // closure field
    }
```

**Step 2**: Options struct declares only what this command needs:
```go
type StopOptions struct {
    IOStreams   *iostreams.IOStreams
    Client     func(context.Context) (*docker.Client, error)
    Resolution func() *config.Resolution
    // command-specific fields...
}
```

**Step 3**: Run function receives only Options (never Factory):
```go
func stopRun(opts *StopOptions) error {
    client, err := opts.Client(context.Background())
    // ...
}
```

Factory fields are closures, so `opts.Client = f.Client` assigns the closure value directly — syntactically identical to a bound method reference.

## Command Scaffolding Template

Every command follows this 4-step pattern. No exceptions.

**Step 1 — Options struct** (declares only what this command needs):

```go
type StopOptions struct {
    // From Factory (assigned in constructor)
    IOStreams   *iostreams.IOStreams
    Client     func(context.Context) (*docker.Client, error)
    Resolution func() *config.Resolution

    // From flags (bound by Cobra)
    Force bool

    // From positional args (assigned in RunE)
    Names []string
}
```

**Step 2 — Constructor** accepts Factory + runF test hook:

```go
func NewCmdStop(f *cmdutil.Factory, runF func(context.Context, *StopOptions) error) *cobra.Command {
    opts := &StopOptions{
        IOStreams:   f.IOStreams,
        Client:     f.Client,
        Resolution: f.Resolution,
    }
```

**Step 3 — RunE** assigns positional args/flags to opts, then dispatches:

```go
    cmd := &cobra.Command{
        Use:   "stop [flags] [NAME...]",
        RunE: func(cmd *cobra.Command, args []string) error {
            opts.Names = args
            if runF != nil {
                return runF(cmd.Context(), opts)
            }
            return stopRun(cmd.Context(), opts)
        },
    }
    cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force stop")
    return cmd
```

**Step 4 — Unexported run function** receives only Options:

```go
func stopRun(ctx context.Context, opts *StopOptions) error {
    client, err := opts.Client(ctx)
    if err != nil {
        return err
    }
    // Business logic using only opts fields
}
```

**Nil-guard for runtime-context deps** (Pattern B — see DESIGN.md §3.4):

```go
func buildRun(ctx context.Context, opts *BuildOptions) error {
    if opts.Builder == nil {
        opts.Builder = build.NewBuilder(/* runtime args */)
    }
    // ...
}
```

**Parent registration** always passes `nil` for runF:

```go
cmd.AddCommand(stop.NewCmdStop(f, nil))
```

## Package Import DAG

Domain packages in `internal/` form a directed acyclic graph with three tiers:

```
┌─────────────────────────────────────────────────────────────────┐
│  LEAF PACKAGES — "Pure Utilities"                               │
│                                                                 │
│  Import: standard library only, no internal siblings            │
│  Imported by: anyone                                            │
│                                                                 │
│  Clawker examples: logger, tui, monitor                         │
└────────────────────────────┬────────────────────────────────────┘
                             │ imported by
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│  MIDDLE PACKAGES — "Core Domain Services"                       │
│                                                                 │
│  Import: leaves only (+ own sub-packages)                       │
│  Imported by: commands, composites, entry point                 │
│                                                                 │
│  Clawker examples:                                              │
│    config/ → logger                                             │
│    credentials/ → logger                                        │
│    hostproxy/ → logger                                          │
│    iostreams/ → logger, tui                                     │
│    prompter/ → iostreams                                        │
│    build/ → config + own subpackages (no docker)                │
└────────────────────────────┬────────────────────────────────────┘
                             │ imported by
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│  COMPOSITE PACKAGES — "Subsystems"                              │
│                                                                 │
│  Import: leaves + middles + own sub-packages                    │
│  Imported by: commands only                                     │
│                                                                 │
│  Clawker examples:                                              │
│    docker/ → build, config, logger, pkg/whail, pkg/whail/buildkit│
│    cmdutil/ → config, docker, hostproxy, iostreams, prompter    │
│    workspace/ → config, docker, logger                          │
│    term/ → docker, logger                                       │
│    ralph/ → docker, logger                                      │
│    project/ → config, iostreams, logger                         │
└─────────────────────────────────────────────────────────────────┘
```

### Import Direction Rules

```
  ✓  middle → leaf                 config imports logger
  ✓  composite → middle            docker imports build, config
  ✓  composite → leaf              ralph imports logger
  ✓  composite → own children      ralph imports ralph/tui

  ✗  leaf → middle                 logger must never import config
  ✗  leaf → leaf (sibling)         leaves have zero internal imports
  ✗  middle ↔ middle (unrelated)   config must never import prompter
  ✗  Any cycle                     A → B → A is always wrong
```

**Lateral imports** between unrelated middle packages are the most common violation. If two middle packages need shared behavior, extract the shared part into a leaf package.

### Where `cmdutil` Fits

`cmdutil` is a **composite package** that commands and the entry point import. It imports config, docker, hostproxy, iostreams, and prompter — touching both domain services and the command framework.

If a utility in `cmdutil` is also needed by domain packages outside commands, extract it into a leaf package:

```
BEFORE (leaky):  docker/naming.go ──imports──▶ cmdutil
AFTER  (clean):  docker/naming.go ──imports──▶ naming/ (standalone leaf)
                 cmdutil/resolve.go ──imports──▶ naming/
```

**Rule**: If a helper touches the command framework, it stays in `cmdutil`. If it's a pure data utility, extract it.

## Anti-Patterns

| # | Anti-Pattern | Why It's Wrong |
|---|-------------|----------------|
| 1 | Run function depending on `*Factory` | Breaks interface segregation; use `*Options` only |
| 2 | Calling closure fields during construction | Defeats lazy initialization; closures are evaluated on use |
| 3 | Tests importing `internal/cmd/factory` | Construct minimal `&cmdutil.Factory{}` struct literals instead |
| 4 | Mutating Factory closures at runtime | Closures are set once in the constructor, never reassigned |
| 5 | Adding methods to Factory | Factory is a pure struct; use closure fields for all dependency providers |
| 6 | Skipping `runF` parameter | Every `NewCmd` MUST accept `runF` even if not yet tested |
| 7 | Direct Factory field access in run functions | Extract into Options first; run function never sees Factory |

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
6. **Factory DI pattern (gh CLI)** — Pure struct in cmdutil, constructor in cmd/factory, Options in commands
