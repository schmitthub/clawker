# Verification: Branch 2 — CP Owns the Firewall (Ownership Reversal)

**Spec**: `.correctless/specs/cp-initiative/branch-2-cp-owns-firewall.md`
**Branch**: `feat/firewall-cp-migration`
**Intensity**: high
**Date**: 2026-04-14 (re-verification after blocker fixes in `fc253a6c`)
**Commits verified**: `9dbd3feb..fc253a6c` (11 commits against `main`)
**Tests**: `make test` — 4633 tests pass, 7 skipped (platform/opt-in), 0 failures.

## Re-verification Note

This is the second pass after the initial verification flagged 3 BLOCKING findings. Commit `fc253a6c fix(controlplane): close B2 verify blockers + relocate admin dial` addresses all three. This pass re-checks each prior blocker, retains the prior weak-finding status for items not closed, and re-evaluates drift.

## Rule Coverage

| Rule | Test | Status | Notes |
|------|------|--------|-------|
| INV-B2-001 | (structural — `stack.go`, `rules_store.go`, `certs.go` confine writes to `internal/controlplane/firewall`; no host-side writer to `FirewallDataSubdir()`) | **weak** | Spec calls for an AST `boundary_test.go` at module root mirroring `TestNoMobyImportOutsideWhail`. Not present. Compliance is by-inspection only — a maintainer adding a host-side `os.Create` against `FirewallDataSubdir()` would not be caught automatically. |
| INV-B2-002 | (structural — only `internal/controlplane/firewall/stack.go` creates `purpose=firewall` / `purpose=coredns` containers) | **weak** | Same as above. Spec calls for AST boundary test asserting no host-side `ContainerCreate/Start/Stop/Restart/Remove` targeting `envoyContainer`/`corednsContainer`. Not present. Grep confirms today's tree is clean (only `stack.go` matches) but has no regression guard. |
| INV-B2-003 | (structural — `internal/firewall/daemon.go` does not exist; no `EnsureDaemon`/`StopDaemon` symbols remain) | **covered** | Compiler enforces absence (deleted package cannot be imported). Grep for `writePIDFile.*firewall` and `firewall.Daemon` returns zero matches in any `.go` file. CI-style assertion satisfied structurally. |
| INV-B2-004 | (structural — `internal/firewall/` deleted; `go vet ./...` via `make test` passes) | **covered** | `internal/firewall/` is absent (`ls` returns "No such file or directory"). Grep for `"github.com/schmitthub/clawker/internal/firewall"` returns zero hits in any `.go` file. Deleted by commit `4a346f73`. |
| INV-B2-005 | `TestCacheableState`, `TestAdminClient_CachesOnSuccessiveCalls`, `TestAdminClient_RebuildsAfterShutdown`, `TestAdminClientKeepaliveParams`, `TestAdminClient_PassesKeepaliveToDial` (`internal/cmd/factory/default_test.go`) | **covered** | Closes prior BLOCKING finding. All four spec-mandated assertions present: (1) ensureRunning fires once across N cached calls; (2) rebuild fires when `grpc.ClientConn` enters Shutdown; (3) keepalive params (Time=30s, Timeout=10s, PermitWithoutStream=false) match CP server-side config; (4) Ready/Connecting/Idle treated as cacheable (table case in `TestCacheableState`). The closure uses two test seams (`ensureRunning` and `dialAdmin`) that tests swap with `t.Cleanup`-protected restoration. The dial helper itself was relocated from `internal/auth` into `internal/controlplane/adminclient` to keep `internal/auth` a primitives-only leaf — a clean architectural side-effect of closing this blocker. |
| INV-B2-006 | `TestEnsureRunning_HappyPath_CreatesContainer`, `TestEnsureRunning_AlreadyRunning_IsNoOp`, `TestEnsureRunning_ExistingStopped_StartsWithoutRecreate`, `TestEnsureRunning_HealthzTimeout_SurfacesError`, `TestEnsureRunning_ConcurrentCallers_SingleCreate`, `TestEnsureRunning_NameConflictRecovery_NoSecondCreate` | **covered** | Unit coverage for idempotency (happy path, already-running no-op, stopped-container-start-without-recreate), healthz timeout propagation, concurrent-caller serialization via package-level mutex, and name-conflict recovery. Mount-divergence reconciliation was retired — see spec §INV-B2-006 History for the rationale and the Docker Desktop `/var/run/docker.sock` false-positive that triggered removal. |
| INV-B2-007 | `TestAgentWatcher_DrainCallback`, `TestAgentWatcher_DoesNotDrainBeforeGrace`, `TestAgentWatcher_NonZeroCountResetsMissStreak`, `TestAgentWatcher_ListAgentsErrorResetsStreak`, `TestAgentWatcher_ListErrCeiling_SurfacesError`, `TestAgentWatcher_ContextCancellationReturnsError`, `TestAgentWatcher_RunTwice_ReturnsError`, `TestBuildCPContainerConfig_RestartPolicyOnFailure`, `TestCPSelfShutdown_E2E` | **covered** | All three spec strands present: (1) watcher-goroutine unit tests cover drain fire + grace suppression + miss-streak reset; (2) container-spec test asserts `RestartPolicyOnFailure` with `MaximumRetryCount=3`; (3) E2E test boots CP with real Docker, waits for drain, asserts `state.ExitCode==0` and `RestartCount==0`. Strict ordering (`CancelAllBypassTimers → GracefulStop → Stack.Stop → FlushAll`) is enforced in `cmd/clawker-cp/main.go` and indirectly verified by the E2E end state. |
| INV-B2-008 | `TestDownRun_ShortCircuitsWhenCPNotRunning`, `TestDownRun_StopsCPAndWarnsAboutFirewall`, `TestDownRun_PropagatesErrors`, `TestNewCmdDown_RunFReceivesOptions`, `TestControlPlaneCLI_UpStatusDown_E2E`, `TestControlPlaneCLI_DownOnAbsentCP_E2E` | **covered** | Unit and E2E. Stream-split contract (warning to stderr not stdout) explicitly asserted. |
| INV-B2-009 | `TestAdminMethodScopes_CoversAllRPCs`, `TestAuthInterceptor_UnmappedMethod_Denied`, `TestAuthInterceptor_ValidToken_CorrectScope_Allowed`, `TestAuthInterceptor_ValidToken_WrongScope_Denied`, `TestAuthInterceptor_NoToken_Denied`, `TestINV_B1_016_SeparateProtoPackages` ("AdminService has correct RPCs" subtest pins the 13 RPCs) | **covered** | Exhaustive: reflection over `AdminService_ServiceDesc` asserts every registered RPC has a scope entry and every scope entry maps to a real RPC; count match enforced. Fail-closed (unmapped method → `PermissionDenied`) verified. `api/admin/v1/admin.go` pins all 13 RPCs to `consts.ScopeAdmin`. |
| INV-B2-010 | (structural — hostproxy is the spec-amended carve-out; no other external reader exists) | **covered (with carve-out)** | Closes prior BLOCKING finding via spec amendment. Spec INV-B2-010 and PRH-B2-004 now explicitly carve out `internal/hostproxy/` as the single allowed direct reader of `egress-rules.yaml`, with rationale documented inline (leaf-package discipline; mirror types; flock + unknown-field tolerance). Grep confirms no other package reads `egress-rules.yaml` directly. The hostproxy read is documented in `internal/hostproxy/CLAUDE.md` as intentional. DRIFT-001 marked resolved in `.correctless/meta/drift-debt.json`. AST-based regression guard for "no NEW reader gets added" remains weak (see weak section). |
| INV-B2-011 | `TestContainerSpecs_FirewallDataMountsAreReadOnly` (subtests `envoy`, `coredns`) — `internal/controlplane/firewall/container_spec_test.go` | **covered** | Closes prior BLOCKING finding. The new test inspects every mount on the Envoy and CoreDNS container specs, identifies mounts rooted under `cfg.FirewallDataSubdir()` via `filepath.Rel`, and asserts `ReadOnly == true` on each. Includes a sanity guard that fails the test if the spec stops mounting firewall data altogether (≥2 firewall mounts on Envoy, ≥1 on CoreDNS) — prevents the invariant from being trivially satisfied by a future code reorganization. Unrelated mounts (`/sys/fs/bpf` on CoreDNS, which `dnsbpf` legitimately writes) are explicitly out of scope. |
| INV-B2-012 | `TestINV_B1_016_SeparateProtoPackages` (13-method surface implicitly lacks `cgroup_path`); `TestHandler_FirewallEnable_*` (handler resolves cgroup internally via `resolveForEnable`); grep confirms `cgroup_path` absent from `admin.proto` | **covered (weak on test approach)** | Implicit coverage: the admin_grpc test pins the 13-method surface, handler tests in `handler_test.go` drive `resolveForEnable` with a `ContainerResolver` injection. Spec calls for an explicit proto-reflection test asserting no `CgroupPath` field on `FirewallEnable/Disable/Bypass` request messages via reflect on the registered proto descriptors. That assertion is not written; compliance is currently by-grep. |
| INV-B2-013 | `TestCPStartupCleanup_E2E`, `ebpf.Manager.CleanupStaleBypass` invocation in `cmd/clawker-cp/main.go` (before `SetReady` and before serving) | **covered (weak)** | E2E seeds orphan bypass entry, restarts CP, asserts cleared. Ordering correct in main.go (cleanup → serve → SetReady). Missing: unit test per spec test approach — fake eBPF manager records the cleanup call and pre-populates stale state to verify idempotency in isolation from Docker. The ebpf-manager tests cover `CleanupStaleBypass` semantics but not its integration with startup ordering. |
| INV-B2-014 | `TestBuildCPContainerConfig_ClawkerNetAttachment` | **covered** | Asserts `cpCfg.NetworkName == consts.Network`. The `EnsureNetwork`-on-create path is exercised by the `EnsureRunning` table tests. Real-Docker delete-and-recover integration test is not present; not blocking. |
| INV-B2-016 | `TestHandler_FirewallEnable_DriftDetected_UsesFreshID`, `TestHandler_FirewallEnable_ContainerGone_FailedPrecondition`, `TestResolveBypassCgroupID_DriftDetected_UsesFresh`, `TestResolveBypassCgroupID_ContainerGone_FallsBack`, `TestResolveBypassCgroupID_EmptyContainerID_FallsBack`, `TestResolveBypassCgroupID_DockerAPIError_FallsBack`, `TestResolveBypassCgroupID_CgroupStatFails_FallsBack`, `TestBypassTimerFired_*`, `TestFirewall_BypassExpiry_E2E`, `TestFirewall_BypassOnGoneContainer_E2E`, `TestFirewall_FullEnrollBypassRestore_E2E` | **covered** | Exceptionally strong. Drift path, container-gone path, Docker-error path, empty-id path, cgroup-stat-error path all exercised at unit level + E2E. Shared helper (`drift.go::resolveBypassCgroupID`) is used by both Enable and the bypass dead-man timer — the regression path the spec highlights. |

### Summary: 11 covered (3 weak on test approach), 2 weak (no regression guard for INV-B2-001/002), 0 UNCOVERED

All prior 3 BLOCKING findings closed by `fc253a6c`.

## Prohibition Compliance

| Prohibition | Status | Evidence |
|-------------|--------|----------|
| PRH-B2-001: No `internal/firewall` imports | **PASS** | Package deleted; grep for `"github.com/schmitthub/clawker/internal/firewall"` returns zero hits in any Go file. Compile-time enforced. |
| PRH-B2-002: No host-side PID file for firewall | **PASS** | `FirewallPIDFilePath` accessor is deleted from `config.Config` interface. Zero Go-file references; only docs/serena memories mention it. |
| PRH-B2-003: No host-side Docker API calls for Envoy/CoreDNS | **PASS** | Grep for `envoyContainer`/`corednsContainer` returns only `internal/controlplane/firewall/stack.go`. No host-side code manages these containers. |
| PRH-B2-004: No direct reads of `egress-rules.yaml` outside CP (hostproxy carve-out) | **PASS** | Spec amended to allow `internal/hostproxy/` as the single carve-out. The hostproxy read is documented as intentional in `internal/hostproxy/CLAUDE.md`. No other package reads `egress-rules.yaml` directly. DRIFT-001 marked resolved. |
| PRH-B2-005: No synchronous daemon pattern in new code | **PASS** | `Setsid: true` appears only in `internal/socketbridge/manager.go` and `internal/hostproxy/manager.go` — pre-existing daemons, not new B2 code. `writePIDFile` does not appear in any B2-added file. |
| PRH-B2-006: No cross-reference between auth CA and MITM CA | **PASS** | Auth material path (`auth/{ca,cli,tls}`) and MITM path (`firewall/certs`) remain disjoint trees; handler types are separate (`internal/auth` vs `internal/controlplane/firewall/certs.go`); rotation entry points are separate (`clawker auth rotate` vs `FirewallRotateCA` RPC). |
| PRH-B2-007: No `cgroup_path` on proto; no CLI-side cgroup computation | **PASS** | (a) `grep "cgroup_path" api/admin/v1/admin.proto` returns only a comment explaining the removal. (b) `CgroupPath` field does not appear on any B2 request message in `admin.pb.go`. (c) `DetectCgroupDriver` and `EBPFCgroupPath` identifiers appear only in `cmd/clawker-cp/main.go` + `internal/controlplane/firewall/cgroup{.go,_test.go}`. |

## Dependencies

No `go.mod` changes between `main` and `feat/firewall-cp-migration`. All B2 transport (gRPC, proto, whail/docker helpers) was added in B1. B2 is a pure internal restructure plus the `fc253a6c` follow-up.

## Architecture Compliance

- **PAT-001 (Factory DI)**: `f.AdminClient` + `f.ControlPlane` introduced as proper lazy Factory nouns. `f.Firewall` is deleted from `cmdutil.Factory`. CLI commands under `internal/cmd/firewall/` and `internal/cmd/controlplane/` use `NewCmd(f, runF)` + Options-struct closures — pattern is consistent with existing nouns (HostProxy, GitManager, etc.).
- **PAT-004 (Firewall Stack)**: Ownership inverted cleanly. CP now owns Envoy + CoreDNS lifecycle via DooD; the 13 RPCs expose the surface; `AgentWatcher` provides the drain-to-zero path and removes the need for a host-side PID daemon. `.correctless/ARCHITECTURE.md` PAT-004 still describes pre-B2 reality (DRIFT-002, open).
- **Package boundaries**: `internal/controlplane/firewall/` is a properly scoped sub-domain. `handler.go` / `stack.go` / `cgroup.go` / `drift.go` / `network.go` split is clean. `ebpf/` moved inside the firewall domain. Mocks regenerated with `moq`. The new `internal/controlplane/adminclient/` subpackage (introduced by `fc253a6c`) keeps `internal/auth` a primitives-only leaf — no `api/admin/v1` import in `auth`.
- **Error handling**: gRPC errors use `status.Errorf(codes.*)` consistently. Internal errors wrap via `fmt.Errorf("...: %w", err)`. No `logger.Fatal` in Cobra hooks. `panic` appears only on constructor preconditions (`NewHandler` non-nil checks) and moq-generated mock noise.
- **Logging**: zerolog for file-side logging only. User output via `fmt.Fprintf(ios.Out / ios.ErrOut, ...)` + `ColorScheme()` semantic icons.
- **Config access**: All ports/paths resolved via `cfg.Settings().ControlPlane` or interface methods (`cfg.FirewallDataSubdir()`, etc.). No hardcoded paths in handler/stack code.
- **Mock regeneration**: `ControlPlaneServiceMock`, `ManagerMock`, `IntrospectorMock`, `AdminServiceClientMock`, `EBPFManagerMock` are all moq-generated and DO-NOT-EDIT-headered. `FirewallManagerMock` deleted with its interface — no stale mock lingers.

## QA Class Fixes Verified

No `qa-findings-*.json` file exists for this task slug. Workflow state shows `qa_rounds: 0`. This branch was implemented without /ctdd gating per the project's documented disablement of TDD-enforced phases (see `.correctless/learnings/tdd-phase-disabled.md`). No QA class fixes to verify.

## Smells

- No TODO/FIXME/HACK comments added by B2 (diff-filtered check).
- `panic` calls in B2 code are limited to: `NewHandler` constructor preconditions (non-nil EBPF/Resolver — appropriate for programmer-error checks), break-glass `cmd/main.go` exit paths (correct for a CLI binary), and moq-generated mock files (auto-generated, not authored).
- The B1 verification report previously flagged "mTLS" wording in a B2 spec context block where INV-B1-001 dropped mTLS. The implementation is correct (mTLS IS required: `tls.RequireAndVerifyClientCert` in `grpc_mtls_test.go`); the inconsistency is between B1 and B2 spec text, not between spec and code. Not blocking.

## Drift

1. **DRIFT-001 (resolved)**: hostproxy reads `egress-rules.yaml` directly. Closed by spec amendment (INV-B2-010 + PRH-B2-004 now carve out `internal/hostproxy/`). Marked `status: "resolved"` in `.correctless/meta/drift-debt.json` with full resolution notes.
2. **DRIFT-002 (still open)**: `.correctless/ARCHITECTURE.md` PAT-004 still describes the pre-B2 world ("Control plane owns ebpf.Manager.Load() lifetime" with no mention of `Stack`, `AgentWatcher`, the 13-RPC surface, `f.AdminClient`, `f.ControlPlane`, or the deleted `internal/firewall/CLAUDE.md` reference). `/cupdate-arch` should fix.
3. **DRIFT-003 (still open)**: `.correctless/AGENT_CONTEXT.md` Key Components table still lists `internal/firewall/` as a live component. Code confirms the package is deleted. Entry must be updated to `internal/controlplane/firewall/` (and the related eBPF entry from `internal/controlplane/ebpf/` to `internal/controlplane/firewall/ebpf/`).

No new drift uncovered in this re-verification pass.

## Spec Updates

`fc253a6c` amended the spec to add the hostproxy carve-out in INV-B2-010 (~6 lines) and PRH-B2-004 (~2 lines). Both edits are scoped, localized, and traceable in the commit. No other spec changes.

The workflow state file still shows `phase: spec` with `qa_rounds: 0` — TDD is disabled for this project (see learnings/tdd-phase-disabled.md). Verification proceeds because the implementation is complete, committed, and tests pass.

## Overall: PASS with 0 BLOCKING findings, 5 weak items, 2 open drift items

### BLOCKING (none)

All three prior blockers closed by `fc253a6c`:
- INV-B2-005 — five new tests in `internal/cmd/factory/default_test.go` cover all four spec-mandated assertions.
- INV-B2-011 — new `container_spec_test.go` asserts ReadOnly on every Envoy + CoreDNS mount rooted under `FirewallDataSubdir()`.
- INV-B2-010 + PRH-B2-004 — spec amended to carve out `internal/hostproxy/`. DRIFT-001 marked resolved.

### Weak (non-blocking, should be strengthened)

- **INV-B2-001 / INV-B2-002**: No AST `boundary_test.go` at module root. Spec explicitly names the mirror pattern (`TestNoMobyImportOutsideWhail`). The invariants are satisfied today but have no regression guard. Recommendation: add a single `boundary_test.go` covering both invariants in one go/packages walk.
- **INV-B2-006**: Mount-divergence reconciliation retired (see spec §INV-B2-006 History). No weakness remaining — the guard was deleted because it false-positived on Docker Desktop's socket-proxy path and defended nothing the host threat model cares about.
- **INV-B2-012**: Proto-reflection assertion that `CgroupPath` is not a field on the three request types is missing. Compliance is currently by-grep + by-handler-test.
- **INV-B2-013**: Unit-level startup-orchestrator test (fake eBPF manager with pre-populated stale state, assert cleanup runs before `SetReady`) is missing. E2E covers the end-to-end behavior.

### Open Drift (handle before merge)

- DRIFT-002: `.correctless/ARCHITECTURE.md` PAT-004 update — run `/cupdate-arch`.
- DRIFT-003: `.correctless/AGENT_CONTEXT.md` Key Components table update — swap `internal/firewall/` → `internal/controlplane/firewall/` + `internal/controlplane/ebpf/` → `internal/controlplane/firewall/ebpf/`.

### Recommended actions

1. Run `/cdocs` (and/or `/cupdate-arch`) to clear DRIFT-002 + DRIFT-003 — both are doc-only and unblock merge.
2. Optional follow-up: add `boundary_test.go` at module root to strengthen INV-B2-001/002 guards. Single PR, ~50 LoC.
3. Optional follow-up: add proto-reflection test for INV-B2-012 (`reflect.TypeOf(&adminv1.FirewallEnableRequest{}).Elem()` has no field named `CgroupPath`).
4. Optional follow-up: extract unit-level startup orchestrator test for INV-B2-013 from the existing E2E by injecting a fake eBPF manager.
