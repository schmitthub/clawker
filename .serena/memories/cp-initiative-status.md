# Control Plane Initiative — Current Status

## Status: Branch 4 agent work complete. Pending final host-side review + the small CLI command-layer wiring follow-up.

**Workflow phase**: B4 agent work done; user's host-side E2E sweep + merge next.
**Branch**: `feat/clawkerd-init` (Branch 4 — clawkerd auth + agent registration)
**Next branch**: Branch 5 (init migration — clawkerd replaces entrypoint init steps; AgentCommandService wakes up)

## Branch Sequence

| # | Name | Status |
|---|------|--------|
| 1 | CP as proper service — auth + gRPC, firewall still owns bootstrap | `complete` (merged to `main`) |
| 2 | Ownership reversal — CP owns firewall, `internal/firewall/` deleted, 13-method scope-corrected AdminService | `complete` (awaiting host-side review on `feat/firewall-cp-migration`) |
| 3 | Daemon consolidation — hostproxy + socketbridge under CP, Docker events replacing watcher polling | `complete` |
| 4 | clawkerd auth — PKCE registration, per-agent certs | `complete` (awaiting CLI wiring follow-up + host-side review on `feat/clawkerd-init`) |
| 5 | Init migration + agent lifecycle — clawkerd replaces init scripts, command channel | pending |
| 6 | Monitor + release + hardening — out of alpha | pending |

Each branch gets its own `/cspec` kickoff before implementation.

## Branch 4 Delivery Summary (14 tasks)

- **Proto contracts** (Task 1): `AgentService.Register` on `api/agent/v1`, `AdminService.AnnounceAgent` on `api/admin/v1`, POC `internal/clawkerd/protocol/v1` deleted.
- **Consts + scope wiring** (Task 2): `ClientIDAgent`, `ScopeAgentSelfRegister`, `AgentSlotTTL`, `BootstrapDir` + 5 file names, 3 `EnvClawkerd*` env names. `ControlPlaneSettings.AgentAPIPort` → `AgentPort`.
- **CLI agent cert minting** (Task 3): `auth.MintAgentCert` → `AgentCert{CertPEM, KeyPEM, ThumbprintHex}` signed by CLI CA, 24h lifetime.
- **Hydra agent client registration** (Task 4): `RegisterAgentClient` (idempotent on 409); CLI + agent clients share the JWK with distinct client_id + scope. `cmd/clawker-cp` Step 5 registers both.
- **Slot registry** (Task 5): `agentslots.Registry` with constant-time PKCE compare (verifier hashed BEFORE slot lookup to defeat name-enumeration timing leak), wrong-verifier preserves slot for benign retry, sync.Once Stop, default sweep period = `AgentSlotTTL/2`.
- **Agent registry + dockerevents** (Task 6): `agentregistry.Registry` keyed by SHA-256 over peer cert DER. `Subscribe(ctx, reg, informer)` evicts on `DeltaRemoved` or `DeltaUpdated` with `Lifecycle == LifecycleStopped`. Lifecycle constants extracted from dispatch.go to feeder.go.
- **CLI bootstrap helpers** (Task 7): `auth.BuildAgentAssertion`, `shared.GenerateAgentBootstrap` / `AnnounceAgent` / `WriteAgentBootstrapToContainer`. Bootstrap goes to the container's writable layer (Docker can't pre-populate tmpfs mounts; tmpfs shadows the underlying directory at start). 0700/0400 root-only perms.
- **CP agent listener** (Task 8): second `grpc.Server` on `cp.AgentPort`, clawker-net only (NOT host-published), shares the admin TLS material. Both servers join the graceful-shutdown WaitGroup.
- **AgentService handler** (Task 9): `agent.Handler.Register` enforces five identity-binding checks (PKCE consume + cert thumbprint vs slot + Docker peer-IP cross-check + label vs canonical agent name + slot atomic). Every failure returns one generic `codes.PermissionDenied`.
- **AdminService.ListAgents** (Task 9b): explicit method on `adminServer` (not method-promoted; would conflict with `UnimplementedAdminServiceServer`). `clawker controlplane agents` CLI verb with `--format`/`--json` support.
- **Per-listener AuthInterceptor** (Task 10): `AgentMethodScopes()` constructed alongside `AdminMethodScopes()`; both interceptors share the Hydra introspector but enforce distinct method-scope vocabularies. `TestAgentMethodScopes_CoversAllRPCs` walks the proto descriptor.
- **clawkerd binary** (Task 11): `cmd/clawkerd` reads bootstrap, exchanges assertion → access token, mTLS-dials CP, Register, idles on `<-ctx.Done()`. Verifier deleted on Register success; cert/key/CA/assertion stay on disk for any future redial. ~250 lines including comments.
- **Bundler + entrypoint** (Task 12): `internal/clawkerd/embed.go` → `Binary []byte`. Pure-Go cross-compile in Makefile (no Dockerfile.controlplane stage needed). Per-project Dockerfile gains `COPY clawkerd /usr/local/bin/clawkerd`. Entrypoint launches clawkerd in the background as root before the firewall healthz wait, gated on `[ -d /run/clawker/bootstrap ]`.
- **E2E tests** (Task 13): authored `clawkerd_register_test.go` (happy path) + `clawkerd_failures_test.go` (seven adversarial cases, most skipped pending an mTLS-dial helper in `test/e2e/harness/`).
- **Documentation** (Task 14): KEY-CONCEPTS, package CLAUDE.md files for agent/agentslots/agentregistry/clawkerd, plan memory updated, this status memo updated.

## Known follow-up before merge

- The CLI command-layer wiring that calls `shared.GenerateAgentBootstrap` / `AnnounceAgent` / `WriteAgentBootstrapToContainer` from `run`/`start` and propagates the three `CLAWKERD_*` env vars is NOT yet in `feat/clawkerd-init`. Building blocks landed in Tasks 7 + 9; call sites are mechanical but were out of B4 budget. Until then, every per-project container starts without `/run/clawker/bootstrap`, the entrypoint's gate skips clawkerd launch, and `ListAgents` reports empty. Documented in the Branch 4 plan memory (Task 12 + 13 sections).
- `test/e2e/harness/` needs an mTLS-dial helper for the adversarial Register tests to actually run. Six of seven adversarial cases skip with explicit "needs harness mTLS-dial helper" messages.

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
