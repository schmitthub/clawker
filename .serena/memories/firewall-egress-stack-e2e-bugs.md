# Firewall E2E Tests and Bug fixes Session (2026-03-18)

**Branch:** `feat/global-firewall`

## Bugs

- [x] **Bypass incomplete**: Fixed — three root causes: (1) Dante hardcoded `external: eth0` but container interface varies; now detects via `/proc/net/route`. (2) `hostConfig.DNS = CoreDNS` made Docker forward ALL DNS through CoreDNS including root's; removed — iptables handles DNS filtering. (3) init-firewall.sh used catch-all rules instead of targeting container user UID; now targets `CLAWKER_USER` UID specifically (matching openclaw-deploy reference).
- [x] **Stack shuts down unexpectedly**: Fixed — daemon was not properly tracking clawker containers.
- [x] **Rules not tested**: Fixed — `TestFirewall_ConfigRules` tests TLS rules, TCP rules (otel-collector:4317), concurrent config sync (container start + CLI firewall add), and verifies rules in global list
- [x] **Merged egress rules all port 0**: Fixed — `normalizeRule()` in rules.go sets TLS→443, SSH→22. TCP port 0 = any port (intentional). All store writes go through normalization.
- [x] **Monitoring stack blocked by firewall**: Fixed — added iptables RETURN rules for `CLAWKER_NET_CIDR` (Docker-assigned clawker-net subnet) in init-firewall.sh. Intra-network traffic is not egress and bypasses Envoy entirely. The CIDR was already wired end-to-end (`Manager.discoverNetwork()` → `env.go` → container env var) but unused. New test: `TestFirewall_IntraNetworkBypass` verifies agent→clawker-net connectivity without explicit rules.
- [x] **Project rules not synced after daemon start**: Fixed — container creation calls `AddRules` with fresh project config. `regenerateAndRestart` skips container restart if stack not running (configs written to disk, daemon picks them up on start).
- [ ] clawker firewall up hangs on the process and blocks instead of starting it in the background as a daemon
- [ ] Never tested firewall disable command
- [ ] never tested firewall disabled setting 
- [ ] Never tested http over raw IP with no domain. should have been implemented but may have been skipped by you lazy eager agents who love to cut corners and avoid features you find icky instead of just googling it
- [ ] **Host proxy OAuth callback broken with firewall enabled**: OAuth browser kickoff works (container→host proxy `POST /open/url` succeeds, browser opens). Callback does not arrive back to Claude Code. Diagnostics so far:
  - iptables RETURN rule for host proxy IP+port is present and matching packets (verified via `iptables -t nat -L OUTPUT -n -v`)
  - `host.docker.internal` resolves to `192.168.65.254` (IPv4 via `getent ahosts`; `getent hosts` returns IPv6 only — use `ahosts`)
  - Host proxy `/health` returns HTTP 200 from inside firewalled container
  - Callback registration (`POST /callback/register`) succeeds from inside container
  - Callback polling (`GET /callback/{session}/data`) works manually from inside container
  - Full round-trip test (register → trigger callback on host → poll from container) returns `received: true` with correct data
  - `callback-forwarder` binary runs correctly when invoked manually with `-v` flag — connects, polls, times out normally
  - `callback-forwarder` processes spawned by `host-open.sh` during real OAuth flow appear as zombies (defunct) — PIDs 317, 801, 3120
  - Claude Code reports: `"OAuth error: The socket connection was closed unexpectedly"` on `fetch()` to `http://localhost:38987/callback...`
  - Worked before the Envoy+CoreDNS firewall was added
  - Hostproxy code may have been overwitten or removed during firewall work. Part of troubleshooting should involve comparing with main. any file related to the hostproxy flow. 


## Session Progress

- Centralised all clawker-net port/IP assignments in `internal/config/consts.go` with Config interface accessors
- Regenerated config mock, wired stubs
- Updated firewall package (manager, daemon, network, envoy, coredns) to use config accessors
- `TestFirewall_Status`, `TestFirewall_AllowedDomain`, `TestFirewall_AddRemove` all pass
- `TestFirewall_Bypass` reaches the bypass script but Dante times out — this is the bypass script logic itself
