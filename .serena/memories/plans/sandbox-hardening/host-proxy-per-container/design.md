# Item 4: Per-container host-proxy bypass from the project's config (ruled 2026-10-24)

Part of `mem:plans/sandbox-hardening/initiative` (Task 3).

## Problem

`controlplane/firewall/handler.go` `resolveHostProxy` (~1354) reads `h.cfg.SecurityConfig().HostProxyEnabled()`. `h.cfg` is CP's own `config.NewConfig()` (`internal/controlplane/cmd.go` ~1521); inside the CP container the project store resolves only the config-dir layer plus defaults (`enable_host_proxy` defaults true), never the per-project `clawker.yaml`, which lives in the project tree on the host. So every enrolled cgroup receives `host_proxy_ip/port` and the eBPF bypass at `common.h` ~862 admits it. Verified: a project with `enable_host_proxy: false` and `forward_https: false` pulled a host GitHub credential and opened the host browser once another agent's daemon was running.

## Ruling

- CLI pushes the project's value; CP persists it per container and replays it. Same shape as egress rules and route identities.
- Rejected: a Docker label carrier; reading the in-container copy of `clawker.yaml` (agent-writable in snapshot mode); CP reading project config itself (not mounted, cannot be for all projects); daemon-side refusal as the fix (lane would remain reachable at the network layer).

## Changes

- `api/admin/v1/admin.proto`: `FirewallEnableRequest` gains `bool host_proxy = 2;`. Regenerate bindings and the `AdminServiceClientMock`.
- CLI: `internal/cmd/container/shared/container_start.go` `BootstrapServicesPostStart` passes `security.HostProxyEnabled()`, which `BootstrapServicesPreStart` already evaluates (~83) for daemon start. One source: compute once, pass to both phases.
- CP store: new `firewall.EnrollmentStore` (interface + store-backed impl in `controlplane/firewall/`, moq mock in `controlplane/firewall/mocks/`), persisted in the firewall data dir beside the route identity table: `map[containerID]{HostProxy bool}`. Follow the store verbs convention; no wrapper layer. Wire through `buildEnforcement` as an interface like `RouteIdentityStore`.
- Handler: `FirewallEnable` writes the record then enrolls with `resolveHostProxy(ctx, req.HostProxy)`. `reenrollAgents` reads the record per container; missing record → no bypass (fail closed, logged once per container with `event=firewall_enroll_no_record`). `resolveHostProxy` loses its `SecurityConfig` read; it keeps the daemon port range check and the `host.docker.internal` resolution, gated by the passed bool.
- Eviction: subscribe to `dockerevents` `container/destroy` for `purpose=agent` in the firewall handler (same topic the agent registry uses for eviction) and delete the record. Stop/die do not evict.
- `FirewallDisable`/`FirewallRemove` leave the record; `FirewallEnable` overwrites it on every start, so a config change takes effect at the next container start.

## Checks

- Handler tests with `EBPFManagerMock`: `FirewallEnable{host_proxy:false}` → `Install` called with zero `HostProxyIp/Port`; `{true}` → resolved values; `reenrollAgents` replays stored true/false and defaults to no bypass; destroy evicts.
- CLI test: `BootstrapServicesPostStart` sends the project value (`AdminServiceClientMock` call capture).
- Host e2e: two projects, one disabled; after enable and after a CP restart, the eBPF config map for the disabled container has `host_proxy_ip == 0`.
- Runtime from the disabled container with another enabled agent running: `curl http://host.docker.internal:<daemon port>/` is refused.

## Out of scope

- Any auth, scoping, or per-request checks in the host proxy daemon. Enabled means full grant.
