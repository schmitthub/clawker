# Control Plane Initiative — Current Status

## Status: Branch 4 + end-to-end CLI integration follow-up COMPLETE. Ready for review.

**Workflow phase**: B4 agent work + B4 follow-up implementation both done. Tree-wide build/vet/`make test` green; pre-commit clean.
**Branch**: `feat/clawkerd-init` (Branch 4 — clawkerd auth + agent registration; B4 follow-up integrated)
**Next branch**: Branch 5 (init migration — clawkerd replaces entrypoint init scripts; first concrete `Command` payload variants land)

## Branch Sequence

| # | Name | Status |
|---|------|--------|
| 1 | CP as proper service — auth + gRPC, firewall still owns bootstrap | `complete` (merged to `main`) |
| 2 | Ownership reversal — CP owns firewall, `internal/firewall/` deleted, 13-method scope-corrected AdminService | `complete` (awaiting host-side review on `feat/firewall-cp-migration`) |
| 3 | Daemon consolidation — hostproxy + socketbridge under CP, Docker events replacing watcher polling | `complete` |
| 4 | clawkerd auth — PKCE registration, per-agent certs | `complete` (core + end-to-end CLI integration follow-up: Connect-as-stream, identity interceptor, slot composite key, dockerevents-driven slot eviction, AnnounceAgent handler, run/start bootstrap delivery) |
| 5 | Init migration + agent lifecycle — clawkerd replaces init scripts, first `Command` payload variants on the open Connect stream | pending |
| 6 | Monitor + release + hardening — out of alpha | pending |

## Active follow-ups (out-of-band of branch sequence)

| Initiative | Status | Memory |
|------------|--------|--------|
| **CP restart resilience** (registry persistence, reconnect path, clawkerd reconnect-with-backoff, `volume prune` safety, `controlplane down` safety, streaming RPC eviction broadcast) | Tracked, not scheduled. Prerequisite for production-readiness. Now the only known gap on the agent registration story | `cp-initiative-cp-restart-resilience` |

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

## Branch 4 follow-up — DELIVERED

Implementation lived in `cp-initiative-clawkerd-cli-integration` (9 tasks). All complete; full key-learnings list in that memory. Highlights:

- **`AgentService.Register` → `AgentService.Connect`** (server-streaming). The connection IS the agent's lifetime command channel. First message after auth is `Welcome` (carries `ClawkerdConfiguration` placeholder, B5 fills in). Stub `AgentService.Events` (client-streaming) for B5 telemetry.
- **Single-server topology**: clawkerd is gRPC client only. Bearer attached via `PerRPCCredentials` (covers both unary AND streaming RPCs — a unary-only interceptor would silently skip Connect).
- **Composite slot key** (thumbprint + agent_name) folds the agent_name cross-check into slot lookup. Solves retry-within-TTL collisions.
- **`agentslots.EvictByContainerID` + `Subscribe`** mirror the existing `agentregistry` pattern. Slot eviction is real-time on container death, not just TTL.
- **`AgentIdentityInterceptor`** (unary + stream) chained AFTER `AuthInterceptor` on the agent listener. Resolves cert thumbprint → registry entry → ctx-attached `*agentregistry.Entry`. Connect is opted out (authenticates itself). The stream wrapper's `Context()` MUST be on the wrapper not promoted from embedded `grpc.ServerStream` — promotion silently breaks identity binding for streaming RPCs.
- **CN cross-check** at Connect (`peerCert.Subject.CommonName == req.AgentName`, constant-time, BEFORE slot consume so a CN mismatch can't burn a slot).
- **Welcome BEFORE registry.Add** — receipt of Welcome by clawkerd is the auth-success signal that authorizes verifier deletion. Send-failure leaves no orphan registry entry; failure path is `codes.Unavailable`, not bare `fmt.Errorf` (would surface as `codes.Unknown`).
- **Bootstrap delivery is UNCONDITIONAL** (NOT gated on `security.firewall.enable`). CP ≠ firewall. Loop containers are also new-container paths and got the AgentName plumbing.
- **`ConnectRequest.code_verifier`** preserves the empty-verifier seam for the future reconnect flow; today's handler still requires it on first-connect.
- **`narrow peerIdentity{Raw, CommonName}`** projection (instead of returning `*x509.Certificate`) encodes the trust policy at compile time — the handler MUST source identity decisions only from these two fields.
- **`Reserve` panics on zero thumbprint / empty Challenge** — programming-error invariants surface at first-call instead of as silent identity-binding gaps.

## Other known follow-ups before merge

- `test/e2e/harness/` needs an mTLS-dial helper for the adversarial Register tests to actually run. Six of seven adversarial cases skip with explicit "needs harness mTLS-dial helper" messages. Now ALSO needs adapting from Register (unary) to Connect (server-streaming) once the helper lands.

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
