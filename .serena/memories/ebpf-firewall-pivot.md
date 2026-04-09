# eBPF Firewall Pivot

## Status: Design doc written, implementation not started

## Context
Branch `fix/project-egress-priority` originally aimed to fix TCP/SSH iptables ordering with content-addressed ports. Pivoted to eBPF after discovering iptables fundamentally can't route per-domain on the same port.

## Design Doc
`.claude/docs/EBPF-DESIGN.md` — comprehensive design covering:
- 5 BPF programs (connect4, sendmsg4, connect6, sendmsg6, sock_create)
- 4 BPF maps (container_map, bypass_map, dns_cache, route_map)
- CoreDNS dns-to-bpf plugin for DNS→IP mapping
- eBPF Manager container (alongside Envoy/CoreDNS)
- Fallback to iptables for old kernels (cgroup v1)
- Migration path (4 phases)

## Key Decisions
- Full iptables replacement, not hybrid (user directive: "if we're going to eBPF we aren't maintaining iptables too")
- `cilium/ebpf` Go library for BPF program loading/map management
- CoreDNS plugin approach for DNS→BPF map population (Cilium's pattern, simpler)
- eBPF Manager as dedicated privileged container (works on Linux + Docker Desktop)
- Agent containers need zero modifications (no iptables, no caps, no firewall.sh)

## Previous Work on Branch (Committed)
- Package rename: `internal/docker/dockertest` → `internal/docker/mocks`
- `FormatPortMappings()` on Manager
- `localLayerFirewallRules()` for per-container priority
- 9 priority tests passing
- Doc updates across ~15 files

## Open Questions from Design Doc
1. eBPF Manager communication: docker exec vs Unix socket
2. CoreDNS image: custom build vs external plugin
3. BPF map sizing
4. Wildcard domain handling in dns_cache
