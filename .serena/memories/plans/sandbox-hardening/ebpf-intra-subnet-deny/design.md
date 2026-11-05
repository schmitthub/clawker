# Item 3: eBPF intra-subnet deny-by-default (ruled 2026-10-24)

Part of `mem:plans/sandbox-hardening/initiative` (Task 7). Run after `mem:plans/sandbox-hardening/monitoring-off-clawker-net/design` so no agent needs a monitoring host.

## Problem

Both eBPF decision functions return "allow, no redirect" for any destination inside the clawker-net subnet: TCP connect path `controlplane/firewall/ebpf/bpf/common.h` ~859, UDP sendmsg path ~970 (`is_in_subnet(dst_ip, cfg->net_addr, cfg->net_mask)`). Verified from an agent: another agent (172.18.0.7) pingable and any `0.0.0.0` bind on it reachable; CP reachable on 7080/7444/4444; monitoring services reachable.

## Ruling

Replace the early return in both paths with an explicit allowlist. Everything else in-subnet gets a deny verdict with `metric_inc(..., ACTION_DENY)`:

| Destination | Ports | Why |
|---|---|---|
| `cfg->envoy_ip` | any | per-route Envoy listener ports are dynamic; Envoy admin is loopback-only inside its container |
| `cfg->coredns_ip` | 53 | already handled by the DNS redirect above; keep for clarity |
| `cfg->cp_ip` | `cfg->cp_agent_port` (AgentPort, default 7444) | Register + `Export` |
| `cfg->cp_ip` | `cfg->cp_hydra_port` (Hydra public, default 4444) | clawkerd one-time assertion exchange; requires a CLI-signed assertion, unusable by the unprivileged user |

Denied: CP healthz 7080, SDS 7445, admin 7443, every other agent container, any future clawker-net service unless added here.

## Changes

- `clawker_container_config` (struct in `common.h`, Go mirror in `controlplane/firewall/ebpf/manager.go`): add `cp_ip` (u32), `cp_agent_port`, `cp_hydra_port` (u16). Keep field order stable for the map value layout; regenerate bindings.
- `NewContainerConfig`: new params; both enroll sites in `controlplane/firewall/handler.go` (`reenrollAgents` ~412 and `FirewallEnable` ~580) pass them. CP IP comes from `firewall.Stack.NetworkInfo` (add the CP container's own clawker-net address if not already there; CP knows its container via the Docker client). Ports from `cfg.ControlPlaneSettings()`.
- Decision functions: after `is_loopback`, replace the subnet early return with `if (is_in_subnet(...)) { if (allowed_internal(cfg, dst_ip, dst_port)) return r; r.verdict = V_DENY; metric_inc(cgroup_id, 0, dst_port, ACTION_DENY); return r; }`. Shared `static __always_inline bool allowed_internal(...)` so the TCP and UDP paths cannot drift. The host-proxy bypass and gateway lockdown branches below it are unchanged (host-proxy IP is the host gateway, outside the subnet check).
- Verify the UDP path emits the deny verdict the same way the unrouted-UDP branch does (EPERM to the caller) and that `recvmsg4` is unaffected.
- IPv6: clawker-net has no IPv6; `connect6`/`sendmsg6` already deny or redirect; leave unchanged, add a test asserting that stays true.

## Checks

- Go struct layout test against the C struct size (existing pattern in `manager_test.go`).
- Handler tests: both enroll paths populate `cp_ip` and ports.
- Host e2e (`test/e2e`, never with `CLAWKER_AGENT` set): two agents; cross-agent TCP and UDP fail; CP 7444 and 4444 connect; 7080 fails; DNS and allowed HTTPS egress still work.
- firewall-uat skill gains the in-container probes: `nc -zv -w2 <other-agent-ip> 22`, `nc -zuv -w2 <other-agent-ip> 9`, `nc -zv -w2 <cp-ip> 7444`, `nc -zv -w2 <cp-ip> 7080`.

## Out of scope

- Loopback filtering inside the container. The relay in item 2 relies on loopback.
- Restricting the Envoy port set.
