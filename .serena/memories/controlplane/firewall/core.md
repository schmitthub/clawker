# Firewall

- `controlplane/firewall/` owns rule persistence, reconciliation, Envoy/CoreDNS configuration, and firewall certificates.
- `controlplane/firewall/ebpf/` owns enforcement. `internal/dnsbpf/` is the CoreDNS plugin; `cmd/coredns-clawker/` builds the custom DNS binary.
- `internal/bundler.EgressRules` composes harness-required rules with project rules. CLI callers send them through AdminService.
- Mutations pass through the firewall action queue. Do not bypass its serialized reconciliation.

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
- Missing SDS support must not stop unrelated CP services.
- `ebpf/netlogger/` exports kernel egress events. Its failure must not disable enforcement.
- Package contracts: `controlplane/firewall/AGENTS.md`, `controlplane/firewall/ebpf/AGENTS.md`, `controlplane/sdscerts/AGENTS.md`.
- For CP shutdown and failure rules: `mem:controlplane/core`.
- For privileged and live Docker validation limits: `mem:testing/core`.
