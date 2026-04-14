# Verification: Branch 2 — CP Owns the Firewall (Ownership Reversal)

**Spec**: `.correctless/specs/cp-initiative/branch-2-cp-owns-firewall.md`
**Branch**: `feat/firewall-cp-migration`
**Intensity**: high
**Date**: 2026-04-14
**Commits verified**: `9dbd3feb..4a346f73` (10 commits against `main`)
**Tests**: `make test` — 4625 tests pass, 7 skipped (platform/opt-in), 0 failures.

## Rule Coverage

| Rule | Test | Status | Notes |
|------|------|--------|-------|
| INV-B2-001 | (structural — `stack.go`, `rules_store.go` scope their writes to `internal/controlplane/firewall`; no host-side writer to `FirewallDataSubdir()`) | **weak** | Spec explicitly calls for an AST `boundary_test.go` at module root (mirror of `TestNoMobyImportOutsideWhail`). No such file exists. Compliance is by-inspection only; the next maintainer adding a host-side Write call would not be caught automatically. |
| INV-B2-002 | (structural — only `internal/controlplane/firewall/stack.go` creates `purpose=firewall` / `purpose=coredns` containers) | **weak** | Same as above — spec calls for AST boundary test asserting no host-side `ContainerCreate/Start/Stop/Restart/Remove` targeting `envoyContainer`/`corednsContainer`. Not present. Grep confirms today's tree is clean (only `stack.go` matches) but has no regression guard. |
| INV-B2-003 | (structural — `ls internal/firewall/daemon.go` returns "no such file"; no `EnsureDaemon`/`StopDaemon` symbols remain) | **covered** | Compiler enforces absence (deleted package cannot be imported). Grep for `writePIDFile.*firewall` and `firewall.Daemon` returns zero matches in Go files. Spec's CI-style assertion is satisfied structurally. |
| INV-B2-004 | (structural — `internal/firewall/` deleted; `go vet ./...` via `make test` passes) | **covered** | `internal/firewall/` is absent; grep for `"github.com/schmitthub/clawker/internal/firewall"` returns zero hits in any `.go` file. Deleted by commit `4a346f73`. |
| INV-B2-005 | (NONE found) | **UNCOVERED** | Spec mandates four specific tests for `f.AdminClient` lazy closure: (1) mock `ensureRunning` counter — first call triggers, subsequent cached; (2) rebuild on `Shutdown`/`TransientFailure`; (3) keepalive dial options present; (4) `Ready`/`Connecting`/`Idle` all cacheable. No `default_test.go` test exists beyond `TestNew`. The logic is in `adminClientFunc` (`internal/cmd/factory/default.go:204-258`) but none of the four contract assertions are testable. |
| INV-B2-006 | `TestEnsureRunning_HappyPath_CreatesContainer`, `TestEnsureRunning_AlreadyRunning_IsNoOp`, `TestEnsureRunning_ExistingStopped_StartsWithoutRecreate`, `TestEnsureRunning_MountDivergence_RecreatesContainer` (4 table-case table), `TestEnsureRunning_HealthzTimeout_SurfacesError`, `TestEnsureRunning_ConcurrentCallers_SingleCreate`, `TestEnsureRunning_NameConflictRecovery_NoSecondCreate` | **covered** | Strong coverage. Explicit table exercises RO/RW divergence, source-path drift, missing mount, extra mount. Concurrent-goroutine test proves mutex serialization. Missing: real-Docker integration for the RO→RW upgrade path (spec explicitly calls for "integration (real Docker idempotency + upgrade path from a pre-staged RO-mount container)") — the unit test covers it, but the end-to-end assertion is absent. |
| INV-B2-007 | `TestAgentWatcher_DrainCallback`, `TestAgentWatcher_DoesNotDrainBeforeGrace`, `TestAgentWatcher_NonZeroCountResetsMissStreak`, `TestAgentWatcher_ListAgentsErrorResetsStreak`, `TestAgentWatcher_ListErrCeiling_SurfacesError`, `TestAgentWatcher_ContextCancellationReturnsError`, `TestAgentWatcher_RunTwice_ReturnsError`, `TestBuildCPContainerConfig_RestartPolicyOnFailure`, `TestCPSelfShutdown_E2E` | **covered** | Spec calls for three strands and all three are present: (1) watcher-goroutine unit tests cover drain fire + grace suppression + miss-streak reset; (2) container-spec test asserts `RestartPolicyOnFailure` with `MaximumRetryCount=3`; (3) E2E test boots CP with real Docker, waits 3min for drain, and asserts `state.ExitCode==0` and `RestartCount==0`. Strict ordering (`CancelAllBypassTimers → GracefulStop → Stack.Stop → FlushAll`) is enforced in `cmd/clawker-cp/main.go:364-389` but not unit-asserted; the E2E indirectly proves end state. |
| INV-B2-008 | `TestDownRun_ShortCircuitsWhenCPNotRunning`, `TestDownRun_StopsCPAndWarnsAboutFirewall`, `TestDownRun_PropagatesErrors`, `TestNewCmdDown_RunFReceivesOptions`, `TestControlPlaneCLI_UpStatusDown_E2E`, `TestControlPlaneCLI_DownOnAbsentCP_E2E` | **covered** | Unit and E2E. Stream-split contract ("warning to stderr not stdout") asserted explicitly. |
| INV-B2-009 | `TestAdminMethodScopes_CoversAllRPCs`, `TestAuthInterceptor_UnmappedMethod_Denied`, `TestAuthInterceptor_ValidToken_CorrectScope_Allowed`, `TestAuthInterceptor_ValidToken_WrongScope_Denied`, `TestAuthInterceptor_NoToken_Denied`, `TestINV_B1_016_SeparateProtoPackages` ("AdminService has correct RPCs" subtest pins 13 RPCs) | **covered** | Exhaustive: reflection over `AdminService_ServiceDesc` asserts every registered RPC has a scope entry and every scope entry maps to a real RPC; count match enforced. Fail-closed (unmapped method → `PermissionDenied`) verified. `api/admin/v1/admin.go:21-38` pins all 13 RPCs to `consts.ScopeAdmin`. |
| INV-B2-010 | (NONE found) | **UNCOVERED** (see Drift item 1) | Spec demands AST test asserting every `EgressRulesFile` / `EgressRulesFileName` reference comes from `internal/controlplane/...`, `internal/config/...`, or test files. No such test. Worse, `internal/hostproxy/egress_check.go:31-36` and `daemon.go:103` read `egress-rules.yaml` directly via `os.ReadFile` + `yaml.Unmarshal` against mirror types — documented as intentional in `internal/hostproxy/CLAUDE.md` ("leaf package; mirror types for EgressRulesFile are intentional") but in direct contradiction with the spec. |
| INV-B2-011 | (NONE found) | **UNCOVERED** | Spec mandates `container_config_test.go` assertion `mount.ReadOnly == true` on Envoy + CoreDNS specs for firewall data paths. No test covers this. The code in `internal/controlplane/firewall/stack.go:418,424,450` sets RO correctly, but there is no regression guard — a maintainer flipping RO→RW would not be caught. |
| INV-B2-012 | `TestINV_B1_016_SeparateProtoPackages` (13-method surface implicitly lacks `cgroup_path`); `TestHandler_FirewallEnable_*` (handler resolves cgroup internally via `resolveForEnable`); grep confirms `cgroup_path` absent from `admin.proto` | **covered** (weak on spec test approach) | Implicit coverage: the admin_grpc test pins the 13-method surface, handler tests in `handler_test.go` drive `resolveForEnable` with a `ContainerResolver` injection. Missing: spec explicitly calls for a proto-reflection test asserting no `cgroup_path` field on `FirewallEnable/Disable/Bypass` request messages via reflect on the registered proto descriptors. That assertion is not written; compliance is currently by-grep. |
| INV-B2-013 | `TestCPStartupCleanup_E2E`, `ebpf.Manager.CleanupStaleBypass` in `cmd/clawker-cp/main.go:273-279` (step 7b, before step 8 gRPC serve and step 9 `SetReady()`) | **covered** (weak) | E2E seeds orphan bypass entry, restarts CP, asserts it's cleared. Ordering correct in main.go (cleanup → serve → SetReady). Missing: unit test per the spec's test approach — "startup orchestrator test: assert cleanup runs before `SetReady`; fake eBPF manager records the cleanup call and pre-populates stale state to verify idempotency and effect". The unit-test counterpart is not present. The ebpf-manager tests cover `CleanupStaleBypass` semantics (`TestDeleteExpiredDNSEntries_*`, `TestClearBypass_*`) but not the integration with startup ordering. |
| INV-B2-014 | `TestBuildCPContainerConfig_ClawkerNetAttachment` | **covered** | Asserts `cpCfg.NetworkName == consts.Network`. Does not assert `ContainerCreateOptions.EnsureNetwork` is set on the create call. Per spec: "integration (delete `clawker-net` externally, run `controlplane up`, assert network recreated and CP attached)" — not present. |
| INV-B2-016 | `TestHandler_FirewallEnable_DriftDetected_UsesFreshID`, `TestHandler_FirewallEnable_ContainerGone_FailedPrecondition`, `TestResolveBypassCgroupID_DriftDetected_UsesFresh`, `TestResolveBypassCgroupID_ContainerGone_FallsBack`, `TestResolveBypassCgroupID_EmptyContainerID_FallsBack`, `TestResolveBypassCgroupID_DockerAPIError_FallsBack`, `TestResolveBypassCgroupID_CgroupStatFails_FallsBack`, `TestBypassTimerFired_*`, `TestFirewall_BypassExpiry_E2E`, `TestFirewall_BypassOnGoneContainer_E2E`, `TestFirewall_FullEnrollBypassRestore_E2E` | **covered** | Exceptionally strong. Unit and E2E. Drift path, container-gone path, Docker-error path, empty-id path, cgroup-stat-error path all exercised. Shared helper (`drift.go::resolveBypassCgroupID`) is used by both Enable and the bypass dead-man timer — the regression path the spec highlights. |

### Summary: 7 covered, 4 weak, 4 UNCOVERED

**UNCOVERED (BLOCKING):**
- **INV-B2-005**: `f.AdminClient` has zero unit tests for its lazy/cache/rebuild/keepalive contract. The spec test approach enumerates four specific assertions — none exist. `internal/cmd/factory/default_test.go` has only `TestNew`. The closure contains critical production logic (connection-state-driven rebuild; never rebuilding for `Ready/Connecting/Idle`; keepalive params; single-flight bootstrap under a mutex). A regression that, e.g., rebuilds every call would pass the build, destabilize long-lived commands (`loop`, `bypass dashboard`, `monitor up`), and ship undetected.
- **INV-B2-010**: No AST test covers the rules-store read boundary, AND `internal/hostproxy` reads `egress-rules.yaml` directly via `os.ReadFile` + `yaml.Unmarshal` (mirror `egressRule`/`egressRulesFile` types). This is a live **spec violation**, not just a coverage gap.
- **INV-B2-011**: Envoy + CoreDNS RO-mount assertion is missing from `container_config_test.go`-style unit tests. The code is correct today; there is no guard.
- **(weak→raised)** — none; see Weak section for items that are covered enough to pass but short of spec intent.

**Weak (non-blocking, should be strengthened):**
- **INV-B2-001 / INV-B2-002**: Both explicitly call for an AST `boundary_test.go` at module root (the spec even names the mirror pattern: `TestNoMobyImportOutsideWhail`). Neither boundary test file exists. The invariants are structurally satisfied today but have no regression guard.
- **INV-B2-006**: Unit coverage is excellent (7 tests, table-driven); the integration path for the RO→RW mount-mode upgrade from a pre-staged B1 container is not exercised against real Docker.
- **INV-B2-012**: Compliance is by-grep + test of the handler's internal `resolveForEnable`; the proto-reflection test that asserts `CgroupPath` is NOT a field on the three request types does not exist.
- **INV-B2-013**: E2E covers the end-to-end cleanup; the unit-level startup-orchestrator test (fake eBPF manager with pre-populated stale state, assert cleanup runs before `SetReady`) is missing.
- **INV-B2-014**: Container-config test pins `NetworkName`; the `EnsureNetwork` + external-delete-and-recover integration test is missing.

## Prohibition Compliance

| Prohibition | Status | Evidence |
|-------------|--------|----------|
| PRH-B2-001: No `internal/firewall` imports | **PASS** | Package deleted; grep for `"github.com/schmitthub/clawker/internal/firewall"` returns zero hits in Go files. Compile-time enforced. |
| PRH-B2-002: No host-side PID file for firewall | **PASS** | `FirewallPIDFilePath` accessor is deleted from `config.Config` interface (per `internal/config/consts.go` / `config.go` today). No Go-file references; only a serena memory + the spec itself mention it. |
| PRH-B2-003: No host-side Docker API calls for Envoy/CoreDNS | **PASS** | Grep for `envoyContainer`/`corednsContainer` returns only `internal/controlplane/firewall/stack.go`. No host-side code manages these containers. |
| PRH-B2-004: No direct reads of `egress-rules.yaml` outside CP | **FAIL** | `internal/hostproxy/egress_check.go` + `daemon.go:103` read `egress-rules.yaml` directly via `os.ReadFile` + `yaml.Unmarshal`. This is documented as an **intentional** design decision in `internal/hostproxy/CLAUDE.md` ("Leaf package: does NOT import `internal/controlplane/firewall` or `internal/storage`... Mirror types for `EgressRulesFile`/`EgressRule`/`PathRule` are intentional copies"). The spec does not carve out an exception for hostproxy. Either the spec must add the exception or hostproxy must switch to an AdminService RPC. See Drift #1. |
| PRH-B2-005: No synchronous daemon pattern in new code | **PASS** | `Setsid: true` appears only in `internal/socketbridge/manager.go` and `internal/hostproxy/manager.go` — pre-existing daemons, not new code. `writePIDFile` does not appear in any B2-added file. |
| PRH-B2-006: No cross-reference between auth CA and MITM CA | **PASS** | Auth material path (`auth/{ca,cli,tls}`) and MITM path (`firewall/certs`) remain disjoint trees; handler types are separate (`internal/auth` vs `internal/controlplane/firewall/certs.go`); rotation entry points are separate (`clawker auth rotate` vs `FirewallRotateCA` RPC). |
| PRH-B2-007: No `cgroup_path` on proto; no CLI-side cgroup computation | **PASS** | (a) `grep "cgroup_path" api/admin/v1/admin.proto` returns only a comment explaining the removal. (b) `CgroupPath` field does not appear on any B2 request message in `admin.pb.go` proto types. (c) `DetectCgroupDriver` and `EBPFCgroupPath` identifiers appear only in `cmd/clawker-cp/main.go` + `internal/controlplane/firewall/cgroup{.go,_test.go}`. |

## Dependencies

No `go.mod` changes between `main` and `feat/firewall-cp-migration`. All B2 transport (gRPC, proto, whail/docker helpers) was added in B1. B2 is a pure internal restructure.

## Architecture Compliance

- **PAT-001 (Factory DI)**: `f.AdminClient` + `f.ControlPlane` introduced as proper lazy Factory nouns. `f.Firewall` field is deleted from `cmdutil.Factory`. CLI commands under `internal/cmd/firewall/` and `internal/cmd/controlplane/` use `NewCmd(f, runF)` + Options-struct closures — pattern is consistent with existing nouns (HostProxy, GitManager, etc.).
- **PAT-004 (Firewall Stack)**: Ownership inverted cleanly. CP now owns Envoy + CoreDNS lifecycle via DooD; the 13 RPCs expose the surface; `AgentWatcher` provides the drain-to-zero path and removes the need for a host-side PID daemon.
- **Package boundaries**: `internal/controlplane/firewall/` is a properly scoped sub-domain. `handler.go` / `stack.go` / `cgroup.go` split is clean. `ebpf/` moved inside the firewall domain (was at `internal/controlplane/ebpf/`). Mocks regenerated with `moq`.
- **Error handling**: gRPC errors use `status.Errorf(codes.*)` consistently. Internal errors wrap via `fmt.Errorf("...: %w", err)`. No `logger.Fatal` in Cobra hooks.
- **Logging**: zerolog for file-side logging only. User output via `fmt.Fprintf(ios.Out / ios.ErrOut, ...)` + `ColorScheme()` semantic icons.
- **Config access**: All ports/paths resolved via `cfg.Settings().ControlPlane` or interface methods (`cfg.FirewallDataSubdir()`, etc.). No hardcoded paths in handler/stack code.
- **Mock regeneration**: `ControlPlaneServiceMock`, `ManagerMock`, `IntrospectorMock`, `AdminServiceClientMock`, `EBPFManagerMock` are all moq-generated and DO-NOT-EDIT-headered. `FirewallManagerMock` deleted with its interface — no stale mock lingers.

**New pattern**: `f.ControlPlane()` noun — `controlplane.Manager` interface with lazy Factory closures for Docker/Config/Logger. This is a reasonable generalization of `f.HostProxy()`; `.correctless/ARCHITECTURE.md` PAT-004 should reference the new noun. Currently PAT-004 still says "Control plane owns ebpf.Manager.Load() lifetime" which is accurate but incomplete.

## QA Class Fixes Verified

No `qa-findings-*.json` file exists for this task slug. Workflow state shows `qa_rounds: 0`. This branch was implemented without /ctdd gating per the project's documented disablement of TDD-enforced phases (see `.correctless/learnings/tdd-phase-disabled.md` and the "No TDD on clawker" memory). No QA class fixes to verify.

## Smells

- **Spec text mismatch** in the spec's own Context section line 44: the B1 verification report (`control-plane-initiative-verification.md`) flagged that INV-B1-002's wording references "mTLS" even though INV-B1-001 drops mTLS. B2's spec text still carries this phrasing in several places (e.g., "mTLS + Hydra OAuth2" in the Context block). The implementation is correct (TLS + OAuth2 — B2's own `grpc_mtls_test.go` confirms mTLS IS actually required: `ClientAuth: tls.RequireAndVerifyClientCert`). Contradiction appears between B1's spec text and B2's spec text, not between spec and code. Not blocking.
- **Dead seam** in `test/e2e/harness/factory.go`: the factory's FactoryOptions still exposes `Firewall func(...)` with nil-default per `test/CLAUDE.md` legacy docs — but grep shows no such field in the updated harness (the current doc lists `AdminClient` + `ControlPlane`, which matches the test file). Documentation is correct; just worth confirming no stale references linger in other test files. No test file in the diff uses `opts.Firewall`.
- **`CPHealthTimeoutError`** is defined inside `internal/controlplane/bootstrap.go` and tested via `errors.As`, but the public error surface of the `controlplane` package isn't documented in the CP CLAUDE.md. Low priority.
- No TODO/FIXME/HACK comments added by B2 (diff-filtered check against the 10 commits).

## Drift

1. **`internal/hostproxy` reads `egress-rules.yaml` directly — direct spec violation (INV-B2-010 / PRH-B2-004).** The read is documented as intentional in `internal/hostproxy/CLAUDE.md` under "Egress Enforcement (`egress_check.go`)" with these justifications: leaf-package discipline (no import of `internal/controlplane/firewall` or `internal/storage`), fail-closed just-in-time read (no caching — rules change at runtime), defense-in-depth against URL-encoded exfil via the `/open/url` endpoint. The design predates B2 but the spec did not exempt it. **Presenting the choice to the user is required here** (drift resolution per verification protocol). Options:
   - **Fix (strict)** — hostproxy switches to `AdminService.FirewallListRules` via a new mTLS-capable gRPC client closure. Cost: hostproxy stops being a leaf package or requires injection of an `AdminServiceClient` through the Factory. Live-tuning latency increases (RPC round-trip per `/open/url`).
   - **Log as debt** — file a DRIFT-NNN entry; defer to a post-B2 branch that co-designs hostproxy/CP auth.
   - **Accept as intentional** — amend INV-B2-010 and PRH-B2-004 to exempt hostproxy; document the exemption in both the spec and `internal/hostproxy/CLAUDE.md`.
2. **`.correctless/ARCHITECTURE.md` drift**: PAT-004 text still describes the pre-B2 world ("Control plane owns ebpf.Manager.Load() lifetime" but no mention of `Stack`, `AgentWatcher`, 13-RPC surface, `f.AdminClient`, `f.ControlPlane`). `/cupdate-arch` would bring it up to date.
3. **`.correctless/AGENT_CONTEXT.md` drift**: "Key Components" table still lists `internal/firewall/` as a component. Spec says package is deleted; code confirms. Entry must be removed or updated to `internal/controlplane/firewall/`.

## Spec Updates

No `spec_updates` recorded in workflow state (`spec_updates: 0`). No amendments during implementation. The spec file remained stable from its initial drafting through the final commit. The spec is 1260 lines; dense but cohesive. Note that the workflow state file still lists the spec file as `.correctless/specs/cp-initiative/branch-2-cp-owns-firewall.md` while `phase: spec` — the state file was not advanced by the TDD phase because TDD is disabled for this project (see learnings/tdd-phase-disabled.md). Verification still proceeds since the implementation is complete and committed.

## Overall: FAIL with 3 BLOCKING findings, 4 weak, 3 drift items

### BLOCKING

1. **INV-B2-005 (CRITICAL/UNCOVERED)**: `f.AdminClient` lazy-bootstrap closure has zero unit tests — the spec mandates four specific assertions (ensureRunning-counter, rebuild on Shutdown/TransientFailure, keepalive dial options, Ready/Connecting/Idle treated as cacheable). None are present. The production logic lives in `internal/cmd/factory/default.go:204-258`; recommended fix is a `internal/cmd/factory/admin_client_test.go` that stubs `ensureRunning`, passes a mock `auth.DialCPAdmin` that captures dial options, and drives `grpc.ClientConn` state transitions via `grpc/test/bufconn`.
2. **INV-B2-010 + PRH-B2-004 (VIOLATION)**: `internal/hostproxy/egress_check.go` reads `egress-rules.yaml` directly. This needs explicit disposition — spec amendment (accept-as-intentional with carve-out), drift-debt entry, or a migration to `FirewallListRules` RPC.
3. **INV-B2-011 (UNCOVERED)**: No unit test asserts `mount.ReadOnly == true` on the Envoy and CoreDNS firewall-data-path mounts. The code is correct today; a simple `stack_test.go` addition that inspects `envoyContainerSpec(...).mounts` and `corednsContainerSpec(...).mounts` and checks `ReadOnly` on every `Target` beginning with `/etc/envoy` or `/etc/coredns` would close this.

### Weak (non-blocking, should be strengthened)

- **INV-B2-001 / INV-B2-002**: No AST `boundary_test.go` at module root. Spec explicitly names the mirror pattern.
- **INV-B2-006**: Real-Docker integration test for the RO→RW mount upgrade path is missing.
- **INV-B2-012**: Proto-reflection assertion that `CgroupPath` is not a field on the three request types is missing.
- **INV-B2-013**: Unit-level startup-orchestrator test (fake eBPF manager with pre-populated stale state, assert cleanup runs before `SetReady`) is missing.
- **INV-B2-014**: `EnsureNetwork` + external-delete integration test is missing.

### Recommended actions

1. **Write the INV-B2-005 tests** — this is critical infra logic with no test coverage whatsoever. Four assertions per spec.
2. **Resolve the INV-B2-010 / PRH-B2-004 hostproxy drift** — user disposition required. My recommendation: accept-as-intentional with a spec amendment, because hostproxy's leaf-package status is a deliberate design property and adding mTLS client plumbing to it is a much larger change than the security value justifies (the `/open/url` read is already defense-in-depth; the primary egress control is Envoy + eBPF).
3. **Add INV-B2-011 unit test** — trivial, one-file addition. No excuse.
4. **Backfill INV-B2-001 / INV-B2-002 AST boundary test** — the spec explicitly calls out the pattern. Adding a module-root `boundary_test.go` unlocks both invariants at once.
5. **Update `.correctless/ARCHITECTURE.md` PAT-004** and **`.correctless/AGENT_CONTEXT.md` Key Components table** — drift items (2) and (3).
6. **Consider adding spec-level BND-B2-003** documenting the hostproxy egress-check carve-out, regardless of disposition chosen for recommendation #2.
