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
| `handler.go` | `firewall.Handler` — implements all 13 firewall RPCs on `adminv1.AdminServiceServer` (B2 scope-corrected surface, see "Handler RPCs" below) |
| `drift.go` | `resolveBypassCgroupID` — shared INV-B2-016 drift resolver; consulted by `FirewallEnable` and the bypass dead-man timer |
| `ebpf/` | eBPF subsystem (relocated from `internal/controlplane/ebpf/`) — `Manager.Load()`, pinned maps, DomainHash, types |
| `testdata/` | Golden files (e.g., `corefile_basic.golden`) |
| `assets/` | `coredns-clawker` Linux binary (gitignored; built by `make coredns-binary`) |

## Handler RPCs (B2 scope-corrected surface)

`firewall.Handler` implements every method on `adminv1.AdminServiceServer`. The composite `controlplane.adminServer` embeds `*firewall.Handler` and registers as the sole AdminService — future cross-domain handlers (monitor, hostproxy, clawkerd) plug in by being embedded alongside.

| RPC | Scope | Purpose |
|-----|-------|---------|
| `FirewallInit` | global | Bring stack up + idempotent BPF readiness check. Returns Envoy/CoreDNS IPs + network ID. |
| `FirewallRemove` | global | Stop stack, flush all eBPF state, cancel bypass timers, wipe rules store. |
| `FirewallEnable(container_id, config)` | per-container | Idempotent enroll. INV-B2-016 drift guard: every call resolves `container_id → cgroup_path` via Docker; logs warning on stored-vs-fresh `cgroup_id` drift; returns `FailedPrecondition` if Docker says the container is gone. |
| `FirewallDisable(container_id)` | per-container | Set BPF bypass for the container. Falls back to stored `cgroup_id` when Docker reports the container gone; no-op for unknown containers. |
| `FirewallBypass(container_id, timeout)` | per-container | `Disable` + dead-man timer that calls drift-guarded `Enable` on expiry. Caps at 1h. |
| `FirewallAddRules` / `FirewallRemoveRules` | global | Mutate rules store (validation all-or-nothing on Add) and hot-reload Envoy + CoreDNS. |
| `FirewallListRules` / `FirewallStatus` | global | Read-only normalized rule dump and stack health snapshot. |
| `FirewallReload` | global | Regenerate configs and restart the stack without rule mutation. |
| `FirewallRotateCA` | global | Regenerate MITM CA + per-domain certs and reload. |
| `FirewallSyncRoutes` / `FirewallResolveHostname` | global | Break-glass route re-sync; DNS lookup from CP netns (used by enroll for `host.docker.internal`). |

Per-container requests carry only `container_id` — there is no `cgroup_path` field. The Handler holds a cached `cgroupDriver` from `DetectCgroupDriver` at startup and resolves paths internally via `EBPFCgroupPath` + `ResolveContainerID` + the Docker-backed `ContainerResolver`.

`HandlerDeps` bundles collaborators (`EBPF`, `Stack`, `Store`, `Cfg`, `Resolver`, `Log`, optional `CertDirFn`). `NewHandler` panics on missing `EBPF` or `Resolver` (every RPC hits them); other fields stay optional so the handler can be unit-tested without spinning up Stack/Store.

## Stability Expectations

- Tasks 2–5 layered `Stack`, cgroup helpers, drift-guarded per-container enforcement, and the 13-method scope-corrected proto surface onto the relocated package. Per-method scopes remain uniformly `"admin"` (INV-B2-009).
- Full architecture reference is drafted in Task 8 once the migration settles.

## See Also

- `../CLAUDE.md` — CP core (Ory auth, startup sequencing, container config)
- `ebpf/CLAUDE.md` — eBPF subsystem details
- `.correctless/specs/cp-initiative/branch-2-cp-owns-firewall.md` — migration spec
- `.serena/memories/cp-initiative-branch-2-firewall-migration-plan` — task-by-task plan
