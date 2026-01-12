# Claucker - Claude Code Development Container Orchestration

<critical_instructions>

## IMPORTANT: Required tool chain when working on this codebase

### I (MUST USE) use the following tooling workflow

1. **Serena** - Always use serena for all code exploration, planning, context management, symbol search, and semantic editing. My workflow after every user request when using serena must be:
   - **Critical**: Always use `initial_instructions` at the start of any session, this provides instructions on how to use the Serena toolbox.
   - Always call `check_onboarding_performed` before planning, if onboarding hasn't been performed call `onboarding`, to identify the project structure, software architecture, and essential tasks, e.g. for testing or building before doing anything else.
   - Always call `list_memories` and `read_memory` for context
   - Always call `list_dir`, `find_file` to understand the project structure
   - Always call `search_for_pattern`, `find_symbol` ,`find_referencing_symbols`, `get_symbols_overview` to search and navigating source code instead of using `grep`
   - Always call `think_about_collected_information` after any planning for pondering the completeness of collected information.
   - Always call `think_about_task_adherence` before making changes
   - Always call `replace_symbol_body`, `insert_after_symbol`, `insert_before_symbol` , `rename_symbol` for editing code
   - Always call `think_about_whether_you_are_done` for determining whether the task is truly completed.
   - Always call `write_memory`, `edit_memory`, `delete_memory` to keep project memories relevant and up to date

2. **Context7** - Always use Context7 MCP when I need library/API documentation, code generation, setup or configuration steps without me having to explicitly ask. Project dependencies are listed in @go.mod
   - Always call `resolve-library-id` using parameters `libraryName` and `query` first to get the library ID
   - Then call `query-docs` using parameters `libraryId` and `query` with topic for relevant docs
   - Essential for project dependencies in @go.mod:
     - Docker SDK
     - Go stdlib
     - github.com/docker/docker
     - github.com/rs/zerolog
     - github.com/spf13/cobra
     - github.com/spf13/viper
     - golang.org/x/term
     - gopkg.in/yaml.v3

### I (SHOULD USE) use the following tooling workflow

1. **ast-grep** - Always fallback on `ast-grep` when not using `serena` for structural code search and refactoring instead of `grep`
   - Always `Skill(ast-grep)` instead of `grep` when searching for code structural search, lint, rewriting
   - Better than regex and `grep` for finding code structures
   - Use for planning and refactoring tasks

</critical_instructions>

## Project Overview

Claucker is a Go CLI tool that wraps the Claude Code agent in secure, reproducible Docker containers.
Core philosophy: "Safe Autonomy" - host system is read-only by default.

## Repository Structure

```
/workspace/
├── cmd/                           # CLI entry points
│   ├── claucker/                  # Main CLI binary
│   └── claucker-generate/         # Standalone generate binary
├── internal/                      # Private packages
│   ├── build/                     # Image building orchestration
│   ├── claucker/                  # Main entry point (bridges cmd/ → pkg/cmd/)
│   ├── config/                    # Viper configuration loading + validation
│   │   ├── schema.go             # Config types (DockerInstructions, etc.)
│   │   ├── loader.go             # YAML loading
│   │   ├── validator.go          # Semantic validation
│   │   ├── defaults.go           # Default values
│   │   └── home.go               # Home directory helpers
│   ├── credentials/               # Environment & secrets handling
│   │   ├── env.go                # EnvBuilder for variable management
│   │   ├── dotenv.go             # .env file parsing
│   │   └── otel.go               # OpenTelemetry config injection
│   ├── engine/                    # Docker SDK abstractions
│   │   ├── client.go             # Engine wrapper, container listing
│   │   ├── container.go          # ContainerManager, ContainerConfig
│   │   ├── volume.go             # VolumeManager
│   │   ├── image.go              # Image management
│   │   ├── names.go              # Container/volume naming, random names
│   │   ├── labels.go             # Label constants, filtering helpers
│   │   ├── ports.go              # Port specification parsing for -p flag
│   │   └── errors.go             # DockerError with user-friendly messages
│   ├── monitor/                   # Observability stack (Prometheus, Grafana, OTel)
│   │   ├── templates.go          # Embedded template loader
│   │   └── templates/            # compose.yaml, prometheus.yaml, etc.
│   ├── term/                      # PTY/terminal handling
│   │   ├── pty.go                # PTY session management
│   │   ├── raw.go                # Raw terminal mode
│   │   └── signal.go             # Signal handling (SIGWINCH)
│   └── workspace/                 # Bind vs Snapshot strategies
│       ├── strategy.go           # Strategy interface
│       ├── bind.go               # Live host mount
│       └── snapshot.go           # Ephemeral volume copy
├── pkg/                           # Public packages
│   ├── build/                     # Version generation and Dockerfile templates
│   │   ├── dockerfile.go         # DockerfileManager, ProjectGenerator
│   │   ├── versions.go           # VersionsManager (npm → semver)
│   │   ├── config.go             # Variant configuration (base images)
│   │   ├── semver/               # Pure Go semver implementation
│   │   ├── registry/             # NPM registry client
│   │   └── templates/            # Embedded Dockerfile, entrypoint, firewall
│   ├── cmd/                       # Cobra commands
│   │   ├── root/                 # Root command (integrates all subcommands)
│   │   ├── start/                # claucker start
│   │   ├── run/                  # claucker run (ephemeral)
│   │   ├── stop/                 # claucker stop
│   │   ├── restart/              # claucker restart
│   │   ├── build/                # claucker build
│   │   ├── sh/                   # claucker sh
│   │   ├── logs/                 # claucker logs
│   │   ├── ls/                   # claucker ls
│   │   ├── rm/                   # claucker rm
│   │   ├── prune/                # claucker prune
│   │   ├── init/                 # claucker init
│   │   ├── config/               # claucker config check
│   │   ├── generate/             # claucker generate
│   │   └── monitor/              # claucker monitor (init/up/down/status)
│   ├── cmdutil/                   # Command utilities
│   │   ├── factory.go            # Factory for dependency injection
│   │   └── output.go             # Error handling and user messaging
│   └── logger/                    # Zerolog setup
├── templates/                     # Scaffolding templates for init command
│   ├── claucker.yaml.tmpl        # Configuration template
│   └── clauckerignore.tmpl       # Ignore file template
├── artifacts/                     # Generated/build output (do not edit)
│   └── dockerfiles/              # Generated Dockerfiles
└── .devcontainer/                 # Development container config
```

## Build Commands

```bash
# Build the CLI with go
go build -o bin/claucker ./cmd/claucker
# Build the CLI with make
make build-cli

# Build standalone generate binary
make cli-generate

# Run tests
go test ./...

# Run with debug logging
./bin/claucker --debug start

# Generate versions.json from npm
./bin/claucker generate latest 2.1

# Generate Docker images (existing infrastructure)
make build VERSION=2.1.2 VARIANT=trixie
```

## Key Abstractions

### WorkspaceStrategy Interface

Two implementations: BindStrategy (live host mount) and SnapshotStrategy (ephemeral volume copy).

### DockerEngine

Wraps Docker SDK with user-friendly errors including "Next Steps" guidance.

### PTYHandler

Manages raw terminal mode and bidirectional streaming for interactive Claude sessions.

- In raw mode, Ctrl+C does NOT generate SIGINT - it's passed as a byte to the container
- Stream methods return immediately when output closes (container exits)
- Does not wait for stdin goroutine (may be blocked on Read())

### DockerfileGenerator

Generates Dockerfiles from Go templates with `TemplateData` struct containing:

- `Instructions` (`*DockerInstructions`) - Type-safe Dockerfile instructions
- `Inject` (`*InjectConfig`) - Raw instruction injection at 6 lifecycle points
- `IsAlpine` - OS detection for conditional package commands

Template injection order: `after_from` → packages → `after_packages` → `root_run` → user setup → `after_user_setup` → COPY → `USER claude` → `after_user_switch` → `user_run` → Claude install → `after_claude_install` → `before_entrypoint` → ENTRYPOINT

### Semver Package (pkg/build/semver)

Pure Go semver implementation (ported from semver.jq) for version parsing, comparison, and matching:

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

Key behaviors:

- Supports partial versions (`2.1` matches highest `2.1.x`)
- Prereleases sort before releases (`2.1.0-beta < 2.1.0`)
- `Match()` finds best matching version for patterns like `latest`, `2.1`, or exact `2.1.2`

### NPM Registry Client (pkg/build/registry)

Fetches Claude Code versions from npm registry:

```go
type NPMClient struct { ... }

func NewNPMClient() *NPMClient
func (c *NPMClient) FetchVersions(ctx context.Context, pkg string) ([]string, error)
func (c *NPMClient) FetchDistTags(ctx context.Context, pkg string) (DistTags, error)
```

Key types:

- `DistTags` - Map of tag names to versions (`latest`, `stable`, `next`)
- `VersionInfo` - Full version metadata with variants
- `VersionsFile` - Complete versions.json structure

### VersionsManager (pkg/build/versions.go)

Orchestrates version resolution by combining npm fetching with semver matching:

```go
type VersionsManager struct { ... }

func NewVersionsManager() *VersionsManager
func (m *VersionsManager) ResolveVersions(ctx context.Context, patterns []string, opts ResolveOptions) (*VersionsFile, error)
func LoadVersionsFile(path string) (*VersionsFile, error)
func SaveVersionsFile(path string, versions *VersionsFile) error
```

### ConfigValidator

Validates `claucker.yaml` with semantic checks beyond YAML parsing:

- Path existence and permissions for `instructions.copy`
- Port range validation for `instructions.expose`
- Duration format validation for `healthcheck` intervals

### Output Utilities (pkg/cmdutil/output.go)

Centralized error handling and user messaging for consistent CLI output:

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

Key functions:

- `HandleError(err)` - If `*engine.DockerError`, uses `FormatUserError()`; otherwise prints simple message
- `PrintNextSteps(steps...)` - Prints numbered list of actionable suggestions
- `PrintError(format, args...)` - Prints `Error: <message>` to stderr
- `PrintWarning(format, args...)` - Prints `Warning: <message>` to stderr

All output goes to stderr, keeping stdout clean for scripting.

### Monitor Package (internal/monitor)

Manages the observability stack using Docker Compose:

- **Prometheus** - Metrics collection
- **Grafana** - Dashboard visualization
- **OpenTelemetry Collector** - Telemetry aggregation

Embedded templates in `internal/monitor/templates/`:
- `compose.yaml` - Docker Compose stack definition
- `prometheus.yaml` - Prometheus scrape config
- `otel-config.yaml` - OTel collector config
- `grafana-datasources.yaml` - Grafana data source config
- `grafana-dashboard.json` - Pre-built dashboard

Commands: `claucker monitor init|up|down|status`

### EnvBuilder (internal/credentials/env.go)

Manages environment variable construction with allow/deny lists:

```go
envBuilder := credentials.NewEnvBuilder()
envBuilder.Set("KEY", "value")
envBuilder.SetAll(cfg.Agent.Env)
envBuilder.LoadDotEnv(filepath.Join(workDir, ".env"))
envBuilder.SetFromHostAll(credentials.DefaultPassthrough())
env := envBuilder.Build()  // []string{"KEY=value", ...}
```

Also handles OTEL variable injection when monitoring is active via `credentials.OtelEnvVars()`.

### Port Parsing (internal/engine/ports.go)

Parses Docker-style port specifications for the `-p` flag:

```go
// Parse port specs into Docker SDK types
portBindings, exposedPorts, err := engine.ParsePortSpecs([]string{
    "8080:8080",              // host:container
    "127.0.0.1:3000:3000",    // ip:host:container
    "24280-24290:24280-24290", // port range
    "53:53/udp",              // UDP protocol
})
```

Supported formats:
- `containerPort` - random host port to container port
- `hostPort:containerPort` - specific host port mapping
- `hostIP:hostPort:containerPort` - bind to specific interface
- `startPort-endPort:startPort-endPort` - port range mapping
- Any format with `/tcp` or `/udp` suffix (default: tcp)

### Container Naming and Labels

Claucker uses hierarchical naming for multi-container support:

- **Container names**: `claucker.project.agent` (e.g., `claucker.myapp.ralph`)
- **Volume names**: `claucker.project.agent-purpose` (e.g., `claucker.myapp.ralph-workspace`)

Key functions in `internal/engine/names.go`:

- `ContainerName(project, agent)` - generates container name
- `VolumeName(project, agent, purpose)` - generates volume name
- `ParseContainerName(name)` - extracts project/agent from name
- `GenerateRandomName()` - Docker-style adjective-noun generator

Docker labels (`internal/engine/labels.go`) enable reliable filtering:

| Label | Purpose |
|-------|---------|
| `com.claucker.managed` | Marker for claucker resources |
| `com.claucker.project` | Project name |
| `com.claucker.agent` | Agent name |
| `com.claucker.version` | Claucker version |
| `com.claucker.image` | Source image tag |
| `com.claucker.workdir` | Host working directory |

Helper functions:

- `ContainerLabels(project, agent, version, image, workdir)` - creates container labels
- `VolumeLabels(project, agent, purpose)` - creates volume labels
- `ClauckerFilter()` - filter args for all claucker resources
- `ProjectFilter(project)` - filter args for specific project

## Code Style

- Use `zerolog` for all logging (never fmt.Print for debug)
- Use `cmdutil` output functions for user-facing messages (never raw fmt.Print to stdout)
- Errors must include actionable "Next Steps" using `cmdutil.PrintNextSteps()`
- All errors and warnings go to stderr via `cmdutil.PrintError()` / `cmdutil.PrintWarning()`
- Use `cmdutil.HandleError(err)` for Docker errors to get rich formatting
- Follow standard Go project layout (cmd/, internal/, pkg/)
- Use interfaces for testability (especially Docker client)
- See `.claude/docs/cli-guidelines.md` for comprehensive CLI design principles

### Cobra CLI Best Practices

All commands follow these patterns:

1. **Use `PersistentPreRunE` not `PersistentPreRun`** - Always return errors properly; never use `logger.Fatal()` or `os.Exit()` in Cobra hooks as they bypass error handling and deferred functions.

2. **Always include Example field** - Every command should have usage examples:

   ```go
   cmd := &cobra.Command{
       Use:   "start",
       Short: "Start Claude containers",
       Example: `  # Start Claude interactively
     claucker start

     # Start with a named agent
     claucker start --agent ralph`,
       RunE: func(cmd *cobra.Command, args []string) error { ... },
   }
   ```

3. **Route status messages to stderr** - Keep stdout clean for data/scripting:

   ```go
   // Status messages → stderr
   fmt.Fprintln(os.Stderr, "Starting container...")
   fmt.Fprintf(os.Stderr, "Container %s started\n", name)

   // Data output → stdout (e.g., ls command table)
   fmt.Println(tableOutput)
   ```

4. **Use flag validation helpers** - Cobra provides built-in validation:

   ```go
   cmd.MarkFlagsOneRequired("name", "project")  // At least one required
   cmd.MarkFlagsMutuallyExclusive("a", "b")     // Can't use both
   cmd.MarkFlagRequired("config")               // Always required
   ```

5. **Consistent error handling** - Use `cmdutil.HandleError(err)` for Docker errors to get rich formatting with "Next Steps" guidance.

## Common Tasks

### Adding a new CLI command

1. Create `pkg/cmd/<cmdname>/<cmdname>.go`
2. Define options struct and `NewCmd<Name>(f *cmdutil.Factory)` function
3. Use `cmdutil` output functions for user messaging:

   ```go
   // Configuration not found
   if config.IsConfigNotFound(err) {
       cmdutil.PrintError("No claucker.yaml found in current directory")
       cmdutil.PrintNextSteps(
           "Run 'claucker init' to create a configuration",
           "Or change to a directory with claucker.yaml",
       )
       return err
   }

   // Docker errors (rich formatting)
   if err != nil {
       cmdutil.HandleError(err)
       return err
   }
   ```

4. Register in `pkg/cmd/root/root.go`

### Modifying Dockerfile generation

1. Edit template in `pkg/build/templates/Dockerfile.tmpl`
2. Update `pkg/build/dockerfile.go` (DockerfileContext struct for base images, ProjectGenerator.buildContext for project builds)
3. If adding new config fields, update `internal/config/schema.go`
4. Add validation in `internal/config/validator.go`

### Adding new build instructions

1. Add type to `internal/config/schema.go` (e.g., `NewInstruction` struct)
2. Add field to `DockerfileInstructions` in `pkg/build/dockerfile.go`
3. Add template logic in `pkg/build/templates/Dockerfile.tmpl` at appropriate injection point
4. Add validation in `internal/config/validator.go`
5. Add tests in `generator_test.go` and `validator_test.go`

## CLI Commands

| Command | Description |
|---------|-------------|
| `claucker init` | Scaffold `claucker.yaml` and `.clauckerignore` |
| `claucker build [--no-cache]` | Build container image; `--no-cache` for fresh build |
| `claucker start [--agent] [-p port] [-- <claude-args>]` | Build image (if needed), create/reuse container, attach TTY; `--agent` names the container; `-p` publishes ports |
| `claucker run [--agent] [-p port] [-- <command>]` | Run ephemeral container (removed on exit); `--agent` names the container; `-p` publishes ports |
| `claucker stop [--agent] [--clean]` | Stop containers; `--agent` for specific, `--clean` destroys volumes |
| `claucker restart [--agent]` | Restart containers to pick up env changes |
| `claucker sh [--agent]` | Open raw bash shell in running container |
| `claucker logs [--agent] [-f]` | Stream container logs |
| `claucker ls [-a] [-p project]` | List claucker containers; `-a` includes stopped, `-p` filters by project |
| `claucker rm [-n name] [-p project]` | Remove containers and volumes; `-n` for specific, `-p` for all in project |
| `claucker prune [-a] [-f]` | Remove unused resources; `-a` removes ALL including volumes |
| `claucker monitor <cmd>` | Manage observability stack (init, up, down, status) |
| `claucker config check` | Validate `claucker.yaml` |
| `claucker generate [versions...]` | Generate versions.json from npm; `--skip-fetch` uses existing file |

## Update README.md

**CRITICAL: Always keep README.md synchronized with code changes.**

After making any of the following changes, you MUST update [README.md](README.md):

### When to Update README

1. **New CLI commands or flags** - Update CLI Commands table and add usage examples
2. **Configuration changes** - Update the `claucker.yaml` example and field descriptions
3. **New features** - Add to appropriate section (Quick Start, Workspace Modes, Security, etc.)
4. **Authentication changes** - Update Authentication section with new env vars or methods
5. **Behavior changes** - Update affected sections to reflect new behavior
6. **Security defaults** - Update Security section if defaults change

### README Writing Guidelines

- **User-first language** - Write for new users, not developers
- **Complete examples** - Show full commands with common flags
- **Concise descriptions** - One sentence per feature when possible
- **Practical use cases** - Explain WHEN to use a feature, not just HOW
- **Tables for reference** - Use tables for commands, flags, and env vars
- **No implementation details** - Avoid internals like package names or function calls

### README Structure

The README follows this order:

1. Quick Start - Get users running in 5 minutes
2. Authentication - How to pass API keys
3. CLI Commands - Reference table + detailed usage
4. Configuration - Full `claucker.yaml` spec with comments
5. Workspace Modes - bind vs snapshot explained
6. Security - Defaults and opt-in dangerous features
7. Ignore Patterns - `.clauckerignore` behavior
8. Development - Build instructions for contributors

### Example Update Pattern

```
Code change: Add --timeout flag to `up` command
README update:
  1. Add to CLI Commands table
  2. Add to "claucker start" flags section:
     --timeout=30s  Container startup timeout (default: 30s)
```

**Before completing any PR or task, verify README.md reflects all user-visible changes.**

## Update CLAUDE.md

**CRITICAL: Keep CLAUDE.md current with architectural and implementation changes.**

This file is the technical blueprint for AI agents and developers. Update it whenever implementation details change.

### When to Update CLAUDE.md

1. **New packages or modules** - Update Repository Structure with purpose
2. **Architectural changes** - Update Key Abstractions section
3. **New abstractions or interfaces** - Add to Key Abstractions with explanation
4. **Build/test commands** - Update Build Commands section
5. **Important behaviors** - Add to Important Gotchas if non-obvious
6. **Design decisions** - Document reasoning in Design Decisions
7. **Directory structure changes** - Update Repository Structure tree
8. **Common task patterns** - Add to Common Tasks section

### CLAUDE.md Writing Guidelines

- **Developer-focused** - Assume reader knows Go and Docker
- **Implementation details** - Include package names, interfaces, key types
- **Architectural reasoning** - Explain WHY, not just WHAT
- **Code patterns** - Show idioms and conventions used in the codebase
- **Gotchas and pitfalls** - Document non-obvious behaviors that cause bugs
- **Keep structure updated** - Repository Structure must match actual layout

### CLAUDE.md Structure

Maintain this order:

1. Required Tools - MCP servers and usage patterns
2. Project Overview - One-line philosophy
3. Repository Structure - ASCII tree with annotations
4. Build Commands - Developer workflow commands
5. Key Abstractions - Core interfaces and their purpose
6. Code Style - Conventions and idioms
7. Common Tasks - Step-by-step patterns for typical changes
8. CLI Commands - Reference (developer view with internals)
9. Update README.md - Keep user docs in sync
10. Update CLAUDE.md - Keep dev docs in sync
11. Configuration - Schema and structure
12. Design Decisions - Architectural choices and rationale
13. Important Gotchas - Non-obvious behaviors

### Example Update Pattern

```
Code change: Add NetworkPolicy type in internal/engine/
CLAUDE.md update:
  1. Add to Repository Structure:
     internal/engine/
       ├── client.go
       ├── container.go
       ├── network.go      # NEW
  2. Add to Key Abstractions:
     ### NetworkPolicy
     Manages iptables rules for firewall. Uses --wait flag to avoid conflicts.
  3. Add to Important Gotchas if needed:
     - iptables requires --wait in concurrent environments
```

**After implementing features, ensure CLAUDE.md guides future AI agents and developers correctly.**

## Configuration (claucker.yaml)

```yaml
version: "1"
project: "my-app"

build:
  image: "buildpack-deps:bookworm-scm"
  packages: ["git", "ripgrep", "make"]

  # Type-safe Dockerfile instructions (validated)
  instructions:
    env: { NODE_ENV: "production" }
    labels: { maintainer: "dev@example.com" }
    copy:
      - { src: "./config.json", dest: "/etc/app/", chown: "claude:claude" }
    expose:
      - { port: 3000 }
    args:
      - { name: "VERSION", default: "1.0" }
    volumes: ["/data"]
    workdir: "/app"
    healthcheck:
      cmd: ["curl", "-f", "http://localhost:3000/health"]
      interval: "30s"
    shell: ["/bin/bash", "-c"]
    root_run:  # As root, before user switch
      - { cmd: "mkdir -p /opt/app" }
      - { alpine: "apk add sqlite", debian: "apt-get install -y sqlite3" }
    user_run:  # As claude user
      - { cmd: "npm install -g typescript" }

  # Raw Dockerfile injection points (unvalidated strings)
  inject:
    after_from: []
    after_packages: ["RUN pip install poetry"]
    after_user_setup: []
    after_user_switch: []
    after_claude_install: []
    before_entrypoint: []

agent:
  includes: ["./docs/architecture.md"]
  env:
    NODE_ENV: "development"

workspace:
  remote_path: "/workspace"
  default_mode: "snapshot"

security:
  enable_firewall: true
  docker_socket: false
```

### Key Config Types (internal/config/schema.go)

| Type | Purpose |
|------|---------|
| `DockerInstructions` | Type-safe Dockerfile instructions with validation |
| `InjectConfig` | Raw instruction injection at 6 lifecycle points |
| `RunInstruction` | OS-aware commands (`cmd`, `alpine`, `debian` variants) |
| `CopyInstruction` | COPY with optional `chown`/`chmod` |
| `HealthcheckConfig` | HEALTHCHECK with interval/timeout/retries |

## Design Decisions

1. **Firewall enabled by default** - Network isolation for security
2. **Docker socket disabled by default** - Opt-in for Docker-in-Docker
3. **Config volume preserved by default** - Use `--clean` to remove all
4. **Idempotent `up` command** - Attaches to existing container if running
5. **Type-safe instructions preferred over raw inject** - `build.instructions` is validated and OS-aware; `build.inject` is escape hatch for advanced users
6. **OS detection from base image** - `RunInstruction` supports `alpine`/`debian` variants; generator detects OS from image name
7. **Hierarchical container naming** - Format `claucker.project.agent` with "/" separators; enables multiple containers per project
8. **Docker labels for resource identification** - Labels (`com.claucker.*`) provide reliable filtering; container names can be parsed but labels are authoritative
9. **Random agent names by default** - If `--agent` not specified, generates Docker-style adjective-noun name (e.g., "clever-fox")
10. **Backward incompatibility with old naming** - Old `claucker-project` format containers are ignored; users should remove manually
11. **Pure Go version generation** - `pkg/build/` replaces shell scripts (semver.jq, versions.sh, apply-templates.sh); provides better testability, cross-platform support, and integration with CLI
12. **Stdout for data, stderr for status** - All status messages, progress indicators, and user feedback go to stderr; only structured data output (like `ls` table) goes to stdout for scripting compatibility
13. **Example field in all commands** - Every Cobra command includes an `Example` field with formatted usage examples shown in `--help` output

## Important Gotchas

- `os.Exit()` does NOT run deferred functions - always restore terminal state explicitly before calling os.Exit
- In raw terminal mode, signals (SIGINT from Ctrl+C) are not generated - input goes directly to the container
- When streaming to containers, don't wait for stdin goroutine on exit - it may be blocked on Read()
- Docker hijacked connections require proper cleanup of both read and write sides
- Never use `logger.Fatal()` in Cobra hooks (`PersistentPreRun`, etc.) - it bypasses Cobra's error handling; always use `PersistentPreRunE` and return errors
- Cobra's `MarkFlagsOneRequired()` must be called after flags are defined, not in the command declaration
