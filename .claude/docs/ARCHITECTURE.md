# Claucker Architecture

> **LLM Memory Document**: Detailed abstractions and interfaces for the claucker codebase.

## WorkspaceStrategy Interface

Two implementations for host-container file sharing:

| Strategy | Purpose | Use Case |
|----------|---------|----------|
| `BindStrategy` | Live host mount | Development, real-time sync |
| `SnapshotStrategy` | Ephemeral volume copy | Safe experimentation, isolation |

Location: `internal/workspace/`

## DockerEngine

Wraps Docker SDK with user-friendly errors including "Next Steps" guidance.

Location: `internal/engine/client.go`

```go
type DockerEngine struct { ... }

func NewDockerEngine(ctx context.Context) (*DockerEngine, error)
func (e *DockerEngine) ListContainers(ctx context.Context, opts ListOptions) ([]Container, error)
```

## PTYHandler

Manages raw terminal mode and bidirectional streaming for interactive Claude sessions.

Location: `internal/term/pty.go`

**Key behaviors:**
- In raw mode, Ctrl+C does NOT generate SIGINT - it's passed as a byte to the container
- Stream methods return immediately when output closes (container exits)
- Does not wait for stdin goroutine (may be blocked on Read())

## DockerfileGenerator

Generates Dockerfiles from Go templates with `TemplateData` struct.

Location: `pkg/build/dockerfile.go`

```go
type TemplateData struct {
    Instructions *DockerInstructions  // Type-safe instructions
    Inject       *InjectConfig        // Raw injection at lifecycle points
    IsAlpine     bool                 // OS detection for package commands
}
```

**Template injection order:**
1. `after_from`
2. packages
3. `after_packages`
4. `root_run`
5. user setup
6. `after_user_setup`
7. COPY
8. `USER claude`
9. `after_user_switch`
10. `user_run`
11. Claude install
12. `after_claude_install`
13. `before_entrypoint`
14. ENTRYPOINT

## Semver Package

Pure Go semver implementation for version parsing, comparison, and matching.

Location: `pkg/build/semver/`

```go
type Version struct {
    Major, Minor, Patch int
    Prerelease, Build   string
    Original            string
}

func Parse(s string) (*Version, error)
func Compare(a, b *Version) int
func Sort(versions []*Version)
func SortStrings(versions []string) []string
func Match(versions []string, target string) (string, error)
```

**Key behaviors:**
- Supports partial versions (`2.1` matches highest `2.1.x`)
- Prereleases sort before releases (`2.1.0-beta < 2.1.0`)
- `Match()` finds best matching version for patterns like `latest`, `2.1`, or exact `2.1.2`

## NPM Registry Client

Fetches Claude Code versions from npm registry.

Location: `pkg/build/registry/`

```go
type NPMClient struct { ... }

func NewNPMClient() *NPMClient
func (c *NPMClient) FetchVersions(ctx context.Context, pkg string) ([]string, error)
func (c *NPMClient) FetchDistTags(ctx context.Context, pkg string) (DistTags, error)
```

**Key types:**
- `DistTags` - Map of tag names to versions (`latest`, `stable`, `next`)
- `VersionInfo` - Full version metadata with variants
- `VersionsFile` - Complete versions.json structure

## VersionsManager

Orchestrates version resolution by combining npm fetching with semver matching.

Location: `pkg/build/versions.go`

```go
type VersionsManager struct { ... }

func NewVersionsManager() *VersionsManager
func (m *VersionsManager) ResolveVersions(ctx context.Context, patterns []string, opts ResolveOptions) (*VersionsFile, error)
func LoadVersionsFile(path string) (*VersionsFile, error)
func SaveVersionsFile(path string, versions *VersionsFile) error
```

## ConfigValidator

Validates `claucker.yaml` with semantic checks beyond YAML parsing.

Location: `internal/config/validator.go`

**Validates:**
- Path existence and permissions for `instructions.copy`
- Port range validation for `instructions.expose`
- Duration format validation for `healthcheck` intervals

## Output Utilities

Centralized error handling and user messaging for consistent CLI output.

Location: `pkg/cmdutil/output.go`

```go
// Smart error handling - detects DockerError for rich formatting
cmdutil.HandleError(err)

// Print numbered "Next Steps" guidance
cmdutil.PrintNextSteps(
    "Run 'claucker init' to create a configuration",
    "Or change to a directory with claucker.yaml",
)

// Simple error/warning output to stderr
cmdutil.PrintError("Configuration validation failed")
cmdutil.PrintWarning("Container already exists")
```

**Key functions:**
- `HandleError(err)` - If `*engine.DockerError`, uses `FormatUserError()`; otherwise prints simple message
- `PrintNextSteps(steps...)` - Prints numbered list of actionable suggestions
- `PrintError(format, args...)` - Prints `Error: <message>` to stderr
- `PrintWarning(format, args...)` - Prints `Warning: <message>` to stderr

All output goes to stderr, keeping stdout clean for scripting.

## Monitor Package

Manages the observability stack using Docker Compose.

Location: `internal/monitor/`

**Components:**
- **Prometheus** - Metrics collection
- **Grafana** - Dashboard visualization
- **OpenTelemetry Collector** - Telemetry aggregation

**Embedded templates** in `internal/monitor/templates/`:
- `compose.yaml` - Docker Compose stack definition
- `prometheus.yaml` - Prometheus scrape config
- `otel-config.yaml` - OTel collector config
- `grafana-datasources.yaml` - Grafana data source config
- `grafana-dashboard.json` - Pre-built dashboard

## EnvBuilder

Manages environment variable construction with allow/deny lists.

Location: `internal/credentials/env.go`

```go
envBuilder := credentials.NewEnvBuilder()
envBuilder.Set("KEY", "value")
envBuilder.SetAll(cfg.Agent.Env)
envBuilder.LoadDotEnv(filepath.Join(workDir, ".env"))
envBuilder.SetFromHostAll(credentials.DefaultPassthrough())
env := envBuilder.Build()  // []string{"KEY=value", ...}
```

Also handles OTEL variable injection when monitoring is active via `credentials.OtelEnvVars()`.

## Port Parsing

Parses Docker-style port specifications for the `-p` flag.

Location: `internal/engine/ports.go`

```go
portBindings, exposedPorts, err := engine.ParsePortSpecs([]string{
    "8080:8080",              // host:container
    "127.0.0.1:3000:3000",    // ip:host:container
    "24280-24290:24280-24290", // port range
    "53:53/udp",              // UDP protocol
})
```

**Supported formats:**
- `containerPort` - random host port to container port
- `hostPort:containerPort` - specific host port mapping
- `hostIP:hostPort:containerPort` - bind to specific interface
- `startPort-endPort:startPort-endPort` - port range mapping
- Any format with `/tcp` or `/udp` suffix (default: tcp)

## Container Naming and Labels

Hierarchical naming for multi-container support.

Location: `internal/engine/names.go`, `internal/engine/labels.go`

**Naming conventions:**
- Container names: `claucker.project.agent` (e.g., `claucker.myapp.ralph`)
- Volume names: `claucker.project.agent-purpose` (e.g., `claucker.myapp.ralph-workspace`)

**Key functions:**
```go
ContainerName(project, agent string) string
VolumeName(project, agent, purpose string) string
ParseContainerName(name string) (project, agent string, err error)
GenerateRandomName() string  // Docker-style adjective-noun
```

**Docker labels** enable reliable filtering:

| Label | Purpose |
|-------|---------|
| `com.claucker.managed` | Marker for claucker resources |
| `com.claucker.project` | Project name |
| `com.claucker.agent` | Agent name |
| `com.claucker.version` | Claucker version |
| `com.claucker.image` | Source image tag |
| `com.claucker.workdir` | Host working directory |

**Helper functions:**
- `ContainerLabels(project, agent, version, image, workdir)` - creates container labels
- `VolumeLabels(project, agent, purpose)` - creates volume labels
- `ClauckerFilter()` - filter args for all claucker resources
- `ProjectFilter(project)` - filter args for specific project
