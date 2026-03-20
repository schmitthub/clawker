# Brainstorm: Shared Container Start Migration

> **Status:** Active
> **Created:** 2026-03-19
> **Last Updated:** 2026-03-19 00:00

## Problem / Topic
The firewall lifecycle (daemon startup, rule syncing, health checks, IP resolution) was incorrectly placed in `buildRuntimeEnv()` inside `CreateContainer()`. This means `container create` starts the entire Envoy+CoreDNS stack as a side effect, while `container start` has zero firewall awareness. Baked-in env vars (Envoy/CoreDNS IPs) go stale if the network changes between create and start. The `FirewallEnabled()` settings check is also never consulted — the firewall always starts regardless of user settings.

## Open Items / Questions
- Where should firewall orchestration live? (start time, not create time)
- Should env vars be resolved at start time or discovered inside the container via Docker DNS?
- How does `container run` change? (it's create+start, so needs both phases)
- What about `loop` commands that also call `CreateContainer`?
- Should `buildRuntimeEnv` still compute firewall env vars at create time, or should they be injected at start time?
- How does the settings gate (`FirewallEnabled()`) get wired in?

## Decisions Made
- (none yet)

## Conclusions / Insights
- `container create` calls `CreateContainer()` → `buildRuntimeEnv()` which starts daemon, syncs rules, waits for healthy, resolves IPs — all runtime concerns
- `container start` has zero firewall logic — doesn't ensure daemon, doesn't check health, doesn't sync rules
- `container run` works by accident because it's create+start in one call
- Env vars baked at create time go stale if network changes between create and start
- `FirewallEnabled()` from settings.yaml is defined and tested but never checked in any runtime path
- `--disable-firewall` flag is deprecated no-op
- `enable`/`disable` CLI commands are per-container iptables ops, not global setting toggles

## Gotchas / Risks
- Docker container env vars are immutable after create — can't inject new env at start time via Docker API
- Containers on `clawker-net` can use Docker embedded DNS to resolve `clawker-envoy` / `clawker-coredns` by name
- The daemon's `watchContainers` loop will see zero agent containers after a bare `create` and tear down the stack

## Unknowns
- Can `init-firewall.sh` reliably resolve container names via Docker DNS at boot?
- What happens if init-firewall.sh runs before Envoy/CoreDNS containers are healthy?
- Does `container start` have access to config/Factory to do firewall orchestration?

## Next Steps
- Discuss: where does firewall orchestration belong in the start path?
- Discuss: Docker DNS name resolution vs baked-in IPs
- Discuss: settings gate wiring
