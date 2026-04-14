# Control Plane Initiative — Current Status

## Status: Branch 1 LANDED on main (PR #250, merged 2026-04-13)

**Shipped**: Containerized `clawker-controlplane` service with Ory auth stack (Hydra + Oathkeeper + Kratos), `AdminService` gRPC over mTLS TCP with OAuth2 JWT, in-process eBPF `Manager.Load()` lifetime, aggregate `/healthz` endpoint.

**Current branch**: `chore/docs-update` (docs catch-up after CP landed)

## Branch Sequence
1. [x] **CP as proper service** — auth + gRPC, firewall still owns bootstrap. **MERGED**
2. [ ] Ownership reversal — CP owns firewall, Manager becomes thin client, daemon sunset
3. [ ] Daemon consolidation — hostproxy + socketbridge under CP, Docker events
4. [ ] clawkerd auth — PKCE registration, per-agent certs (proto reserved at `internal/clawkerd/protocol/v1/`)
5. [ ] Init migration + agent lifecycle — clawkerd replaces init scripts, command channel
6. [ ] Monitor + release + hardening — out of alpha

Each branch gets its own `/cspec` kickoff.

## What landed in Branch 1
- `internal/controlplane/` — `Server`, `Registry`, `AdminHandler`, `AuthInterceptor`, `HydraIntrospector`, `CPStartupOrchestrator`, `SubprocessManager`, `BuildCPContainerConfig`, `WriteOryConfigs`, `RegisterCLIClient`
- `internal/controlplane/ebpf/` — moved from `internal/ebpf/`; owns `Manager.Load()` lifetime
- `internal/auth/` — CLI-side auth material + `DialCPAdmin()` + ES256 assertion
- `api/admin/v1/` — AdminService protobuf (CLI → CP)
- `internal/clawkerd/protocol/v1/` — reserved for future agent↔CP
- `cmd/clawker-cp/` — daemon entrypoint, built by `Dockerfile.controlplane`
- `internal/consts/` — cross-package constants

## Next: Branch 2 kickoff

Ownership reversal — CP owns firewall bootstrap; `firewall.Manager` becomes thin client over gRPC. Daemon loop sunsets. Start with `/cspec`.

## Key Process Notes
- Highway construction: old stays live until replacement proven
- Living roadmap: branch details decided at kickoff, not upfront
- No backward compat needed: eBPF never shipped in a release
- Alpha project: larger branches OK, no official releases during work
- HIGH intensity: security tool, trust boundaries, auth throughout
