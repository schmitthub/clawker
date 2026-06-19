# Bug / Feature Tracker

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
- [ ] `cmd/clawkercp/main.go` should probably be a light wrapper around `internal/controlplane` package instead of doing all the work itself.
- [ ] `clawker firewall up` doesn't need to display the stack ip and network id (its not even the name lol its a long random id). its totally useless info to the user.
- [ ] controlplan up doesn't start firewall but should via settings
- [ ] make localenv seems to break mounting on container create ie `clawker run` forcing a `docker run` and mounting the volumes seems to fix it. so something is up with bind mounting during container create, after the container is created. claude has no idea what to do its totally going in circles.
- [ ] **CP-resilience audit: pre-existing panics on CP-reachable code paths.** Per the resilience contract (no panics in CP — see root `CLAUDE.md` and `internal/controlplane/CLAUDE.md`), the following panics need to be converted to `(nil, error)` returns or recovered. A panic in any of these crashes CP PID 1, skips `Stack.Stop` + `ebpfMgr.FlushAll`, and leaves eBPF programs pinned with no supervisor (silent firewall failure — agents keep filtering against frozen rules with no observation, no rule updates, no containment dispatch). Sites: `internal/controlplane/server.go:50` (`NewAdminServer`), `internal/controlplane/watcher.go:62/65/68` (`NewAgentWatcher`), `internal/controlplane/agent/registry.go:206` (`Add`), `internal/controlplane/firewall/handler.go::NewHandler` (per KEY-CONCEPTS line — panics on missing `EBPF` or `Resolver`). All are constructor-time nil-dep panics — same shape as `agent.NewExecutor` was before being converted; same fix pattern applies. Add tests pinning the error-return contract.
- [ ] Clawker share dir should be overridable via env var and settings
- [ ] Socketbridge deamon log files need to be rotated and cleaned up somehow. It is also difficult to really track down which daemon log is the most recent one visually especially when hundreds of log files are generated. Aggregating logs into a single file with timestamps and log levels would be ideal.
- [ ] **Monitoring stack hardening pass.** Local-dev stack currently ships with security plugins off, OTLP receiver published on all interfaces, no auth on OpenSearch/Dashboards, no scoping on incoming pushes, and reserved hostnames (`otel-collector`, `prometheus`) resolvable from agent containers behind the firewall (`internal/consts/monitoring.go` `MonitoringServiceHostnames` — OpenSearch + Dashboards are intentionally not forwarded; revisit if that policy changes). Defer until functionality is stable; revisit before any "production" framing.
- [ ] **Transparent TCP for random ports not working** — TLS on non-443, HTTP on non-80 (e.g. `tls://host:4443`, `http://host:8080`) doesn't route through Envoy. Needs Envoy config update. (Migrated from `firewall-egress-stack-e2e-bugs`; may have been addressed by eBPF pivot on `fix/project-egress-priority` — re-verify against current `internal/firewall/envoy.go` before triaging.)
- [ ] **HTTP over raw IP (no domain) never tested** — unclear whether it's even implemented. (Migrated from `firewall-egress-stack-e2e-bugs`; same re-verify note as above.)
- [ ] **Consider wiring CLI logger mTLS.** `internal/cmd/factory/default.go::newLogger` wires `clawker-cli` with `Insecure: true` to the untrusted `otlp` receiver on `OtelGRPCPort`; the file-path triple on `logger.OtelOptions` has no in-tree consumer. NOT a security trust issue — CLI log records carry no repudiation weight; spoofing `service.name=clawker-cli` doesn't compromise the trusted forensic indices (which are scoped to CP/Envoy/CoreDNS via the `otlp/infra` receiver's infra-intermediate `client_ca_file`). Consideration is operational hygiene: anyone with reachability to the collector's public OTLP port can inject `clawker-cli` events. Two paths if revisited: (a) mint a CLI-scoped infra leaf via `otelcerts` and add a `clawker-cli` branch to `routing/trusted` in `otel-config.yaml.tmpl`, or (b) leave on plaintext lane and document as intended. Either way, the dormant file-path triple on `OtelOptions` should be reconciled — delete it, or wire a real consumer.
- [ ] Running a container outside of a registered project directory fails with
```
Error: Failed to copy files to container 'bda850fbedc4e9f7dce999fc621a60127da6ea4beed26af3a392ebba25e475b8'
  Details: Error response from daemon: Could not find the file /run/clawker in container bda850fbedc4e9f7dce999fc621a60127da6ea4beed26af3a392ebba25e475b8

Next Steps:
  1. Check if the container exists: docker ps -a
  2. Verify the destination path is valid
  3. Check if the source file exists
```
- [ ] logs from infra containers on linux are owned by root and unread without sudo
- [ ] clawker needs to remove containers if CP fails. containers should be atomic on any failure period 
- [ ] **Firewall Envoy/CoreDNS sibling drift gate misses image/binary/config changes** ([#308](https://github.com/schmitthub/clawker/issues/308)). `firewall.Stack.ensureContainer` (`internal/controlplane/firewall/stack.go:791`) only compares `infra_certs_ready` + `otel_infra_port` labels — does NOT detect Envoy image digest bumps, embedded CoreDNS binary updates, or `envoy_config.go`/`coredns_config.go` template changes. Same bug class as CP drift gate (#300) but on the sibling containers CP manages. Security-relevant: a clawker CLI upgrade with an Envoy CVE patch, CoreDNS dnsbpf fix, or deny-chain hardening won't reach users until a rule mutation OR manual `docker rm`. Fix: stamp `cpBinaryHash()` as a third drift label so any CP-binary-affecting change recreates the siblings.
- [ ] **Replace FNV domain hash with userspace-allocated identity (Cilium pattern).** `internal/controlplane/firewall/ebpf/types.go::DomainHash` derives a 32-bit FNV-1a from the domain string; that hash keys `dns_cache`, `route_map`, and lands on every netlogger record. Theoretically collision-vulnerable. Cilium uses sequential u32 identities allocated by userspace (`pkg/fqdn/namemanager`, `IPCache`); Tetragon doesn't do per-domain BPF enforcement at all. Fix: CP-side identity allocator, BPF maps re-keyed on `identity`, dnsbpf writes identities not hashes, netlogger reverse map collapses to identity→domain direct read. See Serena memory `initiative_route_identity_allocator` for scope.

## Storeui 

- [ ] multiline boxes should accept shift+enter for new lines and enter to save 
- [ ] Audit fields for usage — "build.timeout", "build.start_period", "build.retries", "agent.includes" may be unused/legacy
- [ ] "agent.memory" and similar fields should be grouped in an "advanced" collapsible section
- [ ] Field descriptions inaccurate — "command" says "healthchecks", SHELL says "Default shell for RUN instructions" but it's the terminal shell env var
- [ ] firewall rules editor should be a structured form, not multiline text
- [ ] firewall rules preview shows raw Go map literal instead of formatted display
- [ ] firewall rules duplication bug (github ssh appears twice)
