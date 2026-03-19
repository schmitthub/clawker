# Firewall E2E Tests and Bug fixes Session (2026-03-18)

**Branch:** `feat/global-firewall`

## Bugs

- [x] **Bypass incomplete**: Fixed — three root causes: (1) Dante hardcoded `external: eth0` but container interface varies; now detects via `/proc/net/route`. (2) `hostConfig.DNS = CoreDNS` made Docker forward ALL DNS through CoreDNS including root's; removed — iptables handles DNS filtering. (3) init-firewall.sh used catch-all rules instead of targeting container user UID; now targets `CLAWKER_USER` UID specifically (matching openclaw-deploy reference).
- [x] **Stack shuts down unexpectedly**: Fixed — daemon was not properly tracking clawker containers.
- [ ] **Rules not tested**: We have not tested config firewall.rules only config firewall.add_domain
- [x] **CA Certs wrong dir**: Fixed — EnsureCA was writing ca-cert.pem/ca-key.pem to dataDir instead of dataDir/certsDir. RotateCA cleanup simplified to single RemoveAll.
- [x] **IP collision with monitoring stack**: Envoy (.2) and CoreDNS (.3) collided with monitoring DHCP containers. Fixed: moved to .200/.201. All port/IP consts centralised in config/consts.go.
- [x] **ensureContainer recreated unnecessarily**: Was removing and recreating stopped containers instead of just starting them. Fixed.
- [ ] Test if monitoring stack containers blocked by firewall/iptables (ie allow for docker internal networking still)
- [ ] Consider restoring domain groups for conveninence (ie python, github, google cloud, etc) - pain in the ass to keep on top of tho
- [ ] Merged egress rules in clawker state all say port 0 ex `.clawkerlocal/.local/share/clawker/firewall/egress-rules.yaml`
- [ ] Never testing firewall disable
- [ ] clawker firewall up hangs on the process and blocks instead of starting it in the background as a daemon

## Session Progress

- Centralised all clawker-net port/IP assignments in `internal/config/consts.go` with Config interface accessors
- Regenerated config mock, wired stubs
- Updated firewall package (manager, daemon, network, envoy, coredns) to use config accessors
- `TestFirewall_Status`, `TestFirewall_AllowedDomain`, `TestFirewall_AddRemove` all pass
- `TestFirewall_Bypass` reaches the bypass script but Dante times out — this is the bypass script logic itself
