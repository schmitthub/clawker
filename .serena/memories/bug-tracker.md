# Bug / Feature Tracker

## CP initiative

- [ ] Clawker firewall up / clawker controlplane up fails if CP image isn't built yet. needs to handle building and starting the cp image along with better helper instructions. Seems to have resolved on a second attempt but should be looked into.

```
clawker firewall up
Error: Image 'clawker-controlplane:latest' not found

Next Steps:
  1. Check the image name and tag are correct
  2. Verify you have network access to the registry
  3. Try pulling manually: docker pull clawker-controlplane:latest
```

- [ ] `clawker controlplane up`/`clawker firewall up` give no status output, no progress spinner. just hangs until a success message. bad ux
- [ ] `clawker firewall up` fails with `✗ starting firewall: rpc error: code = Internal desc = firewall init: firewall stack: envoy: creating container clawker-envoy: Failed to create container: Error response from daemon: invalid mount config for type "bind": bind source path does not exist: /root/.local/share/clawker/firewall/envoy.yaml`
- [ ] Not using `internal/docker` client in `cmd/clawker-cp/main.go`
- [ ] `cmd/clawker-cp/main.go` should probably be a light wrapper around `internal/controlplane` package instead of doing all the work itself.
- [ ] `clawker firewall up` doesn't need to display the stack ip and network id (its not even the name lol its a long random id). its totally useless info to the user.
- [ ] CP should remove its container on shutdown imo. unless there is some functional benefit or constraint in doing so
- [ ] Per container firewall readiness probes for clawkerd / agent api. w/ clawkerd keeping networking locked down until it is ready.
- [ ] `clawker firewall remove` always returns a success message even if the domain doesn't exist. this is very dangerous because if a user has a rule for `example.com` and they try to remove `exmaple.com` (typo) it will appear like it succeeded. or if they have a rule for `example.com:80` it will appear like it was removed if they only passed `example.com`(tls).
- [ ] it appears that a container left running with old ebpf rules doesn't get them updated on firewall / CP restart.  
- [ ] controlplan up doesn't start firewall but should via settings
- [ ] controleplane in docker image should be a stage because it requires a full rebuild otherwise
- [ ] make localenv seems to break mounting on container create ie `clawker run` forcing a `docker run` and mounting the volumes seems to fix it. so something is up with bind mounting during container create, after the container is created. claude has no idea what to do its totally going in circles.
- [ ] `internal/controlplane/agent/init_test.go::TestExecutor_Plan_UidGid_RootForDockerSocket_UserForRest` walks `e.plan()` directly and asserts UID/GID/HOME/USER on un-dispatched stages — pins constants against themselves. Should rewrite to capture the dispatched `*clawkerdv1.Command` payloads off `stream.sent` inside `TestExecutor_Run_HappyPath` and assert the privilege fields on the wire frames, so values flow through `runStep`'s payload construction. Landed as-is in commit `ae5dfd4e`.
- [ ] **CP-resilience audit: pre-existing panics on CP-reachable code paths.** Per the resilience contract (no panics in CP — see root `CLAUDE.md` and `internal/controlplane/CLAUDE.md`), the following panics need to be converted to `(nil, error)` returns or recovered. A panic in any of these crashes CP PID 1, skips `Stack.Stop` + `ebpfMgr.FlushAll`, and leaves eBPF programs pinned with no supervisor (silent firewall failure — agents keep filtering against frozen rules with no observation, no rule updates, no containment dispatch). Sites: `internal/controlplane/server.go:50` (`NewAdminServer`), `internal/controlplane/watcher.go:62/65/68` (`NewAgentWatcher`), `internal/controlplane/agent/registry.go:206` (`Add`), `internal/controlplane/firewall/handler.go::NewHandler` (per KEY-CONCEPTS line — panics on missing `EBPF` or `Resolver`). All are constructor-time nil-dep panics — same shape as `agent.NewExecutor` was before being converted; same fix pattern applies. Add tests pinning the error-return contract.

## General

- [ ] Clawker share dir should be overridable via env var and settings
- [ ] Socketbridge deamon log files need to be rotated and cleaned up somehow. It is also difficult to really track down which daemon log is the most recent one visually especially when hundreds of log files are generated. Aggregating logs into a single file with timestamps and log levels would be ideal.
- [ ] Egress monitoring for the firewall stack
- [ ] Might be nice to have all logging also aggregated in monitoring stack  
- [ ] **Transparent TCP for random ports not working** — TLS on non-443, HTTP on non-80 (e.g. `tls://host:4443`, `http://host:8080`) doesn't route through Envoy. Needs Envoy config update. (Migrated from `firewall-egress-stack-e2e-bugs`; may have been addressed by eBPF pivot on `fix/project-egress-priority` — re-verify against current `internal/firewall/envoy.go` before triaging.)
- [ ] **HTTP over raw IP (no domain) never tested** — unclear whether it's even implemented. (Migrated from `firewall-egress-stack-e2e-bugs`; same re-verify note as above.)
- [ ] **Loki ingester wedges into permanent "shutting down" state when Docker disk usage limit is hit.** Container stays running and serves queries (existing data still browsable in Grafana), but every push (otelcol → Loki) is rejected with `gRPC err="Ingester is shutting down"` / HTTP 503. CP + otel-collector are healthy; problem is downstream in the monitoring stack. Observed once (2026-04-26, ~17:47 UTC); resolved by `docker restart loki` after freeing Docker disk space. Two follow-ups: (1) review log retention / volume sizing for Loki + otel-collector + per-container clawkerd.log so the disk doesn't fill in normal operation; (2) investigate whether some component is log-spamming (audit `clawkerd.log` rotation defaults, CP's `informer stats heartbeat` cadence at 30s, otel-collector retry storm when Loki is wedged — the retry loop itself can spam its own log since the writes back-pressure but the log isn't gated).
