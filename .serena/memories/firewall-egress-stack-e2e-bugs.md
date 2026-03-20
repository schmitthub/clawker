# Firewall E2E Tests and Bug fixes Session (2026-03-18)

**Branch:** `feat/global-firewall`

## Bugs

- [x] **Bypass incomplete**: Fixed — originally Dante + proxychains based, now pure iptables. Container UID targeting via `CLAWKER_USER`.
- [x] **Docker internal DNS broken with firewall**: Fixed (2026-03-20) — agent containers created with `--dns 1.1.1.2 1.0.0.2` (Cloudflare). Enable flips resolv.conf to CoreDNS; disable restores 127.0.0.11 (Docker DNS → Cloudflare). CoreDNS has forward zones for `docker.internal` and monitoring stack names (`otel-collector`, `jaeger`, `prometheus`, `loki`, `grafana`) that delegate to Docker's 127.0.0.11, preserving internal networking. New test: `TestFirewall_DockerInternalDNS`.
- [x] **Stack shuts down unexpectedly**: Fixed — daemon was not properly tracking clawker containers.
- [x] **Rules not tested**: Fixed — `TestFirewall_ConfigRules` tests TLS rules, TCP rules (otel-collector:4317), concurrent config sync (container start + CLI firewall add), and verifies rules in global list
- [x] **Merged egress rules all port 0**: Fixed — `normalizeRule()` in rules.go sets TLS→443, SSH→22. TCP port 0 = any port (intentional). All store writes go through normalization.
- [x] **Monitoring stack blocked by firewall**: Fixed — added iptables RETURN rules for `CLAWKER_NET_CIDR` (Docker-assigned clawker-net subnet) in init-firewall.sh. Intra-network traffic is not egress and bypasses Envoy entirely. The CIDR was already wired end-to-end (`Manager.discoverNetwork()` → `env.go` → container env var) but unused. New test: `TestFirewall_IntraNetworkBypass` verifies agent→clawker-net connectivity without explicit rules.
- [x] **Project rules not synced after daemon start**: Fixed — container creation calls `AddRules` with fresh project config. `regenerateAndRestart` skips container restart if stack not running (configs written to disk, daemon picks them up on start).
- [x] clawker firewall up hangs on the process and blocks instead of starting it in the background as a daemon
- [x] Never tested firewall disable command
- [x] never tested firewall disabled setting 
- [x] firewall disable doesn't do agent selection "clawker firewall disable --agent dev" fails
- [ ] no path rules e2e tests
- [ ] No TCP support. Transparent tcp for random ports (like tls to 4443, http to 8080) not working. need to update envoy config 
- [ ] Never tested http over raw IP with no domain. should have been implemented but may have been skipped by you lazy eager agents who love to cut corners and avoid features you find icky instead of just googling it
- [x] Proxychains and dante removed from Dockerfile template (Alpine + Debian). Bypass uses pure iptables flush — neither package was referenced by any Go code or script.
- [x] **Host proxy OAuth callback broken with firewall enabled**: OAuth browser kickoff works (container→host proxy `POST /open/url` succeeds, browser opens). Callback does not arrive back to Claude Code. Diagnostics so far:

## Session Progress

- Centralised all clawker-net port/IP assignments in `internal/config/consts.go` with Config interface accessors
- Regenerated config mock, wired stubs
- Updated firewall package (manager, daemon, network, envoy, coredns) to use config accessors
- `TestFirewall_Status`, `TestFirewall_AllowedDomain`, `TestFirewall_AddRemove` all pass
- `TestFirewall_Bypass` reaches the bypass script but Dante times out — this is the bypass script logic itself
