# Architecture Migration Status

## Migration: Current → "Padded Cell" Design

**Status**: PLANNING PHASE (not started)
**Plan Location**: `~/.claude/plans/warm-humming-ocean.md`
**Design Document**: `.claude/docs/DESIGN.md`

## Design Decisions (User Confirmed)
- **Package name**: `pkg/whail` (whale jail - a Docker jail wrapper)
- **API style**: Docker SDK-like for familiarity
- **Swarm commands**: Deferred to future project (not in this migration)

## Key Architectural Changes

### From
```
cmd/clawker → pkg/cmd/* → internal/engine → Docker SDK
```

### To
```
cmd/clawker → internal/docker (NEW) → pkg/whail (NEW) → Docker SDK
```

## New Packages to Create

1. **`pkg/whail/`** - External Engine (Reusable "whale jail" library)
   - Label-based selector injection
   - Configurable label prefix
   - Docker SDK-like API for familiarity
   - Whitelist interface (only exposed methods accessible)
   - Standalone for use in other projects

2. **`internal/docker/`** - Clawker Docker middleware
   - Initializes `pkg/whail` with clawker's labels
   - High-level operations: `RunAgent()`, `StopAgent()`, `ListAgents()`
   - Uses Viper configuration
   - Handles naming conventions, volume management

**Note**: `pkg/cmd/swarm/` (multi-agent commands) deferred to future project

## Packages to Refactor

| Current | New Location |
|---------|--------------|
| `internal/engine/client.go` | Split → `pkg/whail/engine.go` + `internal/docker/client.go` |
| `internal/engine/labels.go` | Split → `pkg/whail/labels.go` + `internal/docker/labels.go` |
| `internal/engine/container.go` | → `internal/docker/container.go` |
| `internal/engine/volume.go` | → `internal/docker/volume.go` |
| `internal/engine/names.go` | → `internal/docker/names.go` |

## Packages Unchanged

- `internal/config/` - Config loading
- `internal/workspace/` - Bind/Snapshot strategies
- `internal/credentials/` - Env handling
- `internal/build/` - Image building
- `internal/monitor/` - Observability
- `internal/term/` - PTY handling
- `pkg/build/` - Dockerfile generation
- `pkg/logger/` - Logging

## Implementation Phases

## Design Philosophy

**`pkg/whail` is heavy-weight** - Contains:
- All container building core logic
- Configurable labels per resource type with defaults
- Full Docker abstraction

**`internal/docker` is thin** - Just:
- Configures Engine with clawker's label preferences
- Naming conventions
- Thin wrappers for high-level operations

## Implementation Phases

1. **Phase 1**: Create `pkg/whail` foundation (3-4 sessions) - HEAVY
2. **Phase 2**: Create `internal/docker` layer (1-2 sessions) - THIN
3. **Phase 3**: Update commands to use `internal/docker` (2-3 sessions)
4. **Phase 4**: Remove legacy code (1 session)
5. **Phase 5**: Documentation updates (1 session)

**Note**: `swarm` commands deferred to future project

## Current Phase

**Phase 1: FULLY COMPLETED** (2026-01-14)

Created `pkg/whail` foundation with all core files:
- `engine.go` - Core Engine with configurable LabelConfig
- `container.go` - Container operations with label injection
- `volume.go` - Volume operations with label injection
- `network.go` - Network operations with label injection
- `image.go` - Image operations with label injection
- `labels.go` - Label utilities (MergeLabels, LabelConfig, etc.)
- `errors.go` - Generic Docker errors

Test files:
- `engine_test.go` - Comprehensive integration tests (55 tests)
- `labels_test.go` - Unit tests for label utilities
- `errors_test.go` - Unit tests for error types

All tests passing: `go test ./pkg/whail/...`

**Next Phase**: Phase 2 - Create `internal/docker` layer

## Key Design Patterns

### Selector Injection (pkg/whail)
```go
// All list operations automatically inject label filters
func (e *Engine) ContainerList(ctx context.Context, opts ListOptions) ([]Container, error) {
    opts.Filters = e.injectManagedFilter(opts.Filters)
    return e.cli.ContainerList(ctx, opts)
}
```

### High-Level Client API (internal/docker)
```go
type Client struct {
    engine  *whail.Engine
    config  *config.Config
    project string
}

func (c *Client) RunAgent(ctx context.Context, agent string, opts RunOptions) error
func (c *Client) StopAgent(ctx context.Context, agent string, opts StopOptions) error
```

## How to Resume

**Say**: "Continue working on the architecture migration"

**Then**:
1. Read `architecture_migration_tasks` memory for detailed task list
2. Check `Current Phase` and `Next Task` in that memory
3. Resume work from where last session stopped
4. **Before context runs out**: Update the task list and Session Log

**Task List Memory**: `architecture_migration_tasks`
**Plan File**: `~/.claude/plans/warm-humming-ocean.md`
