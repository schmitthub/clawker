# Architecture — Clawker

> See `.claude/docs/ARCHITECTURE.md` for the full package DAG, Factory DI pattern, presentation layer model, firewall stack architecture, and control plane design.
> See `.claude/docs/DESIGN.md` for configuration system, security model, container lifecycle, and error handling taxonomy.
> See `.claude/docs/KEY-CONCEPTS.md` for the type/abstraction index.
> See `.serena/memories/` for active work-in-progress tracking (project-overview, outstanding-features, brainstorm_the-controlplane-and-clawkerd, bug-tracker).

## Design Patterns

### PAT-001: Factory DI (GitHub CLI Pattern)
- Pure struct with eager IO/TUI/version + lazy noun closures
- Commands extract closures into per-command Options structs
- Run functions never see Factory, only Options
- See `.claude/docs/ARCHITECTURE.md` § Factory Dependency Injection

### PAT-002: Layered Config (Replaces Viper)
- `storage.Store[T]` typed layered YAML engine
- Walk-up merge from CWD to project root
- Two independent schemas: Settings (host infra) + Project (project defaults)
- See `internal/config/CLAUDE.md`

### PAT-003: Label-Based Resource Isolation
- `dev.clawker.managed=true` is authoritative for ownership
- `pkg/whail` injects label filter on all list operations
- See `.claude/docs/DESIGN.md` § Resource Identification

### PAT-004: CP Owns the Firewall (Envoy + CoreDNS + eBPF)
- Domain-based egress rules, shared infra (1 stack per host, N agents)
- Global BPF route_map keyed by {domain_hash, dst_port}
- **CP is the single owner** — no host-side firewall daemon, no `internal/firewall/` package, no PID file
- CLI -> `f.AdminClient(ctx)` -> `AdminService` gRPC (13 RPCs, uniform `admin` scope, mTLS + OAuth2 JWT) -> `firewall.Handler` -> `Stack`/`ebpf.Manager`/`Store`/`ContainerResolver`
- CP self-bootstraps on first admin call; `AgentWatcher` self-shuts-down on drain-to-zero (INV-B2-007)
- `internal/hostproxy/` is the single carve-out allowed to read `egress-rules.yaml` directly (PRH-B2-004)
- See `internal/controlplane/CLAUDE.md`, `internal/controlplane/firewall/CLAUDE.md`, `internal/cmd/controlplane/CLAUDE.md`

## Conventions

- Config access: use `Config` interface accessors, never hardcode paths/filenames
- Logging: zerolog for file only, `fmt.Fprintf` to IOStreams for user output
- Errors: `fmt.Errorf("context: %w", err)`, custom types in `cmdutil/errors.go`
- Testing: table-driven + testify, mocks in `*/mocks/`, fakes in `*test/`
- Commands: `NewCmd(f, runF)` pattern with Options struct
- See `.claude/rules/code-style.md`, `.claude/rules/testing.md`
