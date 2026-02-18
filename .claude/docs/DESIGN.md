# Clawker Design Document

Clawker is a Go CLI tool that wraps the Claude Code agent in secure, reproducible Docker containers.

## 1. Philosophy: "The Padded Cell"

### Core Principle

Standard Docker gives users full control—which is dangerous for beginners and risky when running autonomous AI agents. Clawker creates a "padded cell": users interact with Docker-like commands, but operations are isolated to clawker-managed resources only.

### Threat Model

Clawker protects everything **outside** the container from what happens **inside**:

- Host filesystem protected from container writes (bind mounts controlled)
- Host network protected via firewall (outbound controlled, inbound open)
- Other Docker resources protected via label-based isolation
- The container itself is disposable—a new one can always be created

We do **not** inherit Docker's threat model. If Docker allows catastrophic commands, clawker permits them—but only against clawker-managed resources.

## 2. Core Concepts

### 2.1 Project

A clawker project is defined by configuration files. Every clawker command requires project context.

**Configuration Precedence** (highest to lowest):

1. CLI flags
2. Environment variables
3. Project config (`./clawker.yaml`)
4. User defaults (from settings)

Higher precedence wins silently (no warnings on override).

**Configuration Files**:

| Location | Purpose |
|----------|---------|
| `./clawker.yaml` | Project-specific config (schema: `Config`) |
| `~/.local/clawker/settings.yaml` | User settings (`Settings`: default_image, logging) |
| `~/.local/clawker/projects.yaml` | Project registry (`ProjectRegistry`: slug→path map) |

**Project Resolution**: The `config.Config` gateway (implementing `config.Provider`) resolves the current working directory to a registered project via longest-prefix path matching during lazy initialization. Resolution results are accessed via `Provider.ProjectKey()`, `Provider.WorkDir()`, and `Provider.ProjectFound()`. The `Project` schema struct's `Project` field is `yaml:"-"` — injected by the loader from the registry, never persisted in YAML.

### 2.2 Agent

An agent is a named container instance. Agents have a many-to-many relationship with projects and images:

- One project can have multiple agents
- One agent belongs to one project
- Multiple agents can share an image
- One agent uses one image

**Naming Convention**: `clawker.<project>.<agent>`

- Example: `clawker.myapp.dev`, `clawker.backend.worker`

If no agent name provided, one is generated randomly (Docker-style adjective-noun).

### 2.3 Resource Identification

Three mechanisms identify clawker-managed resources:

| Mechanism | Purpose | Authority |
|-----------|---------|-----------|
| **Labels** | Filtering, ownership verification | Authoritative source of truth |
| **Naming** | Human readability, programmatic filtering when needed | Secondary |
| **Network** | Container-to-container communication, security isolation | Functional |

**Key Labels**:

```
dev.clawker.managed=true
dev.clawker.project=<project-name>   # omitted when project is empty
dev.clawker.agent=<agent-name>
```

**Naming Segments**:
- 3-segment: `clawker.project.agent` (e.g., `clawker.myapp.dev`) — when project is set
- 2-segment: `clawker.agent` (e.g., `clawker.dev`) — when project is empty (orphan project)

**Strict Ownership**: Clawker refuses to operate on resources without `dev.clawker.managed=true` label, even if they have the `clawker.` name prefix.

### Project Registry Lifecycle

1. **Register**: `clawker project init` or `clawker project register` adds a slug→path entry to `projects.yaml`
2. **Lookup**: `Factory.Config()` returns a `config.Provider` that internally resolves the project from `os.Getwd()` via longest-prefix path match against the registry
3. **Orphan projects**: If no project is resolved, resources get 2-segment names and omit the project label

## 3. System Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     cmd/clawker                              │
│                   (Cobra commands)                           │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│                  internal/docker                             │
│            (Clawker-specific middleware)                     │
│         - Config-dependent (uses Viper)                      │
│         - Exposes interface for commands                     │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│                   pkg/whail                                  │
│              (External Engine - Reusable docker decorator)   │
│         - Label-based selector injection                     │
│         - Whitelist of allowed operations                    │
│         - Standalone library for other projects              │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│              github.com/moby/moby                            │
│                  (Docker SDK)                                │
└─────────────────────────────────────────────────────────────┘
```

### 3.1 External Engine (`pkg/whail`)

A standalone, reusable library for building isolated Docker clients. Designed for use in other container-based AI wrapper projects. Decorates docker client to scope methods to resources with managed labels while adding utility methods for filtering, checking, option merging.

**Core Mechanism: Selector Injection**

The engine wraps Docker SDK and injects label filtering logic for some methods. :

```go
// User calls:
engine.ContainerList(ctx, opts)

// Engine transforms to:
// ContainerList lists containers matching the filter.
// The managed label filter is automatically injected.
func (e *Engine) ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error) {
 options.Filters = e.injectManagedFilter(options.Filters)
 return e.APIClient.ContainerList(ctx, options)
}

// User calls:
engine.ContainerInspect(ctx, containerID)
// ContainerInspect inspects a container.
// Only inspects managed containers.
func (e *Engine) ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error) {
 isManaged, err := e.IsContainerManaged(ctx, containerID) // added checker method
 if err != nil {
  return types.ContainerJSON{}, ErrContainerInspectFailed(containerID, err)
 }
 if !isManaged {
  return types.ContainerJSON{}, ErrContainerNotFound(containerID)
 }
 return e.APIClient.ContainerInspect(ctx, containerID)
}

// checker method for if container is managed
// IsContainerManaged checks if a container has the managed label.
func (e *Engine) IsContainerManaged(ctx context.Context, containerID string) (bool, error) {
 info, err := e.APIClient.ContainerInspect(ctx, containerID)
 if err != nil {
  if client.IsErrNotFound(err) {
   return false, nil
  }
  return false, err
 }

 val, ok := info.Config.Labels[e.managedLabelKey]
 return ok && val == e.managedLabelValue, nil
}
```

**Whitelist Approach**

`Engine` is a concrete struct (not an interface) that embeds the Docker `APIClient` and selectively overrides methods with label-injecting wrappers. Only wrapped methods enforce isolation — unwrapped SDK methods remain accessible but are not used by clawker's higher layers.

**Blocked by Design**: Clawker's `internal/docker` layer only calls Engine methods that have label enforcement. Operations that cannot apply label filters (e.g., `system prune` without filters) are not called by clawker code.

**Docker API Compatibility**: Minimum supported version defined at compile-time. No feature detection or graceful degradation—fail fast on incompatible versions.

### 3.3 Factory Dependency Injection

The CLI layer follows the GitHub CLI's Factory pattern:

```
internal/clawker/cmd.go
    │ calls factory.New()
    ▼
internal/cmd/factory/default.go  ──imports──▶  internal/cmdutil  ◀──imports──  internal/cmd/*
    │                                               ▲
    │ imports heavy deps                            │
    │ (config, docker, hostproxy,                   │ imports for Factory type
    │  iostreams, logger, prompts)                  │ + utilities
    ▼                                               │
Returns *cmdutil.Factory                      Commands consume
with all closures wired                       *cmdutil.Factory
```

Factory is a pure struct with 9 closure/value fields — no methods. 3 eager (set directly), 6 lazy (closures with `sync.Once`):

**Eager**: `Version` (string), `IOStreams` (`*iostreams.IOStreams`), `TUI` (`*tui.TUI`)
**Lazy**: `Config` (`func() config.Provider`), `Client` (`func(ctx) (*docker.Client, error)`), `GitManager` (`func() (*git.GitManager, error)`), `HostProxy` (`func() hostproxy.HostProxyService`), `SocketBridge` (`func() socketbridge.SocketBridgeManager`), `Prompter` (`func() *prompter.Prompter`)

The constructor in `internal/cmd/factory/default.go` wires all closures. Commands extract closures into per-command Options structs. Run functions only accept `*Options`, never `*Factory`.

### 3.4 Dependency Placement Decision Tree

When adding a new command helper or heavy dependency:

```
"Where does my heavy dependency go?"
              │
              ▼
Can it be constructed at startup,
before any command runs?
              │
       ┌──────┴──────┐
       YES            NO (needs CLI args, runtime context)
       │              │
       ▼              ▼
  3+ commands?    Lives in: internal/<package>/
       │          Constructed in: run function
  ┌────┴────┐     Tested via: inject mock on Options
  YES       NO
  │         │
  ▼         ▼
FACTORY   OPTIONS STRUCT
FIELD     (command imports package directly)
```

Rules:
- Implementation always lives in `internal/<package>/` — never in `cmdutil/`
- `cmdutil/` contains only: Factory struct (DI container), output utilities, arg validators
- Heavy command helpers live in dedicated packages: `internal/bundler/` (build utilities), `internal/project/` (registration), `internal/docker/` (container naming). Image resolution helpers live in `internal/cmdutil/` (`ResolveImageWithSource`, `FindProjectImage`)

See also `.claude/rules/dependency-placement.md` (auto-loaded).

#### Pattern A vs Pattern B — Side-by-Side

```
                    PATTERN A                      PATTERN B
                    Factory Field                  Options Nil-Guard
                    ─────────────                  ─────────────────
  Declared in       cmdutil/factory.go             cmd/<verb>/<verb>.go
  Constructed in    cmd/factory/default.go         run function (nil-guard)
  Constructed       once, at startup               per command execution
  Depends on        config, other Factory fields   CLI args, resolved targets
  Test injection    stub closure on Factory        set field on Options directly

  Production flow   factory.New() → closure        if opts.X == nil {
                    → stored on Factory                opts.X = real.New(...)
                    → extracted to Options          }

  Test flow         f := &cmdutil.Factory{          opts := &VerbOptions{
                        SomeDep: mockFn,                SomeDep: &mock{},
                    }                               }
```

**Key rule**: Breadth of use does NOT determine the pattern. A dependency used by 40 commands still uses Pattern B if it needs runtime context (CLI args, resolved targets, user selections).

### 3.2 Internal Client (`internal/docker`)

Clawker-specific middleware that builds on the External Engine:

- Initializes External Engine with clawker's label configuration
- Loads configuration via Viper (merged from all sources)
- Handles clawker-specific logic (agent naming, volume conventions)
- Exposes high-level interface for Cobra commands

```go
type Client struct {
    engine  docker.Engine
    config  *config.Config
    project string
}

func (c *Client) RunAgent(ctx context.Context, agent string, opts RunOptions) error {
    // High-level operation combining multiple engine calls
}
```

## 4. Command Taxonomy

Commands mirror the Docker CLI structure:

### 4.1 Structure

| Pattern | Usage | Examples |
|---------|-------|----------|
| `clawker <verb>` | Container runtime operations | `run`, `stop`, `build` |
| `clawker <noun> <verb>` | Resource operations | `container ls`, `volume rm` |

### 4.2 Primary Nouns

All Docker nouns are supported:

- `container` - Container management
- `volume` - Volume management
- `network` - Network management
- `image` - Image management
- `project` - Project registry management
- `worktree` - Git worktree management

### 4.3 Primary Verbs

**Runtime Verbs** (top-level):

- `init` - Initialize project configuration
- `build` - Build container image
- `run` - Build and run agent (idempotent)
- `stop` - Stop agent(s)
- `restart` - Restart agent(s)

**Resource Verbs** (under nouns):

- `ls` / `list` - List resources
- `rm` / `remove` - Remove resources
- `inspect` - Show detailed information
- `prune` - Remove unused resources

**Configuration Verbs**:

- `config check` - Validate configuration

**Observability Verbs**:

- `monitor up` - Start monitoring stack
- `monitor down` - Stop monitoring stack
- `monitor status` - Show stack status

### 4.4 Opaque to Docker

Clawker does **not** expose Docker passthrough commands. Users cannot run arbitrary Docker commands through clawker. The interface is completely opaque to Docker internals.

## 5. State Management

### 5.1 Stateless CLI

Clawker stores **no local state**. All state lives in Docker:

- Container state (running, stopped, etc.)
- Labels (project, agent, metadata)
- Volumes (workspace, config, history)

Benefits:

- Multiple clawker instances can operate concurrently
- No state synchronization issues
- Recovery is trivial—just query Docker

### 5.2 Idempotency

Operations mirror Docker's idempotency behavior:

- `run` on existing container: Attach to existing (race condition resolution)
- `build` when image exists: Rebuild (use cache by default)
- `stop` on stopped container: No-op success
- `rm` on non-existent: Error (or success with force)

### 5.3 Orphaned Resources

Handled identically to Docker:

- Volumes from deleted containers persist
- Images with no containers persist
- `prune` commands clean up unused resources

## 6. Error Handling

### 6.1 Error Taxonomy

| Category | Source | Handling |
|----------|--------|----------|
| User errors | Bad input, missing config | Next Steps guidance |
| Docker errors | Daemon unavailable, permission denied | Pass through directly |
| Container runtime | Captured from stderr | Stream to user |
| Network errors | Timeout, DNS failure | Pass through with context |
| Internal errors | Bugs, unexpected state | Stack trace in debug mode |

### 6.2 User Error Format

```
Error: No clawker.yaml found in current directory

Next steps:
  1. Run 'clawker init' to create a configuration
  2. Or change to a directory with clawker.yaml
```

### 6.3 Exit Codes

General codes (extensible later):

- `0` - Success
- `1` - General error

Pattern follows GitHub CLI conventions.

## 7. Security Model

### 7.1 Credential Handling

Credentials are passed via environment variables:

- `ANTHROPIC_API_KEY` - Primary authentication
- `ANTHROPIC_AUTH_TOKEN` - Token-based auth
- Filtered from `.env` files (sensitive pattern matching)

Note: Environment variables are visible in `docker inspect`. This is accepted for simplicity.

**Container credential injection**: When `claude_code.use_host_auth` is true (default),
`containerfs.PrepareCredentials()` copies the host's `~/.claude/.credentials.json` into
the config volume. The `docker.CopyToVolume` two-phase chown ensures UID 1001 ownership.
This supplements environment variable passing for persistent credential storage.

### 7.2 Firewall

Firewall configuration is **out of scope** for the core design. Users implement firewall via:

- Custom Dockerfile with iptables rules
- Entrypoint scripts
- Network policies

The `security.enable_firewall` config option triggers inclusion of a firewall init script in the generated Dockerfile.

### 7.3 Strict Label Ownership

Clawker **refuses** to operate on resources without proper labels:

```go
if !hasLabel(container, "dev.clawker.managed", "true") {
    return ErrNotManagedResource
}
```

This prevents accidental operations on user's non-clawker Docker resources.

## 8. Multi-Agent Operations

### 8.1 Relationships

```
┌─────────┐     ┌─────────┐     ┌─────────┐
│ Project │────<│  Agent  │>────│  Image  │
└─────────┘     └─────────┘     └─────────┘
     1               *               *
```

- One project has many agents
- Many agents can share one image
- One agent uses one image at a time

### 8.2 Race Condition Resolution

When two processes attempt to create the same agent:

1. First process creates the container
2. Second process detects existing container
3. Second process attaches to existing container

No error, no duplicate—deterministic behavior.

## 9. Observability

### 9.1 Monitoring Stack

A Docker Compose stack on `clawker-net`:

- **OpenTelemetry Collector** - Telemetry aggregation
- **Prometheus** - Metrics collection
- **Jaeger** - Distributed tracing
- **Loki** - Log aggregation
- **Grafana** - Visualization dashboards

Container images are built with OTEL environment variables pointing to the collector.

### 9.2 Verbosity Levels

| Flag | Level | Output |
|------|-------|--------|
| `-q, --quiet` | Quiet | Minimal output |
| (default) | Normal | Human-friendly |
| `-v, --verbose` | Verbose | Detailed output |
| `-D, --debug` | Debug | Developer diagnostics |

### 9.3 Progress Reporting

| Operation Type | Indicator | Package |
|----------------|-----------|---------|
| Indeterminate (short) | Goroutine spinner | `iostreams` (`StartSpinner`, `RunWithSpinner`) |
| Determinate (image pull) | Progress bar | `iostreams` (`ProgressBar`) |
| Multi-step (image build) | Tree display with per-stage child windows | `tui` (`RunProgress`) |
| Streaming (logs) | Partial screen with progress indicators | `iostreams` |

**Tree display** is the primary pattern for multi-step operations. TTY mode renders a BubbleTea sliding-window view; plain mode prints incremental text. Both driven by the same `chan ProgressStep` event channel.

### 9.4 Presentation Layer Patterns

The **4-scenario output model** determines which packages a command imports:

| Scenario | Packages | When to use |
|----------|----------|-------------|
| Static | `iostreams` + `fmt` | Print-and-done: lists, inspect, rm |
| Static-interactive | `iostreams` + `prompter` | Confirmation prompts mid-flow |
| Live-display | `iostreams` + `tui` | Continuous rendering, no user input |
| Live-interactive | `iostreams` + `tui` | Full keyboard input, navigation |

**TUI Factory noun** (`*tui.TUI`): Created eagerly by Factory. Commands capture `f.TUI` in their Options struct. Lifecycle hooks are registered later via `RegisterHooks()` — this decouples hook registration from TUI creation.

**Key types**:
- `tui.TUI` — Factory noun wrapping IOStreams; owns hooks + delegates to `RunProgress`
- `tui.RunProgress(ctx, ios, ch, cfg)` — Generic progress display (BubbleTea TTY + plain text fallback)
- `tui.ProgressStep` — Channel event: `{ID, Name, Status, LogLine, Cached, Error}`
- `tui.ProgressDisplayConfig` — Configuration with `CompletionVerb`, callback functions (`IsInternal`, `CleanName`, `ParseGroup`, `FormatDuration`, `OnLifecycle`)
- `tui.LifecycleHook` — Generic hook function type; threaded via config, nil = no-op
- `tui.HookResult` — `{Continue bool, Message string, Err error}` — controls post-hook flow

## 10. Container Lifecycle

### 10.1 State Machine

```
           ┌──────────┐
           │ Building │
           └────┬─────┘
                ▼
     ┌──────────────────┐
     │     Created      │
     └────────┬─────────┘
              ▼
     ┌──────────────────┐     ┌──────────┐
     │     Running      │────▶│  Paused  │
     └────────┬─────────┘     └──────────┘
              ▼
     ┌──────────────────┐
     │     Stopped      │
     └────────┬─────────┘
              ▼
     ┌──────────────────┐
     │     Removed      │
     └──────────────────┘
```

### 10.2 Signal Handling

All signals (Ctrl+C, etc.) are passed through to Docker. Clawker does not intercept or transform signals.

### 10.3 Restart Policies

Restart policies are passed directly to Docker:

```yaml
agent:
  restart: unless-stopped
```

### 10.4 Container Init

New containers require one-time initialization to inherit the host user's Claude Code
configuration (settings, plugins, credentials). This avoids manual re-authentication
and plugin installation on every container creation.

**Config schema** (`config.ClaudeCodeConfig` in `clawker.yaml`):
- `strategy`: `"copy"` (copy host config) or `"fresh"` (clean slate). Default: `"copy"`
- `use_host_auth`: Forward host credentials to container. Default: `true`

**Init flow** (orchestrated by `shared.CreateContainer()` in `cmd/container/shared/container.go`):

Progress streamed via events channel (`chan CreateContainerEvent`). Steps:
1. **workspace** — `workspace.SetupMounts()` + `workspace.EnsureConfigVolumes()`
2. **config** (skipped if volume cached) — `containerfs.PrepareClaudeConfig()` + `containerfs.PrepareCredentials()` → `docker.CopyToVolume()`
3. **environment** — `config.ResolveAgentEnv()` merges env_file/from_env/env → runtime env vars (warnings sent as `MessageWarning` events)
4. **container** — validate flags, `BuildConfigs()`, `docker.ContainerCreate()` + `InjectOnboardingFile()` (when `use_host_auth`) + `InjectPostInitScript()` (when `agent.post_init` configured)

**Key packages**: `internal/containerfs` (tar preparation, path rewriting),
`internal/workspace` (volume lifecycle), `internal/cmd/container/shared` (orchestration)

## 11. Testing Strategy

### 11.1 Integration Regression Tests

Tests run against real Docker—no mocking:

- Ensures actual Docker behavior
- Catches API compatibility issues
- Validates label filtering works correctly

**Before completing any code change:**

1. Run `go test ./...` - all unit tests must pass
2. Run `go test ./internal/cmd/...` - all integration tests must pass

### 11.2 Table-Driven Tests

Single test functions with case tables:

**engine test**

```go
func TestContainerOperations(t *testing.T) {
    test := []struct {
        name    string
        setup   func()
        action  func() error
        verify  func() error
    }{
        {"create and start", ...},
        {"stop running", ...},
        {"remove stopped", ...},
    }
    for _, tt := range test {
        t.Run(tt.name, func(t *testing.T) {
            // ...
        })
    }
}
```

**cli command test**

```go
func TestNewCmdStop(t *testing.T) {
    tests := []struct {
        name    string
        args    []string
        wantErr bool
    }{
        {name: "no args", args: []string{}, wantErr: true},
        {name: "with agent flag", args: []string{"--agent", "dev"}},
        {name: "with container name", args: []string{"clawker.myapp.dev"}},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            tio := iostreamstest.New()
            f := &cmdutil.Factory{IOStreams: tio.IOStreams}

            var gotOpts *StopOptions
            cmd := NewCmdStop(f, func(opts *StopOptions) error {
                gotOpts = opts
                return nil
            })

            cmd.SetArgs(tt.args)
            cmd.SetIn(&bytes.Buffer{})
            cmd.SetOut(&bytes.Buffer{})
            cmd.SetErr(&bytes.Buffer{})

            err := cmd.Execute()
            if tt.wantErr {
                require.Error(t, err)
                return
            }
            require.NoError(t, err)
            require.NotNil(t, gotOpts)
        })
    }
}
```

## 12. Dependencies

| Dependency | Purpose |
|------------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/spf13/viper` | Configuration management |
| `github.com/moby/moby` | Docker SDK |
| `github.com/rs/zerolog` | Structured logging |
| `github.com/charmbracelet/bubbletea` | Terminal UI framework (TUI package only) |
| `github.com/charmbracelet/bubbles` | TUI components — spinner, viewport, key (TUI package only) |
| `github.com/charmbracelet/lipgloss` | Terminal styling (iostreams package only) |
| `github.com/go-git/go-git/v6` | Git operations (git package only) |
| `golang.org/x/term` | Terminal capabilities (term package only) |
