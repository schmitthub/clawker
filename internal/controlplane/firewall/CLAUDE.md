# Controlplane Firewall Subpackage

> **Status:** scaffold — populated by Branch 2 Task 1 (pure file relocation from `internal/firewall/`) and expanded task-by-task. Full API reference is written in Task 8. The `internal/firewall/` package is deleted in Task 8.

Firewall domain under the control plane. Owns the egress enforcement surface: Envoy + CoreDNS config generation, MITM CA + per-domain certs, egress rules store, eBPF manager (under `ebpf/`), CoreDNS binary embed, Docker network discovery, and the gRPC `AdminServiceServer` firewall-domain handler.

## Package Layout (post-Task-1)

| File | Purpose |
|------|---------|
| `envoy_config.go` | Envoy YAML generation; per-domain filter chains; LOGICAL_DNS clusters; TCP/SSH listeners |
| `coredns_config.go` | Corefile generation; per-domain forward zones; `dnsbpf` plugin directive; catch-all NXDOMAIN |
| `certs.go` | CA keypair generation/loading; per-domain cert signing; wildcard SANs; rotation |
| `rules_store.go` | `EgressRulesFile` schema + `NewRulesStore(cfg)` + rule validation/normalization/dedup |
| `network.go` | `NetworkInfo`, `DiscoverNetwork(ctx, *docker.Client, cfg)` (docker.Client-based), `ComputeStaticIP` |
| `embed_coredns.go` | `//go:embed assets/coredns-clawker` — Linux-static custom CoreDNS with dnsbpf plugin |
| `errors.go` | Sentinel errors (`ErrEnvoyUnhealthy`, `ErrCoreDNSUnhealthy`, `ErrCPUnhealthy`) + `HealthTimeoutError` |
| `handler.go` | `firewall.Handler` — implements `adminv1.AdminServiceServer` firewall methods (B1 semantics; rewritten to 13-method scope-corrected surface in Task 5) |
| `ebpf/` | eBPF subsystem (relocated from `internal/controlplane/ebpf/`) — `Manager.Load()`, pinned maps, DomainHash, types |
| `testdata/` | Golden files (e.g., `corefile_basic.golden`) |
| `assets/` | `coredns-clawker` Linux binary (gitignored; built by `make coredns-binary`) |

## Stability Expectations

- B1 semantics are preserved through Task 1. Tasks 2–5 layer on `Stack`, cgroup helpers, drift-guarded per-container enforcement, and the 13-method scope-corrected proto surface.
- Full architecture reference is drafted in Task 8 once the migration settles.

## See Also

- `../CLAUDE.md` — CP core (Ory auth, startup sequencing, container config)
- `ebpf/CLAUDE.md` — eBPF subsystem details
- `.correctless/specs/cp-initiative/branch-2-cp-owns-firewall.md` — migration spec
- `.serena/memories/cp-initiative-branch-2-firewall-migration-plan` — task-by-task plan
