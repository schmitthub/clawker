# Control Plane Initiative — Current Status

## Status: Branch 2 complete. Pending final host-side review.

**Workflow phase**: B2 agent work done; user's host-side E2E sweep + merge next.
**Branch**: `feat/firewall-cp-migration` (Branch 2 — ownership reversal)
**Next branch**: Branch 3 (daemon consolidation — hostproxy + socketbridge under CP)

## Branch Sequence

| # | Name | Status |
|---|------|--------|
| 1 | CP as proper service — auth + gRPC, firewall still owns bootstrap | `complete` (merged to `main`) |
| 2 | Ownership reversal — CP owns firewall, `internal/firewall/` deleted, 13-method scope-corrected AdminService | `complete` (awaiting host-side review on `feat/firewall-cp-migration`) |
| 3 | Daemon consolidation — hostproxy + socketbridge under CP, Docker events replacing watcher polling | pending |
| 4 | clawkerd auth — PKCE registration, per-agent certs | pending |
| 5 | Init migration + agent lifecycle — clawkerd replaces init scripts, command channel | pending |
| 6 | Monitor + release + hardening — out of alpha | pending |

Each branch gets its own `/cspec` kickoff before implementation.

## Branch 2 Delivery Summary (all 8 tasks)

- **13-method scope-corrected AdminService** (Task 5) replacing B1's 7 short-named RPCs. All RPCs uniform `"admin"` scope (INV-B2-009).
- **Firewall ownership inverted**: `internal/firewall/` deleted (Task 6/8). `internal/controlplane/firewall/` owns handler, Stack, rules store, certs, Envoy+CoreDNS config, eBPF subsystem.
- **Host-side bootstrap** (Task 3): `controlplane.EnsureRunning`/`Stop`/`CPRunning` with mount-mode reconciliation + restart policy `on-failure` max 3 + static-IP attachment.
- **CP self-shutdown** (Task 4): `AgentWatcher` drives drain-to-zero callback (`CancelAllBypassTimers → GracefulStop → Stack.Stop → ebpf.FlushAll`) — INV-B2-007.
- **Defensive startup cleanup** (Task 4): `ebpf.CleanupStaleBypass` runs before `SetReady` — INV-B2-013.
- **Factory `f.AdminClient(ctx)` + `f.ControlPlane()`** (Task 6/7). `adminClientFunc` caches `grpc.ClientConn`, rebuilds only on `TransientFailure`/`Shutdown`, `ensureRunning` package-level seam for the CP bootstrap path.
- **Drift guard** (Task 5 — INV-B2-016): every `FirewallEnable` resolves `container_id → cgroup_path` via Docker; bypass dead-man timer goes through the same `resolveBypassCgroupID`.
- **Break-glass `clawker controlplane up/down/status`** (Task 7): new `controlplane.Manager` interface + moq, wired through Factory.
- **Task 8 cleanup**: deleted `cfg.FirewallPIDFilePath`/`FirewallLogFilePath` + constants; dropped dead `consts.PidsDir`; fixed harness failure-dump slice; rewrote `internal/controlplane/firewall/CLAUDE.md` to a full reference; updated ARCHITECTURE.md + KEY-CONCEPTS.md + rules files. User-facing docs (`docs/*.mdx`, `README.md`) intentionally NOT touched per user instruction — those get updated at release time.

## Quality Gates (Branch 2)

- `go build ./...`, `go vet ./...`, `go vet ./test/e2e/...`: green
- `make test`: 4625 tests pass, 7 skips (Windows/opt-in)
- E2E suite authored + committed in every task; agents do NOT execute (host-side review runs them)

## Key Process Notes
- Highway construction: old stays live until replacement proven
- Living roadmap: branch details decided at kickoff, not upfront
- No backward compat needed: eBPF never shipped in a release
- Alpha project: larger branches OK, no official releases during work
- HIGH intensity: security tool, trust boundaries, auth throughout
- User rejected TDD on this project (see `.correctless/learnings/tdd-phase-disabled.md`) — use integration + E2E + battle-tested mocks instead
- User-facing docs in `docs/` are not published yet; update at release time, not during branch work
