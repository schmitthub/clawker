# Firewall

- `controlplane/firewall/` owns rule persistence, reconciliation, Envoy/CoreDNS configuration, and firewall certificates.
- `controlplane/firewall/ebpf/` owns enforcement. `internal/dnsbpf/` is the CoreDNS plugin; `cmd/coredns-clawker/` builds the custom DNS binary.
- `internal/bundler.EgressRules` composes harness-required rules with project rules. CLI callers send them through AdminService.
- Mutations pass through the firewall action queue. Do not bypass its serialized reconciliation.
- The host-proxy eBPF bypass is per container. The CLI sends the project's `enable_host_proxy` on `FirewallEnable`; the CP persists it in the `EnrollmentStore` (`enrollments.yaml`, beside the identity table) and replays it on restart. No record means no bypass. CP's own config never decides it: `clawker.yaml` is not visible to the CP. Every enrollment write (the enable write and the destroy evict) runs on the ActionQueue with the install it decides; the store carries no lock.
- clawker-net is an eBPF allowlist, not an open subnet. `allowed_internal` (`ebpf/bpf/common.h`) admits Envoy on any port, CoreDNS on 53, and the CP on `AgentPort` and `HydraPublicPort`; every other in-subnet destination is denied in the kernel. The CP address and the two ports travel in `container_config` (`cp_ip`, `cp_agent_port`, `cp_hydra_port`), built by `Handler.containerConfig` for both enroll sites from `NetworkInfo.CPIP` (`consts.CPIPLastOctet`) and `ControlPlaneSettings`. The host-proxy bypass is checked before the allowlist (`host.docker.internal` can resolve in-subnet on some daemons). `Load()` checks the old pinned `container_map` with `LookupBytes` so a value-layout change never sweeps live agents' links.

## Rules and identity

- Rule identity is destination + protocol + port. Path rules have their own path key.
- A bare domain matches only that host. A leading-dot wildcard includes the apex and subdomains.
- Paths are literal prefixes unless marked as anchored RE2 expressions. HTTP method filters are separate.
- `firewall refresh` adds or updates config rules. Removing config text does not remove a stored rule; removal/prune operations do that.
- `IdentityAllocator` assigns and persists route identities. Do not derive them from a domain hash.
- Live destination identities must stay stable across rule changes and CP restarts because DNS entries remain pinned.
- A missing identity produces no route or DNS directive. Log the failure; keep traffic blocked.

## TLS and telemetry

- Exact hosts use exact certificate handling. Wildcard HTTPS/WSS rules use the SDS path to obtain a leaf for the requested SNI, including deeper names.
- `sds_server.go` serves allowed names from the shared `CAStore`. CP listener wiring is in `internal/controlplane/cmd.go`.
- `controlplane/sdscerts/` owns the dedicated Envoy SDS client identity. Do not substitute the telemetry client certificate.
- Envoy validates upstream (re-encrypted) server certificates against `upstream-ca.crt`, which CP writes into the certs directory from the Mozilla root store module (`x509roots/fallback/bundle`, `upstreamca.go`). Never point `trusted_ca` at the Envoy image's `/etc/ssl/certs/ca-certificates.crt`: that Debian bundle lags the root program (eMudhra incident, October 2026).
- Missing SDS support must not stop unrelated CP services.
- `ebpf/netlogger/` exports kernel egress events. Its failure must not disable enforcement.
- Package contracts: `controlplane/firewall/AGENTS.md`, `controlplane/firewall/ebpf/AGENTS.md`, `controlplane/sdscerts/AGENTS.md`.
- For CP shutdown and failure rules: `mem:controlplane/core`.
- For privileged and live Docker validation limits: `mem:testing/core`.
