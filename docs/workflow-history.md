# Workflow History

### 2026-04-14 — Branch 2: CP Owns the Firewall
Branch: feat/firewall-cp-migration. Rules: 22 (15 invariants + 7 prohibitions). QA rounds: 0. Findings fixed: 3 (re-verification closed all prior blockers in commit `fc253a6c`). Inverted firewall ownership so the clawker control plane container is the single owner of Envoy, CoreDNS, eBPF, and the egress rules store; deleted `internal/firewall/`; CLI commands now speak the 15-method `AdminService` gRPC (`admin` scope on 14 methods; `GetSystemTime` is public, mTLS + OAuth2 JWT) via `f.AdminClient(ctx)`; added `AgentWatcher` drain-to-zero self-shutdown and `clawker controlplane up/down/status` break-glass commands.
