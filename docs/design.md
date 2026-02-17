---
title: "Design Philosophy"
description: "Security model, core concepts, and key design decisions"
---

# Design Philosophy

## The Padded Cell

Clawker creates a "padded cell" for AI coding agents. Standard Docker gives users full control — dangerous for autonomous AI agents. Clawker wraps Docker-like commands with isolation that protects everything outside the container from what happens inside.

### What We Protect

- **Host filesystem** — from container writes (bind mounts controlled)
- **Host network** — via firewall (outbound controlled, inbound open)
- **Other Docker resources** — via label-based isolation
- **The container itself** — is disposable; a new one can always be created

We do not inherit Docker's full threat model. If Docker allows a command, Clawker permits it — but only against Clawker-managed resources.

## Core Concepts

### Project

A project is defined by `clawker.yaml` and registered in the project registry (`~/.local/clawker/projects.yaml`). Every Clawker command requires project context, resolved via longest-prefix path matching against the registry.

**Configuration precedence** (highest to lowest):
1. CLI flags
2. Environment variables
3. Project config (`./clawker.yaml`)
4. User settings (`~/.local/clawker/settings.yaml`)

### Agent

An agent is a named container instance. One project can have multiple agents, each running in its own isolated container.

**Naming convention**: `clawker.<project>.<agent>` (e.g., `clawker.myapp.dev`)

### Resource Identification

| Mechanism | Purpose | Authority |
|-----------|---------|-----------|
| **Labels** | Filtering, ownership verification | Authoritative source of truth |
| **Naming** | Human readability | Secondary |
| **Network** | Container communication, isolation | Functional |

**Strict ownership**: Clawker refuses to operate on resources without `dev.clawker.managed=true`, even if they have the `clawker.` name prefix.

## Security Model

### Defaults

| Setting | Default | Rationale |
|---------|---------|-----------|
| Firewall | Enabled | Blocks outbound except allowlisted domains |
| Docker socket | Disabled | Container cannot control Docker |
| Git credentials | Forwarded | Agent access only — keys stay on host |

### Credential Handling

- API keys passed via environment variables
- Subscription users authenticate via the host proxy (OAuth callback interception)
- Git HTTPS credentials forwarded through the host proxy
- SSH keys forwarded via agent socket (never copied)

### Firewall

The firewall init script blocks all outbound traffic by default, then allowlists specific domains. IP range sources fetch CIDR blocks from cloud provider APIs for services like GitHub.

## Key Design Decisions

1. **All Docker SDK calls go through `pkg/whail`** — never bypass this isolation layer
2. **Labels are authoritative** — `dev.clawker.managed=true` determines ownership, not names
3. **stdout for data, stderr for status** — enables scripting and composability
4. **Factory DI pattern** — pure struct in `cmdutil`, constructor in `cmd/factory`, Options in commands
5. **Stateless CLI** — all state lives in Docker (containers, labels, volumes); no local state files
6. **`config.Config` is a gateway** — lazy accessor for Project, Settings, Resolution, Registry
7. **zerolog is file-only** — user-visible output uses `fmt.Fprintf` to IOStreams

## State Management

Clawker stores no local state. All state lives in Docker:
- Container state (running, stopped)
- Labels (project, agent, metadata)
- Volumes (workspace, config, history)

This means multiple Clawker instances can operate concurrently with no synchronization issues.

## Command Taxonomy

Commands mirror Docker's CLI structure:

| Pattern | Examples |
|---------|----------|
| `clawker <verb>` | `run`, `stop`, `build` |
| `clawker <noun> <verb>` | `container ls`, `volume rm`, `image build` |

Top-level shortcuts: `init`, `build`, `run`, `start`, `config check`, `monitor *`, `loop *`, `version`

Management commands: `container`, `volume`, `network`, `image`, `project`, `worktree`

## Multi-Agent Operations

```
Project ──< Agent >── Image
  1           *          *
```

- One project has many agents
- Many agents can share one image
- Race condition resolution: second process attaches to existing container (no error, no duplicate)

## Error Handling

Errors return typed values to `Main()` for centralized rendering:
- `fmt.Errorf(...)` — general errors
- `cmdutil.FlagError` — triggers usage display
- `cmdutil.SilentError` — already displayed, just exit non-zero
