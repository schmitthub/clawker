# Agent Context — Clawker

> Last updated: 2026-04-14

## What This Project Does

CLI tool for managing Docker-based development containers with security-focused egress firewall. Think "docker run" with opinionated naming, config, workspace management, and network security. Currently in alpha. The **control plane** (containerized gRPC admin service) now owns firewall, eBPF, and Envoy/CoreDNS lifecycle — CLI commands talk to it over mTLS + OAuth2 JWT via `f.AdminClient(ctx)`.

## Key Components

See `.claude/docs/KEY-CONCEPTS.md` for the full type/abstraction index. Critical packages:

| Component | Location | Purpose |
|-----------|----------|---------|
| CLI entry | `cmd/clawker/` | Main binary, Cobra root |
| CP daemon binary | `cmd/clawker-cp/` | Containerized CP daemon entry (PID 1 in `clawker-controlplane`) |
| Custom CoreDNS | `cmd/coredns-clawker/` | CoreDNS build embedding the `dnsbpf` plugin |
| Control plane core | `internal/controlplane/` | CP lifecycle, Ory auth (Hydra/Kratos/Oathkeeper), AdminService composition, `AgentWatcher` drain-to-zero, host-side `EnsureRunning`/`Stop` |
| CP firewall domain | `internal/controlplane/firewall/` | `Handler` (13 RPCs), `Stack` (Envoy+CoreDNS containers), envoy/coredns config gen, MITM certs, rules store, cgroup helpers |
| eBPF subsystem | `internal/controlplane/firewall/ebpf/` | `Manager`, cgroup programs, pinned maps; break-glass `ebpf-manager` CLI under `cmd/` |
| Admin client | `internal/controlplane/adminclient/` | CLI-side mTLS + JWT dial helper for `AdminService` |
| Auth | `internal/auth/` | CLI-side key material (CA, signing key, server cert) — primitives-only leaf |
| Auth CLI | `internal/cmd/auth/` | `clawker auth rotate` |
| CP CLI | `internal/cmd/controlplane/` | Break-glass `clawker controlplane up/down/status` |
| Firewall CLI | `internal/cmd/firewall/` | `status/list/add/remove/reload/up/down/enable/disable/bypass/rotate-ca` — all routed through `f.AdminClient` gRPC |
| Config | `internal/config/` | Layered YAML config engine |
| Docker | `internal/docker/` | Clawker Docker middleware (labels/naming) |
| Whail | `pkg/whail/` | Reusable Docker engine with label isolation |

## Design Patterns (load-bearing)

- **PAT-001 Factory DI** — `cmdutil.Factory` is a pure struct with closure fields. Commands extract closures into per-command `Options`. Key nouns introduced by B2: `f.AdminClient(ctx)` (gRPC client, lazy + cached, auto-bootstraps CP on first use) and `f.ControlPlane()` (CP container lifecycle manager). `f.Firewall` is **deleted** — no direct Go calls into firewall code from CLI.
- **PAT-004 Firewall ownership** — CP is the **single owner** of firewall state, eBPF lifetime, and Envoy/CoreDNS containers. There is no host-side firewall daemon, no `internal/firewall/` package, no PID file. CLI → AdminService (13 RPCs, uniform `admin` scope) → `firewall.Handler` → `Stack`/`Manager`/`Store`/`Resolver`.
- **CP drain-to-zero (INV-B2-007)** — `AgentWatcher` polls Docker for `purpose=agent` containers. After drain-to-zero + grace, the CP self-shuts-down with strict ordering: cancel bypass timers → `GracefulStop` gRPC → `Stack.Stop` → `ebpf.Manager.FlushAll`. Exit code 0 does not retrigger the `on-failure` restart policy.

## Common Pitfalls

- Don't add a new reader of `egress-rules.yaml` outside the CP. `internal/hostproxy/` is the **only** carve-out (documented in `internal/hostproxy/CLAUDE.md`). PRH-B2-004 guards this.
- Don't add `cgroup_path` to admin protobuf messages or compute cgroup paths CLI-side. Path resolution is hidden behind `firewall.ContainerResolver`; the handler resolves via Docker on every RPC (INV-B2-016 drift guard).
- Don't pass `CgroupPath` on `FirewallEnable/Disable/Bypass` requests — those RPCs carry only `container_id`.
- `CP down` does not stop Envoy/CoreDNS (they live on `clawker-net`). Use `clawker firewall down` to route `FirewallRemove` through the CP first, then `clawker controlplane down`.
- `HandlerDeps.Stack == nil` silently turns stack-up/down RPCs into no-ops (intentional for unit tests). Production wiring in `cmd/clawker-cp/main.go` must always wire a real `*Stack`.
- Domain hash contract is shared across three packages: `firewall.normalizeDomain` → `ebpf.DomainHash` → `internal/dnsbpf` writes into the pinned `dns_cache`/`route_map`. Changing either the normalization or the hash requires all three sites + `route_map` wipe.

## Quick Reference

| Need to... | Do this |
|------------|---------|
| Run unit tests | `make test` |
| Run E2E | `go test ./test/e2e/... -v -timeout 10m` |
| Build clawker CLI | `make clawker-build` |
| Build CP + eBPF + CoreDNS binaries | `make cp-binary` / `make ebpf-binary` / `make coredns-binary` |
| Regen protos | `make proto` |
| Regen mocks | `cd internal/<pkg> && go generate ./...` |
| Regen CLI docs | `go run ./cmd/gen-docs --doc-path docs --markdown --website` |
| Find a spec | `.correctless/specs/{feature}.md` |
| CP + firewall pkg ref | `internal/controlplane/CLAUDE.md` + `internal/controlplane/firewall/CLAUDE.md` |
| Architecture reference | `.claude/docs/ARCHITECTURE.md` |
| Type/abstraction index | `.claude/docs/KEY-CONCEPTS.md` |
| Workflow state | `.correctless/artifacts/workflow-state-*.json` |
| Outstanding features | `.serena/memories/outstanding-features` |
| Bug tracker | `.serena/memories/bug-tracker` |
