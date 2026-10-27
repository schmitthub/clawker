# Firewall

- `controlplane/firewall/` owns rule persistence, reconciliation, Envoy/CoreDNS configuration, and firewall certificates.
- `controlplane/firewall/ebpf/` owns enforcement. `internal/dnsbpf/` is the CoreDNS plugin; `cmd/coredns-clawker/` builds the custom DNS binary.
- `internal/bundler.EgressRules` composes harness-required rules with project rules. CLI callers send them through AdminService.
- Mutations pass through the firewall action queue. Do not bypass its serialized reconciliation.
- There is no eBPF host-proxy lane (removed 2026-10-26). A direct dial to `host.docker.internal` gets no bypass; host actions go agent -> clawkerd relay -> CP AgentService -> host proxy (`mem:controlplane/agent/core`). `container_config` is 32 bytes. There is no `EnrollmentStore`; `FirewallEnableRequest` carries only the container ID (field 2 reserved).
- clawker-net is an eBPF allowlist, not an open subnet. `allowed_internal` (`ebpf/bpf/common.h`) admits Envoy on any port, CoreDNS on 53, and the CP on `AgentPort` and `HydraPublicPort`; every other in-subnet destination is denied in the kernel. The CP address and the two ports travel in `container_config` (`cp_ip`, `cp_agent_port`, `cp_hydra_port`), built by `Handler.containerConfig` for both enroll sites from `NetworkInfo.CPIP` (`consts.CPIPLastOctet`) and `ControlPlaneSettings`. Before the loopback pass-through, `decide_connect`/`decide_sendmsg` deny `DOCKER_EMBEDDED_DNS` (127.0.0.11) on every port except the 53 redirect: Docker's resolver listens on a random port there and forwards upstream outside eBPF (bypass proven 2026-10-26, e2e `TestFirewall_DockerDNSDirectPortDenied`). `sock_create` denies `SOCK_RAW` and `SOCK_DGRAM` with `IPPROTO_ICMP`/`IPPROTO_ICMPV6` (ping sockets send through `ping_v4_sendmsg`, which no sendmsg hook sees; unconnected ICMP tunnel proven 2026-10-26, e2e `TestFirewall_ICMPBlocked`). The host-proxy bypass is checked before the allowlist (`host.docker.internal` can resolve in-subnet on some daemons). `Load()`'s stale-link sweep keeps every pinned link the kernel still reports attached to a cgroup and never reads `container_map`, so a value-layout change (empty new map until re-enroll) never sweeps live agents' links, even after a failed bringup.
- CoreDNS has no always-on `docker.internal` zone (removed 2026-10-27). Docker DNS sends names it does not know to the host resolvers, so an open subtree is a DNS exfiltration path. Only a user rule for `host.docker.internal` opens a zone: exact name only (even for a wildcard rule), forwarded to `127.0.0.11`. No reserved hosts in the identity sync.

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
