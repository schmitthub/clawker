# Firewall E2E Tests and Bug fixes Session (2026-03-18)

**Branch:** `feat/global-firewall`

## Bugs

- [x] **Bypass incomplete**: Fixed — three root causes: (1) Dante hardcoded `external: eth0` but container interface varies; now detects via `/proc/net/route`. (2) `hostConfig.DNS = CoreDNS` made Docker forward ALL DNS through CoreDNS including root's; removed — iptables handles DNS filtering. (3) init-firewall.sh used catch-all rules instead of targeting container user UID; now targets `CLAWKER_USER` UID specifically (matching openclaw-deploy reference).
- [x] **Stack shuts down unexpectedly**: Fixed — daemon was not properly tracking clawker containers.
- [x] **Rules not tested**: Fixed — `TestFirewall_ConfigRules` tests TLS rules, TCP rules (otel-collector:4317), concurrent config sync (container start + CLI firewall add), and verifies rules in global list
- [x] **Merged egress rules all port 0**: Fixed — `normalizeRule()` in rules.go sets TLS→443, SSH→22. TCP port 0 = any port (intentional). All store writes go through normalization.
- [x] **Monitoring stack blocked by firewall**: Partially fixed — TCP rules now work via per-rule iptables DNAT to dedicated Envoy listeners. otel-collector:4317 verified in E2E. Remaining: clawker-net subnet traffic should bypass Envoy entirely (add iptables RETURN for subnet CIDR), or bake monitoring stack domains/ports into required rules like api.anthropic.com.
- [x] **Project rules not synced after daemon start**: Fixed — container creation calls `AddRules` with fresh project config. `regenerateAndRestart` skips container restart if stack not running (configs written to disk, daemon picks them up on start).
- [ ] clawker firewall up hangs on the process and blocks instead of starting it in the background as a daemon
- [ ] Never tested firewall disable command
- [ ] never tested firewall disabled setting 


## Session Progress

- Centralised all clawker-net port/IP assignments in `internal/config/consts.go` with Config interface accessors
- Regenerated config mock, wired stubs
- Updated firewall package (manager, daemon, network, envoy, coredns) to use config accessors
- `TestFirewall_Status`, `TestFirewall_AllowedDomain`, `TestFirewall_AddRemove` all pass
- `TestFirewall_Bypass` reaches the bypass script but Dante times out — this is the bypass script logic itself
