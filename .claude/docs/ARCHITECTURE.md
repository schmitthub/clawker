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
│                  pkg/cmd/*                                   │
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

### pkg/cmd/* - CLI Commands

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

## Other Key Components

| Package | Purpose |
|---------|---------|
| `internal/workspace` | Bind vs Snapshot strategies for host-container file sharing |
| `internal/term` | PTY handling for interactive sessions |
| `internal/config` | Viper config loading and validation |
| `internal/credentials` | Environment variable construction with allow/deny lists |
| `internal/monitor` | Observability stack (Prometheus, Grafana, OTel) |
| `pkg/build` | Dockerfile generation, semver, npm registry client |
| `pkg/cmdutil` | Factory, error handling, output utilities |

## Design Principles

1. **All Docker SDK calls go through pkg/whail** - Never bypass this layer
2. **Labels are authoritative** - `com.clawker.managed=true` determines ownership
3. **Naming is secondary** - `clawker.*` prefix for readability, not filtering
4. **stdout for data, stderr for status** - Enables scripting/composability
5. **User-friendly errors** - All errors include "Next Steps" guidance
