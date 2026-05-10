# Outstanding Features / TODOs

Top-level tracker for features and architectural improvements that are known but not yet implemented.

---

## User notes on potential features

- Root bypassing may no longer be needed — was an escape hatch for iptables era. With eBPF container-id-based toggling, root egress could stay locked down.
- Add a claude hook via managed settings in /etc/claude-code to require approval when editing clawker.yaml (defensive measure against image pollution).
- iptables should default to failsafe fallbacks (no traffic) if eBPF/fw stack fails.
- Corefile and envoy config should always be regenerated on startup/init to pick up new generation logic.

---

## 2. Native IPv6 support

**Status:** not supported (documented limitation)
**Scope:** large

Currently: `connect4` = full enforcement, `connect6` = IPv4-mapped works, native IPv6 denied. Works in practice (most programs use dual-stack + A records) but `curl -6` is blocked.

Full support needs: IPv6 Envoy listeners, IPv6-keyed dns_cache map, IPv6 cluster endpoints, IPv6 container_config fields, IPv6 subnet/loopback checks in connect6, AAAA handling in dnsbpf plugin, IPv6 network creation in firewall config.

Touches almost every part of firewall stack. Multi-task initiative.

---

## 4. OTel monitoring from eBPF (container traffic + eBPF logs)

**Status:** not started
**Scope:** medium

**Want:** Per-container traffic metrics from BPF `metrics_map` (bytes/packets per {cgroup_id, domain_hash, dst_port, action}) exported as OTel metrics. Also BPF program event logs via ring buffer.

**Existing:** `metrics_map` + `metric_inc()` already in BPF programs. Monitor stack (Grafana/Prometheus/Loki) exists. Logger has OTel bridge.

**Missing:** Go-side metrics scraper, BPF ring buffer for events, Grafana dashboard panels.

---

## 5. clawkerd → container death linkage

**Status:** not started
**Scope:** small-to-medium
**Surfaced:** 2026-04-26 UAT

If clawkerd dies, container keeps running with no CP channel. Need death linkage.

**Options:** (A) clawkerd as PID 1 with signal forwarding + child management, (B) bash supervisor that kills container on clawkerd exit, (C) health-check based with CP-side reaper.

**Recommendation:** Option B first (cheap, reversible), promote to A if needed. Should coordinate with reconnect-with-backoff work.

---

## 6. CP endpoint env-var disclosure

**Status:** not started
**Scope:** small
**Surfaced:** 2026-04-26 UAT

`CLAWKER_CP_HYDRA_URL` + `CLAWKER_CP_AGENT_ADDR` visible to unprivileged user via `env`. No auth material leaks (bootstrap is root-only), but leaks CP network topology.

**Fix:** Move to bootstrap-dir files (root:0400), drop env vars. Clawkerd already reads bootstrap from disk.

---

## Cross-cutting notes

Items 4 and 5 are natural companions — the CP daemon is the home for the metrics scraper. Not in scope: specific egress rule features (path rules, wildcards, IP ranges).
