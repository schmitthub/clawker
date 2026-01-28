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
3. Repository root config (`./clawker.yaml`)
4. User home config (`~/.config/clawker/clawker.yaml`)

Higher precedence wins silently (no warnings on override).

**Configuration Files**:

| Location | Purpose |
|----------|---------|
| `./clawker.yaml` | Project-specific settings |
| `~/.config/clawker/clawker.yaml` | Global project defaults |
| `~/.config/clawker/config.yaml` | CLI/user settings (separate schema) |

*Note: Detailed config schema TBD.*

### 2.2 Agent

An agent is a named container instance. Agents have a many-to-many relationship with projects and images:

- One project can have multiple agents
- One agent belongs to one project
- Multiple agents can share an image
- One agent uses one image

**Naming Convention**: `clawker.<project>.<agent>`

- Example: `clawker.myapp.ralph`, `clawker.backend.worker`

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
com.clawker.managed=true
com.clawker.project=<project-name>
com.clawker.agent=<agent-name>
```

**Strict Ownership**: Clawker refuses to operate on resources without `com.clawker.managed=true` label, even if they have the `clawker.` name prefix.

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

Operations are defined at compile-time via an interface. Only exposed methods are accessible:

```go
type Engine interface {
    // Exposed operations (label-filterable)
    ContainerList(ctx context.Context, opts ListOptions) ([]Container, error)
    ContainerCreate(ctx context.Context, config ContainerConfig) (string, error)
    ContainerStart(ctx context.Context, id string) error
    ContainerStop(ctx context.Context, id string, timeout *time.Duration) error
    ContainerRemove(ctx context.Context, id string, opts RemoveOptions) error
    ContainerLogs(ctx context.Context, id string, opts LogsOptions) (io.ReadCloser, error)

    VolumeCreate(ctx context.Context, opts VolumeCreateOptions) (Volume, error)
    VolumeList(ctx context.Context, opts VolumeListOptions) ([]Volume, error)
    VolumeRemove(ctx context.Context, id string, force bool) error

    NetworkCreate(ctx context.Context, name string, opts NetworkCreateOptions) (string, error)
    NetworkList(ctx context.Context, opts NetworkListOptions) ([]Network, error)
    NetworkRemove(ctx context.Context, id string) error

    ImageBuild(ctx context.Context, buildContext io.Reader, opts ImageBuildOptions) (BuildResponse, error)
    ImagePull(ctx context.Context, ref string, opts ImagePullOptions) (io.ReadCloser, error)
    ImageList(ctx context.Context, opts ImageListOptions) ([]Image, error)
    ImageRemove(ctx context.Context, id string, opts ImageRemoveOptions) ([]ImageDeleteResponse, error)

    // NOT exposed: system prune, network disconnect (non-managed), etc.
}
```

**Blocked by Omission**: Any Docker SDK method not in this interface is inaccessible. Operations that cannot apply label filters (e.g., `system prune` without filters) are simply not exposed.

**Docker API Compatibility**: Minimum supported version defined at compile-time. No feature detection or graceful degradation—fail fast on incompatible versions.

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
| `clawker <noun> <verb>` | Multi-agent operations | `swarm start`, `swarm stop` |

### 4.2 Primary Nouns

All Docker nouns are supported:

- `container` - Container management
- `volume` - Volume management
- `network` - Network management
- `image` - Image management
- `swarm` - Multi-agent operations (clawker-specific)

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

### 7.2 Firewall

Firewall configuration is **out of scope** for the core design. Users implement firewall via:

- Custom Dockerfile with iptables rules
- Entrypoint scripts
- Network policies

The `security.enable_firewall` config option triggers inclusion of a firewall init script in the generated Dockerfile.

### 7.3 Strict Label Ownership

Clawker **refuses** to operate on resources without proper labels:

```go
if !hasLabel(container, "com.clawker.managed", "true") {
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

### 8.2 Parallel Operations

Multiple containers can start in parallel:

```bash
clawker swarm start --agents=3  # Start 3 agents concurrently
```

### 8.3 Bulk Operations via `swarm`

The `swarm` noun handles multi-agent commands:

```bash
clawker swarm start          # Start all agents for project
clawker swarm stop           # Stop all agents for project
clawker swarm status         # Status of all agents
```

### 8.4 Race Condition Resolution

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

| Operation Type | Indicator |
|----------------|-----------|
| Indeterminate | Spinner |
| Determinate (image pull) | Progress bar |
| Streaming (logs) | Partial screen with progress indicators |

Verbose mode shows full streaming logs.

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
func TestNewCmdToken(t *testing.T) {
 tests := []struct {
  name       string
  input      string
  output     TokenOptions
  wantErr    bool
  wantErrMsg string
 }{
  {
   name:   "no flags",
   input:  "",
   output: TokenOptions{},
  },
  {
   name:   "with hostname",
   input:  "--hostname github.mycompany.com",
   output: TokenOptions{Hostname: "github.mycompany.com"},
  },
  {
   name:   "with user",
   input:  "--user test-user",
   output: TokenOptions{Username: "test-user"},
  },
  {
   name:   "with shorthand user",
   input:  "-u test-user",
   output: TokenOptions{Username: "test-user"},
  },
  {
   name:   "with shorthand hostname",
   input:  "-h github.mycompany.com",
   output: TokenOptions{Hostname: "github.mycompany.com"},
  },
  {
   name:   "with secure-storage",
   input:  "--secure-storage",
   output: TokenOptions{SecureStorage: true},
  },
 }

 for _, tt := range tests {
  t.Run(tt.name, func(t *testing.T) {
   ios, _, _, _ := iostreams.Test()
   f := &cmdutil.Factory{
    IOStreams: ios,
    Config: func() (gh.Config, error) {
     cfg := config.NewBlankConfig()
     return cfg, nil
    },
   }
   argv, err := shlex.Split(tt.input)
   require.NoError(t, err)

   var cmdOpts *TokenOptions
   cmd := NewCmdToken(f, func(opts *TokenOptions) error {
    cmdOpts = opts
    return nil
   })
   // TODO cobra hack-around
   cmd.Flags().BoolP("help", "x", false, "")

   cmd.SetArgs(argv)
   cmd.SetIn(&bytes.Buffer{})
   cmd.SetOut(&bytes.Buffer{})
   cmd.SetErr(&bytes.Buffer{})

   _, err = cmd.ExecuteC()
   if tt.wantErr {
    require.Error(t, err)
    require.EqualError(t, err, tt.wantErrMsg)
    return
   }

   require.NoError(t, err)
   require.Equal(t, tt.output.Hostname, cmdOpts.Hostname)
   require.Equal(t, tt.output.SecureStorage, cmdOpts.SecureStorage)
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
