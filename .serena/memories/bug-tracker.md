# Bug / Feature Tracker

- [ ] Clawker share dir should be overridable via env var and settings 
- [ ] Socketbridge deamon log files need to be rotated and cleaned up somehow. It is also difficult to really track down which daemon log is the most recent one visually especially when hundreds of log files are generated. Aggregating logs into a single file with timestamps and log levels would be ideal.
- [ ] Egress monitoring for the firewall stack 
- [ ] Might be nice to have all logging also aggregated in monitoring stack  
- [ ] **Transparent TCP for random ports not working** — TLS on non-443, HTTP on non-80 (e.g. `tls://host:4443`, `http://host:8080`) doesn't route through Envoy. Needs Envoy config update. (Migrated from `firewall-egress-stack-e2e-bugs`; may have been addressed by eBPF pivot on `fix/project-egress-priority` — re-verify against current `internal/firewall/envoy.go` before triaging.)
- [ ] **HTTP over raw IP (no domain) never tested** — unclear whether it's even implemented. (Migrated from `firewall-egress-stack-e2e-bugs`; same re-verify note as above.)
