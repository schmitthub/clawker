# CP Initiative — Branch 2: CP Owns the Firewall (Implementation Plan)

**Branch:** `feat/firewall-cp-migration`
**Parent memory:** `cp-initiative-status`
**Spec:** `.correctless/specs/cp-initiative/branch-2-cp-owns-firewall.md`
**Context doc:** `.correctless/specs/cp-initiative/CONTEXT.md`

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Relocate firewall package files into `internal/controlplane/firewall/` (pure move) | `complete` | opus |
| Task 2: Add `firewall.Stack` + `firewall/cgroup.go` helpers | `complete` | opus |
| Task 3: Add `controlplane/bootstrap.go` — host-side `EnsureRunning` | `complete` | opus |
| Task 4: `AgentWatcher` + CP self-shutdown + restart policy `on-failure` | `complete` | opus |
| Task 5: Protocol migration — proto rewrite + handler rewrite to 13-method scope-corrected surface | `pending` | — |
| Task 6: Factory `f.AdminClient` + command migration + delete `firewall.Manager` | `pending` | — |
| Task 7: Break-glass `clawker controlplane up/down/status` CLI | `pending` | — |
| Task 8: Delete `internal/firewall/` + final cleanup + docs | `pending` | — |

## Key Learnings

- **Task 1 (Opus, 2026-04-14):** Package name `firewall` is intentionally identical in old (`internal/firewall`) and new (`internal/controlplane/firewall`) paths. Every consumer that imports both uses an alias (`fwcp`/`fwlegacy`/`fwhandler`/`cpfw`). The old package is deleted in Task 6/8, at which point aliases disappear.
- **Task 1 (Opus):** `network.go` could not be moved clean because `firewall.Manager` uses raw moby (not `*docker.Client`). Resolution: moved `NetworkInfo` + added `DiscoverNetwork(ctx, *docker.Client, cfg)` + exported `ComputeStaticIP` in the new package; kept raw-moby `(m *Manager).discoverNetwork` / `ensureNetwork` as a temporary `internal/firewall/manager_network.go` that returns `fwcp.NetworkInfo`. Zero type divergence; single source of truth for `NetworkInfo`. Removed with Manager in Task 6/8.
- **Task 1 (Opus):** `rules.go` helpers (`normalizeRule`, `ruleKey`, `normalizeAndDedup`) + embed vars (`clawkerCPBinary`, `ebpfManagerBinary`, `corednsClawkerBinary`) had to be exported (capital letter) so `firewall.Manager` can call them cross-package until Task 6/8.
- **Task 1 (Opus):** `Makefile`, `Dockerfile.controlplane`, `.gitignore`, `REPRODUCIBILITY.md`, `internal/firewall/CLAUDE.md`, `internal/controlplane/CLAUDE.md`, `internal/controlplane/firewall/ebpf/{CLAUDE.md,cmd/CLAUDE.md}` all had hard-coded paths that needed updating. `COREDNS_BINARY` sits at `internal/controlplane/firewall/assets/coredns-clawker` (firewall subpkg); `EBPF_BINARY` and `CP_BINARY` sit at `internal/controlplane/assets/` (CP-core).
- **Task 1 (Opus) — test-hunter follow-ups for Task 6/8:**
  - `internal/controlplane/firewall/rules_store_test.go` has been merged from the old `rules_test.go`; of its ~10 tests only `TestValidateDst` + `TestEgressRulesFileFields_AllFieldsHaveDescriptions` actually exercise `rules_store.go`. The rest (`TestAddRules_*`, `TestRemoveRules`, `TestAddRules_RejectsInvalidDomain`, `TestAddRules_NormalizesEmptyFields`) exercise `fwlegacy.Manager.AddRules/RemoveRules/List`. When the legacy Manager is deleted in Task 6/8, split this file: keep the two real rules-store tests; move the Manager-driven tests onto whatever owns `AddRules` in the new CP `Stack` (Task 2 target) OR delete them as duplicated by `test/e2e/firewall_test.go` coverage. Do NOT bleed `fwlegacy` imports into the new package past Task 6.
  - `internal/controlplane/firewall/handler_test.go` still uses function prefix `TestAdminHandler_*` after the `AdminHandler → firewall.Handler` rename. Cosmetic only — rename to `TestHandler_*` in Task 5 (where the handler is being rewritten anyway) or Task 8 (final cleanup pass). No behavior change.
- **Task 1 (Opus) — pre-commit gotcha:** pre-commit stashes unstaged changes before running hooks. `git add` only adds named files; for modifications to tracked files (Makefile, Dockerfile.controlplane, .gitignore, sibling CLAUDE.mds) use `git add -u` (or `git commit -a`) BEFORE committing — otherwise the hook runs against the old tree and fails on stale paths that your unstaged edits already fixed.
- **Task 2 (Opus, 2026-04-14):** `*docker.Client.Info(ctx, client.InfoOptions{}) (client.SystemInfoResult, error)` promotes end-to-end through `whail.Engine`'s embedded `client.APIClient` — no new whail/docker method required, just a single-method extension to `whailtest.FakeAPIClient` (`InfoFn` + `Info`). Return type is `client.SystemInfoResult` (moby renamed; `client.InfoResult` does not exist).
- **Task 2 (Opus):** Static-IP assignment for Envoy/CoreDNS cannot go through whail's `ContainerCreateOptions.EnsureNetwork` — that helper hard-overwrites `EndpointSettings` with `{NetworkID}`, erasing any caller-supplied `IPAMConfig`. Call `dc.EnsureNetwork(ctx, ...)` first (defensive guard), then `DiscoverNetwork` + explicit `NetworkingConfig` with `IPAMConfig.IPv4Address` in `ContainerCreate`.
- **Task 2 (Opus):** whail's `ContainerInspect` enforces the managed-label jail via a nested `IsContainerManaged` call that *re-invokes* `ContainerInspectFn` — so test fakes have to return `Config.Labels[managedKey]=ManagedLabelValue` in the inspect response, not just an ID. Otherwise the jail returns `NotManaged` and the real caller sees `ErrContainerNotFound`. Confirmed by test fixture `managedInspectFn` in `cgroup_test.go`.
- **Task 2 (Opus):** Stop/Reload "no-op" tests need affirmative behavioral assertions (`NotContains(fake.Calls, "ContainerStop")`, `FileExists(envoy.yaml)`) or they pass trivially without exercising the short-circuit — test-hunter flagged this pattern and it's a recurring trap.
- **Task 2 (Opus) — whail pull stream caveat:** `APIClient.ImagePull` only returns a top-level error on initial HTTP failure; auth, manifest, and layer errors stream as JSON frames with an `error` field. `io.Copy(io.Discard, reader)` swallows them. Use a `drainPullStream` helper (in `stack.go`) that decodes frames and surfaces `msg.Error`. Same pattern applies to `ImageBuild` — `ensureCorednsImage` already does this.
- **Task 2 (Opus) — network-not-found classification:** whail wraps "network missing" errors in `*DockerError{Op: "network_find"}` which does NOT implement `cerrdefs.NotFound`. `strings.Contains(err.Error(), "not found")` substring-matches false positives ("image not found", "endpoint not found"). Simpler: in Status, log any discovery error at Warn and leave topology fields empty — callers already use per-container `isRunning` to distinguish "stack down" from "Docker unreachable". Avoids the classification entirely.
- **Task 2 (Opus) — reviewer findings applied:** 1) `ensureEnvoyImage` now decodes JSON pull stream for errors (silent-failure-hunter CRITICAL). 2) `WaitForHealthy.HealthTimeoutError.Timeout` uses `time.Since(start)` — previous code passed a stale `time.Until(deadline)` value. 3) `isNetworkNotFound` substring-match helper deleted; Status logs network-discovery errors at Warn instead. 4) Deleted 3 tautological/phantom tests (`TestDetectCgroupDriver_ReturnsDriver`, `TestNewStack_NilLoggerSubstitutesNop`, `TestStack_Accessors_ReturnDiscoveredValues`), merged 3 `TestEBPFCgroupPath_*` into one table-driven test, switched `TestResolveContainerID_PropagatesLookupError` from `.Contains(err.Error(), name)` to `assert.ErrorIs(err, sentinel)`, strengthened Stop/Reload no-op tests with affirmative Calls/FileExists assertions. Comment cleanup: removed ephemeral Task-N/Phase-N/legacy references per project rule.
- **Task 3 (Opus, 2026-04-14):** `NetworkInfo` gained a `Gateway netip.Addr` field so `controlplane.EnsureRunning` can call `fwcp.ComputeStaticIP(gateway, cfg.CPIPLastOctet())` instead of re-deriving the CP IP from EnvoyIP+2 — follows the existing `EnvoyIPLastOctet`/`CoreDNSIPLastOctet` config-accessor pattern. `consts.CPIPLastOctet = 202` + `config.Config.CPIPLastOctet() byte` accessor + moq regen. Three-way symmetry now means Task 8 cleanup can delete the B1 `cpStaticIP(envoyIP)` helper without replacement.
- **Task 3 (Opus):** `BuildCPContainerConfig` gained a RW bind mount of `cfg.FirewallDataSubdir()` → `/var/lib/clawker/firewall` and a `RestartPolicy container.RestartPolicy` field set to `on-failure` with `MaximumRetryCount=3`. B1 never mounted this path at all (firewall Manager wrote files host-side). The B1-era container produces full mount-set divergence — detected by `hasMountDivergence` which does a bidirectional check: length, Target presence, Source, Type, ReadOnly. A B1-era container (legacy mount set) will therefore always be reconciled on first B2 `EnsureRunning`.
- **Task 3 (Opus) — reviewer findings applied:** 1) `ensureCPImage` stream decoder now uses `drainBuildStream` helper that distinguishes `io.EOF` (success) from truncated-stream / malformed-JSON (error) and decodes both `error` and `errorDetail.message` — BuildKit emits the detailed form (silent-failure-hunter CRITICAL #1). 2) `createCPContainer` name-conflict recovery uses `cerrdefs.IsConflict(err)` instead of English substring match; `recoverFromNameConflict` helper validates the recovered container against `hasMountDivergence` + a non-nil managed-list hit, refusing to start an unmanaged squatter or a mount-divergent leftover (silent-failure-hunter HIGH #3–#5; code-reviewer Important #1). 3) `waitForCPHealthz` captures last probe outcome (transport error, status code, body snippet) on `*CPHealthTimeoutError`; `Unwrap` returns the last transport error so `errors.As` works both for the typed timeout and any wrapped underlying error. Deadline check moved ABOVE `ctx.Err()` check so a caller-supplied short ctx deadline produces the typed error with diagnostics rather than bare `context.DeadlineExceeded`. `context.Canceled` still returns the raw ctx error (distinguishes "caller Ctrl+C'd" from "CP never came up"). 4) `hasMountDivergence` is now bidirectional: `len(got) != len(want)` + Source/Type/ReadOnly comparison per-Target (code-reviewer Important #1). 5) `Stop` signature dropped the `cfg` "for symmetry" parameter. 6) Removed redundant RO-mount test (merged into one table-driven `TestEnsureRunning_MountDivergence_RecreatesContainer` covering missing/RO-flip/source-change/extra-mount divergences). 7) Rewrote concurrent test to block `ContainerCreateFn` on a release channel + assert create-count==1 while blocked — genuinely exercises `ensureMu`. 8) Rewrote timeout test with a real `httptest.Server` returning 503 + snapshot assertion that `LastStatus==503` and `LastBody` contains the diagnostic payload — eliminated the "either CPHealthTimeoutError OR context.DeadlineExceeded" phantom fork (test-hunter). 9) Removed `findFreePort` dead helper + unused `bootstrapFixture.reset` field. 10) Comment-analyzer pass: dropped "B1 legacy layout", "B1 → B2 upgrade case", "B1-era container", "Matches the B1 cpStaticIP policy" references.
- **Task 4 (Opus, 2026-04-14):** `AgentWatcher` landed as a package-level type in `internal/controlplane/watcher.go` with injectable `listAgents` + `onDrainToZero` seams. CP main.go replaced the raw `mobyclient.New` construction with `docker.NewClient(ctx, cfg, log)` so the same handle drives the firewall Stack, container resolver, and watcher's list-agents closure — avoids fan-out of moby clients. The watcher uses `client.Filters{}.Add("label", ...)` directly (moby path; daemon exception per `.claude/rules/docker-client.md`) instead of pulling `pkg/whail` into CP main.
- **Task 4 (Opus) — drain-to-zero ordering:** Spec §6 listed order as `Stack.Stop → BPF flush → GracefulStop → exit`, but reviewer flagged a real RPC/Flush race — gRPC still serves throughout. Final ordering inside `drainCallback` is `handler.CancelAllBypassTimers → grpcServer.GracefulStop → stack.Stop → ebpfMgr.FlushAll`, then the outer shutdown path runs (GracefulStop is idempotent on second call). This is intentionally stricter than spec §6 and should be reflected when Task 8 edits the spec's prose.
- **Task 4 (Opus) — error propagation:** `drainCallback` returns `errors.Join(stopErr, flushErr)` rather than nil-on-warning. `run()` captures the drain error into `drainErr` and returns it so the CP exits non-zero on partial teardown; this lets the `on-failure` restart policy legitimately retrigger investigation instead of silently blessing a broken drain. `CleanupStaleBypass` signature changed from `int` to `(int, error)` for the same reason — startup must fail if defensive cleanup can't complete.
- **Task 4 (Opus) — watcher hardening:** (1) `ListErrCeiling` (default 20) bounds how many consecutive Docker errors the watcher tolerates before surfacing an error from `Run`. Prevents the "Docker wedged → CP stays blind forever" hole silent-failure-hunter flagged. (2) `started atomic.Bool` makes the at-most-once Run contract structural — a second Run call returns an error instead of spinning up a competing poll goroutine. (3) Negative option values panic (matches the nil-callback panic policy) instead of snapping to defaults — misconfig must fail loudly.
- **Task 4 (Opus) — Handler race mitigation:** `CancelAllBypassTimers` reassigns `h.bypassTimers = make(...)` after the stop loop (cleaner than per-entry delete during range). The race where a timer's fire goroutine has already passed `timer.Stop`'s gate and will call `Enable(cgID)` after `FlushAll` is documented on the method; benign because `Enable`'s `clearBypass` treats `ErrKeyNotExist` as success.
- **Task 4 (Opus) — E2E files authored, not run:** `test/e2e/cp_self_shutdown_test.go` verifies INV-B2-007 (clean exit code 0 + `RestartCount == 0`); `test/e2e/cp_startup_cleanup_test.go` verifies INV-B2-013 via the break-glass `ebpf-manager` CLI (seeds bypass_map, restarts CP, asserts entry cleared). Startup-cleanup test depends on `ebpf-manager bypass <cgroupID>` and `ebpf-manager bypass-status <cgroupID>` — verify those subcommands exist / accept orphan IDs during host-side review. Neither E2E is run by agents; both compile (`go vet ./test/e2e/...`).
- **Task 4 (Opus) — reviewer findings applied:**
  1. Reordered drain callback: GracefulStop before FlushAll (code-reviewer HIGH #2, silent-failure-hunter HIGH #3).
  2. `drainCallback` returns `errors.Join(...)` instead of nil (silent-failure-hunter BLOCKER #1).
  3. `CleanupStaleBypass` signature `(int, error)` (silent-failure-hunter HIGH #4).
  4. Watcher `ListErrCeiling` + surfaced error (silent-failure-hunter HIGH #2).
  5. Watcher `started atomic.Bool` + consistent panic-on-misconfig policy (type-design-analyzer).
  6. Consolidated happy-path + error-propagation tests into `TestAgentWatcher_DrainCallback` table-driven (test-hunter MERGE).
  7. Comment cleanup: dropped section dividers (`--- Block on signal ---`, `--- Graceful shutdown ---`), removed duplicate `matching ebpfCgroupPath()` cross-reference, retitled the "Branch 2 E2E Policy" host-only comments to stable phrasing (comment-analyzer).
  8. `CancelAllBypassTimers` reassigns map instead of delete-in-loop (code-simplifier).
  9. Watcher `ctx.Err()` idiom replaces `errors.Is(ctx.Canceled|DeadlineExceeded)` (code-simplifier).
- **Task 4 (Opus) — kept for Task 6/8 cleanup:** `CleanupStaleBypass` + `FlushAll` are on concrete `*Manager` only, not on the `EBPFManager` RPC interface — this is deliberate (these are lifecycle operations, not RPCs). Task 5's handler rewrite may want to share the drift resolver between Handler and the AgentWatcher's drain callback if future work adds more per-container cleanup. The `DockerCgroupPath(driver, containerID)` helper suggested by code-simplifier (consolidate the systemd/cgroupfs switch shared by `cmd/clawker-cp/main.go:containerResolver` and `ebpf/types.go:CgroupPath`) is tabled for Task 5/8 — scope creep relative to Task 4's mandate.

- **Task 3 (Opus) — kept for Task 6/8 cleanup:** The `drainBuildStream` helper in `bootstrap.go` and `drainPullStream`/build-drain loop in `firewall/stack.go:ensureCorednsImage` are near-duplicates. Consolidate into a single `firewall.DrainBuildStream` (exported) or a `controlplane/internal/dockerstream` package when Task 8 does the final cleanup pass. Same story for `cpBuildContext` vs `corednsBuildContext` — generic `buildTarContext([]tarFile)` would dedupe. Simplifier reviewer's struct-wrapper suggestion (`Bootstrap` type w/ methods instead of package-level vars + free functions) is also tabled for now — the current shape matches the `firewall.Stack` neighbor and is called exactly once by `adminClientFunc` in Task 6, so the struct conversion adds ceremony without removing coupling. Revisit if Task 6 surfaces test-isolation issues with the package-level seam vars.

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run the acceptance gates for the completed task (build + vet + `make test`; author any required E2E tests — see **E2E Policy** below — but do NOT run them).
2. Update the Progress Tracker in this memory.
3. Append any key learnings to the Key Learnings section.
4. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents to review this task's changes, then fix any and all findings. **All six must be invoked** even if you believe the diff is trivial — `test-hunter` in particular catches dead tests and self-serving assertions that mechanical moves accidentally preserve.
5. Commit all changes from this task with a descriptive commit message. Stage with `git add -u` + explicit `git add <new>` (or `git commit -a`) before invoking commit so pre-commit hooks see the full tree.
6. Present the handoff prompt from the task's Wrap Up section to the user.
7. Wait for the user to start a new conversation with the handoff prompt.

Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## E2E Policy (applies to every task in this initiative)

**Agents author E2E tests. Agents do NOT run E2E tests.**

- Agents run in a clawker-managed container and cannot drive their own Docker stack reliably. Running `go test ./test/e2e/...`, `./bin/clawker run @`, or any other smoke path that spins up clawker infrastructure from inside the container is forbidden.
- Every task that lists E2E work must **land the E2E test files in the same commit** as the production code they cover. Tests should compile (`go vet ./test/e2e/...`) but are never executed by agents.
- End-to-end validation is deferred to a **final host-side review by the user** once the initiative is complete (all 8 tasks done + PR ready). The user will run the full E2E suite on the host and feed regressions back.
- Acceptance gates agents actually run each task: `go build ./...`, `go vet ./...`, `make test` (unit). Everything else is written, committed, and deferred.
- If an agent believes a task is blocked without E2E execution signal, stop and escalate to the user — do not loosen the test design or fabricate verification.

---

## Context for All Agents

### Background

Branch 2 completes the ownership inversion of clawker's firewall stack. Today `internal/firewall/manager.go` (1,715 LoC) is a god package that bootstraps the control plane as if the CP were a firewall dependency; `internal/firewall/daemon.go` (696 LoC) runs a host-side PID-file daemon that holds lifecycle authority. Both are replaced by CP-side equivalents.

Additionally, B1's proto surface (`api/admin/v1/admin.proto`) had scope inversions that a prior agent introduced sloppily: per-container `Install/Remove` overlapped with the per-container enrollment job `Enable`; per-container `Remove` was really a global teardown. B2 corrects this to a 13-method scope-correct surface (see spec §8 and Context table).

**Package itself is deleted.** `internal/firewall/` does not survive the merge. Files migrate to `internal/controlplane/` (CP core) and `internal/controlplane/firewall/` (NEW firewall subpackage). The `FirewallManager` interface is deleted entirely. Commands call `f.AdminClient(ctx) (adminv1.AdminServiceClient, error)` directly.

### Testing Policy (READ THIS FIRST)

**TDD is disabled on this project.** See `.correctless/learnings/tdd-phase-disabled.md` for the full post-mortem. Branch 1 ran `/ctdd` with a subagent and produced garbage unit tests; the next agent saw tests pass and skipped most requirements. User spent 12+ hours rescuing it.

**What this branch uses instead:**
- Integration tests + E2E over real Docker (`test/e2e/`, `test/whail/`) — **authored** alongside production code, **not run** by agents (see **E2E Policy** above).
- **Reuse the battle-tested test infra** listed below. Do not invent new fixtures when equivalents exist.
- Unit tests are allowed but must exercise real behavior, not mock return values.
- Tests land in the same commit as the code change they cover.

**Existing test infra (use these, don't replace them):**
- `internal/testenv/` — isolated XDG dirs, config/project manager setup (`testenv.New(t, opts...)`)
- `internal/config/mocks/` (import as `configmocks`) — `NewBlankConfig()`, `NewFromString(projectYAML, settingsYAML)`, `NewIsolatedTestConfig(t)`, `ConfigMock`
- `internal/docker/mocks/` — `FakeClient`, moby mock transport, `SetupContainerCreate`, `SetupContainerStart`
- `pkg/whail/whailtest/` — `FakeAPIClient`, recorded build scenarios
- `internal/controlplane/mocks/` — `MockServer`, moq-generated mocks (`ControlPlaneServiceMock`, `IntrospectorMock`, `EBPFManagerMock`)
- `internal/hostproxy/hostproxytest/` — `MockHostProxy`
- `internal/git/gittest/` — `InMemoryGitManager`
- `test/e2e/harness/` — CLI test harness (delegates dirs to testenv, adds chdir + Factory + Run)
- `test/whail/` — BuildKit integration
- `iostreams.Test()` → `(*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer)` for command tests

See `.claude/rules/testing.md` for the full DAG-driven test pattern.

### Key Files (read before starting any task)

Authoritative sources (read in this order):
1. `CLAUDE.md` (repo root — mantra, tooling, design decisions)
2. `.correctless/specs/cp-initiative/branch-2-cp-owns-firewall.md` (the spec — single source of truth for semantics)
3. `.claude/rules/testing.md` (testing DAG)
4. Package-specific CLAUDE.md files touched by the task

Core code locations:
- `api/admin/v1/admin.proto` — proto surface (rewritten in Task 5)
- `api/admin/v1/admin.go` — method-scope registration map
- `internal/controlplane/` — CP core (grows with bootstrap.go, watcher.go, embed moves)
- `internal/controlplane/firewall/handler.go` — `firewall.Handler` (was `internal/controlplane/admin_handler.go` pre-Task-1; rewritten in Task 5)
- `internal/controlplane/firewall/ebpf/` — eBPF subsystem (moved here in Task 1)
- `internal/firewall/` — legacy package, deleted in Task 8 (Task 1 slimmed it to `firewall.go` + `manager.go` + `manager_network.go` + `daemon.go` + `mocks/`)
- `internal/cmd/firewall/` — 13 subcommands rewired in Task 6
- `internal/cmd/container/shared/container_start.go` — `BootstrapServicesPostStart` rewired in Task 6
- `internal/cmd/factory/default.go` — `firewallFunc` → `adminClientFunc` in Task 6
- `internal/cmdutil/factory.go` — Factory field swap in Task 6
- `cmd/clawker-cp/main.go` — watcher wiring in Task 4, handler ctor update in Task 5

### Design Patterns

- **Factory DI**: Factory is a pure struct with closure fields. Commands take `*cmdutil.Factory` + a `runF` for test injection. Construct test Factories with struct literals + configmocks/FakeClient; never call `factory.New()` outside `internal/clawker/cmd.go`.
- **DAG-driven test doubles**: Every package in the dependency DAG provides test utilities (see Testing Policy above).
- **Factory noun principle**: `f.AdminClient(ctx)` returns an `adminv1.AdminServiceClient` (a thing), not an action. Callers call methods on it: `adminClient.FirewallEnable(ctx, req)`.
- **Whail enforcement**: only `pkg/whail` imports moby client; only `internal/docker` imports whail. CP uses `*docker.Client` (not raw moby — the `internal/firewall` exception is removed in Task 8). `docker.Client.Info(ctx) (system.Info, error)` is already promoted end-to-end via Go embedding (`whail.Engine` embeds `client.APIClient` at `pkg/whail/engine.go:34`). No new test infra needed beyond a tiny `InfoFn` stub on `whailtest.FakeAPIClient` in Task 5.
- **gRPC + mTLS**: CP AdminService is mTLS (CLI-CA signed) + OAuth2 JWT (Hydra introspection) + uniform `"admin"` scope. All RPCs must appear in the registered-methods set (INV-B2-009 fail-closed).
- **Drift guard**: Every `FirewallEnable` resolves container_id → fresh cgroup_path via Docker API before writing BPF state (INV-B2-016). The existing `resolveBypassCgroupID` in handler.go (pre-rewrite) is the reference implementation — extract into a shared helper in Task 5.
- **Domain handler embedding**: `adminServer` in `server.go` embeds `*firewall.Handler` so Go method promotion surfaces all 13 methods at the composite. Future branches embed `*monitor.Handler`, `*hostproxy.Handler`, etc.

### Rules

- Read `CLAUDE.md`, relevant `.claude/rules/` files, and package CLAUDE.md before starting each task.
- Use Serena tools for code exploration — read symbol bodies only when needed.
- Use deepwiki + Context7 for external library docs before guessing API.
- All new code must compile (`go build ./...`), pass `go vet ./...`, and pass `make test` at each task boundary. Author E2E tests but do NOT run them (see **E2E Policy**).
- Do not skip integration tests or rationalize away security boundary tests — write them, commit them, defer execution to the final user review.
- Never hand-edit moq-generated mocks — regenerate via `go generate ./...`.
- Use `Config` interface accessors (`cfg.FirewallDataSubdir()`, etc.) — never hardcode paths.

---

## Task 1: Relocate firewall package files into `internal/controlplane/firewall/` (pure move, no semantic change) — COMPLETE

> **Note on `docker.Client.Info`**: `whail.Engine` embeds `client.APIClient` directly (`pkg/whail/engine.go:34`), and `internal/docker.Client` composes `whail.Engine`, so `Info(ctx) (system.Info, error)` is already promoted end-to-end — no whail/docker changes required. The only test-infra gap is `whailtest.FakeAPIClient`, which embeds a nil `*client.Client` for unexported methods and does NOT explicitly stub `Info`. Task 5 (where `DetectCgroupDriver` is wired into Handler init — via CP-side `internal/docker.Client` with its existing label/name machinery) adds an `InfoFn func(context.Context) (system.Info, error)` field on `FakeAPIClient` and the dispatching method (~10 lines, identical to the existing `ContainerCreateFn` etc. pattern). `internal/docker/mocks/helpers.go` can gain a matching `SetupInfo(info system.Info)` helper if the call pattern shows up in multiple tests.

Task 1 landed as commit `6d1a5c0a`. See Key Learnings for the file-mapping surprises encountered (raw-moby Manager helpers kept as `manager_network.go`; `normalizeRule`/`ruleKey`/`normalizeAndDedup` + embed vars exported; test-hunter deferred follow-ups for Task 5/6/8).

### Acceptance Criteria (agent-run)

```bash
go build ./...
go vet ./...
make test
```

Preserve B1 semantics intact — this task only moved files, it did NOT change behavior.

### Deferred to final host-side review

- `go test ./test/e2e/firewall_test.go -timeout 5m`
- `./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @`

---

## Task 2: Add `firewall.Stack` + `firewall/cgroup.go` helpers (new code, not yet wired)

**Creates/modifies:**
- NEW: `internal/controlplane/firewall/stack.go` — `Stack` type managing Envoy + CoreDNS lifecycle via DooD
- NEW: `internal/controlplane/firewall/stack_test.go`
- NEW: `internal/controlplane/firewall/cgroup.go` — `DetectCgroupDriver`, `EBPFCgroupPath`, `ResolveContainerID`
- NEW: `internal/controlplane/firewall/cgroup_test.go`
- NEW: `internal/controlplane/firewall/status.go` — internal `Status` struct (used by Stack and later by FirewallStatus RPC)
- NEW (authored, not run): `test/e2e/firewall_stack_test.go` — integration test exercising Stack lifecycle against real Docker

**Depends on:** Task 1 (firewall subpackage exists)

### Implementation Phase

1. Read Spec §4 (Stack type) + §9 (cgroup resolution).
2. Implement `Stack`:
   ```go
   type Stack struct {
       docker *docker.Client
       cfg    config.Config
       log    *logger.Logger
       store  *storage.Store[EgressRulesFile]
   }
   ```
   Methods: `NewStack`, `EnsureRunning(ctx)`, `Stop(ctx)`, `Reload(ctx)`, `WaitForHealthy(ctx)`, `Status(ctx)`, `EnvoyIP()`, `CoreDNSIP()`, `NetworkID()`, `CIDR()`.
   - Logic moves from `firewall.Manager`'s Envoy/CoreDNS startup portion — call `docker.Client.EnsureNetwork` internally as defensive guard.
   - Generate configs from `store.Read().Rules` before start.
3. Implement `DetectCgroupDriver(ctx, *docker.Client) (string, error)` — calls `docker.Client.SystemInfo` and returns the cgroup driver string (`"systemd"` or `"cgroupfs"`).
4. Implement `EBPFCgroupPath(cgroupDriver, containerID string) string` — pure function, mirrors today's `ebpfCgroupPath` in `firewall/manager.go`.
5. Implement `ResolveContainerID(ctx, *docker.Client, ref string) (string, error)` — fast-path 64-char hex, otherwise Docker API inspect.
6. Author integration test for Stack against real Docker (drop in `test/e2e/firewall_stack_test.go`). Commit, do NOT run.
7. Unit tests for cgroup helpers using stubbed `docker.Client.FakeClient`.

### Acceptance Criteria (agent-run)

```bash
go build ./...
go vet ./...
go vet ./test/e2e/...                               # ensure authored E2E at least compiles
make test
go test ./internal/controlplane/firewall/... -v
```

### Deferred to final host-side review

- `go test ./test/e2e/... -run TestFirewallStack -timeout 10m`
- `./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @`

### Wrap Up

1. Update Progress Tracker: Task 2 -> `complete`.
2. Append to Key Learnings.
3. Run all six review subagents (see Context Window Management step 4).
4. Commit: `feat(firewall): add Stack + cgroup helpers in controlplane/firewall subpackage`
5. **STOP.** Handoff:

> **Next agent prompt:** "Continue CP Initiative Branch 2. Read memory `cp-initiative-branch-2-firewall-migration-plan` — Task 2 is complete. Begin Task 3: Add `controlplane/bootstrap.go` — host-side `EnsureRunning`."

---

## Task 3: Add `controlplane/bootstrap.go` — host-side `EnsureRunning` (new code, not wired)

**Creates/modifies:**
- NEW: `internal/controlplane/bootstrap.go` — `EnsureRunning(ctx, *docker.Client, config.Config, *logger.Logger) error` + `Stop(...)`
- NEW: `internal/controlplane/bootstrap_test.go`
- MODIFY: `internal/controlplane/cp_container.go` — `BuildCPContainerConfig` mounts `FirewallDataSubdir` RW (was RO in B1); set restart policy `on-failure` with max 3 retries; ensure `clawker-net` attachment (INV-B2-014).

**Depends on:** Task 2 (reuses firewall.Stack indirectly; bootstrap only starts the CP container)

### Implementation Phase

1. Read Spec §3 (EnsureRunning signature + steps), §7 (CP container spec).
2. Implement `controlplane.EnsureRunning`:
   - Idempotent, concurrency-safe (mutex).
   - Steps: ensure auth material → ensure CP image → `docker.Client.ContainerCreate` with `ContainerCreateOptions.EnsureNetwork` → `ContainerStart` → poll `/healthz` on `127.0.0.1:<HealthPort>` until 200 or timeout.
   - **Mount-mode reconciliation (INV-B2-006)**: if an existing CP container is found with RO bind mount on `FirewallDataSubdir`, stop + remove it and recreate with RW.
   - Fail-closed on partial state — return error with actionable message.
3. Implement `controlplane.Stop(ctx, ...)` — stops CP container only (NOT Envoy/CoreDNS; those stay until `FirewallRemove` RPC — INV-B2-008).
4. Modify `BuildCPContainerConfig`:
   - Flip `FirewallDataSubdir` mount to RW.
   - Add restart policy: `container.RestartPolicy{Name: RestartPolicyOnFailure, MaximumRetryCount: 3}`.
   - Explicit `clawker-net` endpoint; use `ContainerCreateOptions.EnsureNetwork`.
5. Unit tests with `docker/mocks.FakeClient`:
   - Concurrent goroutine race: assert single container create.
   - Existing RO-mount container → assert stop + recreate.
   - `/healthz` never returns 200 → assert `HealthTimeoutError`.
   - Container already running + healthy → fast path, no-op.
6. Update `container_config_test.go`: assert `ReadOnly == false` for FirewallDataSubdir mount; assert `RestartPolicy.Name == "on-failure"`; assert `clawker-net` in endpoint config.

### Acceptance Criteria (agent-run)

```bash
go build ./...
go vet ./...
make test
go test ./internal/controlplane/... -v
```

### Deferred to final host-side review

- `./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @`

Note: new `controlplane.EnsureRunning` is unused by production code yet — `firewall.Manager.EnsureRunning` still owns the CP lifecycle path. Task 6 cuts over.

### Wrap Up

1. Update Progress Tracker: Task 3 -> `complete`.
2. Key Learnings.
3. Run all six review subagents.
4. Commit: `feat(controlplane): add host-side bootstrap.EnsureRunning + mount/restart-policy updates`
5. **STOP.** Handoff:

> **Next agent prompt:** "Continue CP Initiative Branch 2. Read memory `cp-initiative-branch-2-firewall-migration-plan` — Task 3 is complete. Begin Task 4: AgentWatcher + CP self-shutdown + restart policy."

---

## Task 4: `AgentWatcher` + CP self-shutdown + defensive startup cleanup

**Creates/modifies:**
- NEW: `internal/controlplane/watcher.go` — `AgentWatcher` (30s poll, missed_threshold=2, 60s grace)
- NEW: `internal/controlplane/watcher_test.go`
- MODIFY: `cmd/clawker-cp/main.go` — wire watcher + drain-to-zero callback + graceful shutdown sequence
- MODIFY: `internal/controlplane/startup.go` — add defensive eBPF cleanup pass on startup (INV-B2-013) BEFORE `SetReady()`
- NEW (authored, not run): `test/e2e/cp_self_shutdown_test.go` + `test/e2e/cp_startup_cleanup_test.go`

**Depends on:** Task 2 (Stack for shutdown), Task 3 (bootstrap for context)

### Implementation Phase

1. Read Spec §6 (AgentWatcher) + INV-B2-007 + INV-B2-013.
2. Implement `AgentWatcher`:
   ```go
   type AgentWatcher struct {
       docker          *docker.Client
       cfg             config.Config
       log             *logger.Logger
       pollInterval    time.Duration     // 30s; test seam via option
       missedThreshold int               // 2
       gracePeriod     time.Duration     // 60s
       onDrainToZero   func(ctx) error   // caller-supplied
       listAgentsFn    func(ctx) (int, error)  // test seam; prod uses ListContainersByProject
   }
   func (w *AgentWatcher) Run(ctx) error
   ```
   - Counts containers with `purpose=agent, managed=true` labels.
   - Grace period before first "missed" count.
   - On drain-to-zero: call `onDrainToZero`, return.
3. Wire into `cmd/clawker-cp/main.go` AFTER `orchestrator.SetReady()`:
   - Drain callback: `Stack.Stop` → eBPF flush (container_map, bypass_map, bypass timers, pinned links) → `grpcServer.GracefulStop` → `os.Exit(0)`.
   - Strict ordering per INV-B2-007.
4. Add startup cleanup pass in `CPStartupOrchestrator` BEFORE `SetReady`:
   - Call `ebpf.Manager.CleanupAllLinks()` or an idempotent flush sequence.
   - Remove orphaned pinned links.
   - Clear any stale bypass_map entries (if persisted across crash).
5. Unit test for watcher:
   - Stub `listAgentsFn` returning 0 for N polls; assert `onDrainToZero` fires in exact order (Stack.Stop → BPF flush → GracefulStop → exit).
   - Stub returning non-zero; assert no exit.
   - Verify grace period respected.
6. **Author** integration tests in `test/e2e/`:
   - Start CP with zero agents → observe `(30s × 2) + 60s` grace → assert container exited (0) and NOT restarted (restart policy is `on-failure`).
   - Pre-seed BPF maps with stale entries; start CP; assert maps empty before first RPC.
   - Commit the files; do NOT run them.

### Acceptance Criteria (agent-run)

```bash
go build ./...
go vet ./...
go vet ./test/e2e/...
make test
go test ./internal/controlplane/... -v
```

### Deferred to final host-side review

- `go test ./test/e2e/... -run TestCPSelfShutdown -timeout 5m`
- `go test ./test/e2e/... -run TestCPStartupCleanup -timeout 5m`
- `./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @`

### Wrap Up

1. Update Progress Tracker: Task 4 -> `complete`.
2. Key Learnings.
3. Run all six review subagents.
4. Commit: `feat(controlplane): add AgentWatcher + self-shutdown + defensive startup cleanup`
5. **STOP.** Handoff:

> **Next agent prompt:** "Continue CP Initiative Branch 2. Read memory `cp-initiative-branch-2-firewall-migration-plan` — Task 4 is complete. Begin Task 5: Protocol migration — proto rewrite + handler rewrite to 13-method scope-corrected surface."

---

## Task 5: Protocol migration — proto rewrite + handler rewrite (THIS IS THE BIG SEMANTIC CHANGE)

**Creates/modifies:**
- REWRITE: `api/admin/v1/admin.proto` to 13-method scope-corrected surface (see Spec §8).
- REGENERATE: `api/admin/v1/admin.pb.go` + `admin_grpc.pb.go` via `make proto`.
- MODIFY: `api/admin/v1/admin.go` — `AdminMethodScopes()` registered-methods map — all 13 methods mapped to `"admin"`.
- REWRITE: `internal/controlplane/firewall/handler.go` with the 13 methods:
  - `FirewallInit` (global — stack up + BPF attach)
  - `FirewallRemove` (global — stack down + BPF detach + flush all state)
  - `FirewallEnable(container_id, config)` (per-container, drift-guarded — INV-B2-016)
  - `FirewallDisable(container_id)` (per-container — remove from container_map)
  - `FirewallBypass(container_id, timeout)` (per-container — timed Disable + dead-man Enable)
  - `FirewallAddRules` / `FirewallRemoveRules` / `FirewallListRules` / `FirewallReload` / `FirewallStatus` / `FirewallRotateCA` (global)
  - `FirewallSyncRoutes` / `FirewallResolveHostname` (global — unchanged from B1)
- NEW: shared drift resolver extracted from the old `resolveBypassCgroupID` — callable from both `FirewallEnable` and bypass timer restore.
- MODIFY: `internal/controlplane/server.go` — `adminServer` struct embeds `*firewall.Handler`; register as sole `AdminServiceServer`.
- MODIFY: `internal/controlplane/startup.go` — `CPStartupOrchestrator` constructs Stack + firewall.Handler.
- MODIFY: `internal/firewall/manager.go` — adapter shims for B1-named methods that now call the renamed RPCs internally. **This is temporary** — deleted in Task 6.
- UPDATE: `authz.go` registered-methods test to reflect all 13 methods via `AdminServiceServer` reflection.
- UPDATE: `internal/controlplane/firewall/CLAUDE.md` with the 13-method surface.
- **Cosmetic rename:** `internal/controlplane/firewall/handler_test.go` test prefix `TestAdminHandler_*` → `TestHandler_*` (carries over from Task 1's pure-move; rename them alongside the handler rewrite).
- AUTHOR (not run): E2E coverage in `test/e2e/firewall_test.go` for drift case, container-gone case, bypass expiry, full enroll→bypass→restore flow.

**Depends on:** Tasks 2, 3, 4 (subpackage + Stack + bootstrap + watcher all in place)

### Implementation Phase

1. Read Spec §5, §8, §9, §Context table. Study `resolveBypassCgroupID` in B1's handler.
2. Rewrite `admin.proto`:
   - Drop B1's 7 short-named RPCs.
   - Add 13 prefixed RPCs per Spec §8.
   - Remove `cgroup_path` field from all per-container requests.
   - `FirewallRemoveRequest` and `FirewallInitRequest` are empty (global).
   - `FirewallEnableRequest` carries `container_id` + `ContainerConfig`.
3. Run `make proto`.
4. Extract drift resolver from old handler into `internal/controlplane/firewall/drift.go` (or embed in handler.go). Shared by Enable and bypass timer.
5. Implement new `firewall.Handler`:
   - Constructor caches `cgroupDriver` from `DetectCgroupDriver` at init.
   - Each per-container RPC calls `ResolveContainerID` + drift resolver + `EBPFCgroupPath` internally.
   - `FirewallEnable`: idempotent. Docker lookup → fresh cgroup_id → write `container_map` → attach BPF links if not present → clear any bypass flag.
   - `FirewallDisable`: delete `container_map` entry (BPF links stay attached — fast re-enable).
   - `FirewallBypass`: `FirewallDisable` + `time.AfterFunc` that calls `FirewallEnable` (drift-guarded) on expiry.
   - `FirewallInit`: `Stack.EnsureRunning` + `ebpf.Manager.Load()` (if not already loaded).
   - `FirewallRemove`: `Stack.Stop` + flush all eBPF state + cancel all bypass timers.
6. Add `adminServer` in `server.go` embedding `*firewall.Handler`. Compile-time check: `var _ adminv1.AdminServiceServer = (*adminServer)(nil)`.
7. Update `CPStartupOrchestrator` to wire new Handler + Stack.
8. Adapter shim in `internal/firewall/manager.go` — keep `FirewallManager` external shape intact but route to new RPCs. Example: manager's `InstallFirewall(containerID)` calls `adminClient.FirewallEnable(ctx, ...)`. This lets B1 CLI callers keep working until Task 6 deletes them.
9. Update `authz.go` registered-methods test.
10. Rename `TestAdminHandler_*` → `TestHandler_*` throughout `handler_test.go` (Task 1 carryover).
11. Integration tests (authored — committed, not run):
    - `FirewallEnable` drift case: fake Docker resolver returns a different cgroup path than stored; assert warning logged + fresh ID written.
    - `FirewallEnable` container gone: fake resolver returns `!exists`; assert `FailedPrecondition`.
    - `FirewallBypass` timer expiry: stub time; assert drift-guarded Enable fires.
    - Full enroll → bypass → auto-restore flow end-to-end.
    - E2E scenarios against real Docker (file-level coverage; defer execution).

### Acceptance Criteria (agent-run)

```bash
go build ./...
go vet ./...
go vet ./test/e2e/...
make test
go test ./api/admin/... -v
go test ./internal/controlplane/firewall/... -v
```

### Deferred to final host-side review

- `go test ./test/e2e/firewall_test.go -timeout 10m`
- `./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @`

### Wrap Up

1. Update Progress Tracker: Task 5 -> `complete`.
2. Key Learnings — this is the highest-risk task, document anything surprising.
3. Run all six review subagents.
4. Commit: `feat(admin): scope-correct firewall surface to 13 methods with drift guard`
5. **STOP.** Handoff:

> **Next agent prompt:** "Continue CP Initiative Branch 2. Read memory `cp-initiative-branch-2-firewall-migration-plan` — Task 5 is complete. Begin Task 6: Factory `f.AdminClient` + command migration + delete `firewall.Manager`."

---

## Task 6: Factory `f.AdminClient` + command migration + delete `firewall.Manager`

**Creates/modifies:**
- MODIFY: `internal/cmdutil/factory.go` — delete `Firewall` field; add `AdminClient func(ctx) (adminv1.AdminServiceClient, error)`.
- MODIFY: `internal/cmd/factory/default.go` — delete `firewallFunc`; add `adminClientFunc` per Spec §2 (with package-level `ensureRunning` seam + keepalive). Remove `mobyclient` import.
- NEW: `internal/controlplane/mocks/admin_client_mock.go` (moq-generated from `adminv1.AdminServiceClient`) OR hand-written mock for command tests.
- REWIRE all 13 firewall subcommands in `internal/cmd/firewall/*.go` through `f.AdminClient` per the Spec's Blast Radius command table.
- DELETE: `internal/cmd/firewall/up.go`'s `NewCmdServe` (hidden daemon entrypoint).
- MODIFY: `internal/cmd/container/shared/container_start.go` `BootstrapServicesPostStart` flow:
  - `adminClient := cmdOpts.AdminClient(ctx)`
  - `adminClient.FirewallInit(ctx, &FirewallInitRequest{})`
  - `adminClient.FirewallAddRules(ctx, &FirewallAddRulesRequest{Rules: ProjectRules(cfg)})`
  - `adminClient.FirewallEnable(ctx, &FirewallEnableRequest{ContainerId: container, Config: cfg})`
- SWAP `f.Firewall` → `f.AdminClient` (or drop entirely) in:
  - `internal/cmd/container/{start,stop,run,restart,remove}/*.go`
  - `internal/cmd/loop/{iterate,tasks,shared}/*.go`
- UPDATE command tests: replace `FirewallManagerMock` with `AdminServiceClientMock`.
- DELETE: `internal/firewall/manager.go`, `daemon.go`, `manager_network.go`, and the adapter shims added in Task 5.
- DELETE: `internal/firewall/mocks/manager_mock.go`.
- **Test-hunter follow-up from Task 1:** split `internal/controlplane/firewall/rules_store_test.go`. Keep `TestValidateDst` + `TestEgressRulesFileFields_AllFieldsHaveDescriptions`. Migrate `TestAddRules_*` / `TestRemoveRules` / `TestAddRules_RejectsInvalidDomain` / `TestAddRules_NormalizesEmptyFields` onto the new `Stack.AddRules` / `Stack.RemoveRules` / `Stack.List` surface (or delete if covered by E2E). Drop the `fwlegacy` alias import — nothing in the CP firewall package should import `internal/firewall` after this task.
- AUTHOR (not run): broad E2E coverage across the `clawker firewall *` verbs.

**Depends on:** Task 5 (proto + handler rewritten)

### Implementation Phase

1. Read Spec §1 (Factory field), §2 (adminClientFunc), §"Blast Radius" command table.
2. Swap Factory field: `Firewall` → `AdminClient`. Update `cmdutil/CLAUDE.md`.
3. Implement `adminClientFunc` per Spec §2 exact code:
   - Package-level `var ensureRunning = controlplane.EnsureRunning` for test override.
   - Cache grpc.ClientConn; rebuild only on `TransientFailure`/`Shutdown`.
   - Dial with `keepalive.ClientParameters{Time: 30s, Timeout: 10s, PermitWithoutStream: false}`.
4. Generate or hand-write mock for `adminv1.AdminServiceClient` used by command tests.
5. Rewire each of the 13 `internal/cmd/firewall/*.go` subcommands per the table. Delete `NewCmdServe`.
6. Rewire `BootstrapServicesPostStart` flow (3 RPCs: Init, AddRules, Enable).
7. Rewire container and loop commands — drop or swap `f.Firewall`.
8. Update all command tests to use AdminServiceClientMock.
9. Delete `internal/firewall/manager.go`, `daemon.go`, `manager_network.go`, `mocks/`.
10. Execute the `rules_store_test.go` split from the bullet above — verify `grep -rn "fwlegacy\|internal/firewall\"" --include='*.go'` returns zero in `internal/controlplane/`.
11. **Author** broad E2E coverage (firewall list/add/remove, enroll, bypass, etc.). Commit; do NOT run.

### Acceptance Criteria (agent-run)

```bash
go build ./...
go vet ./...
go vet ./test/e2e/...
make test
```

### Deferred to final host-side review

- `go test ./test/e2e/... -timeout 10m`
- `./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @`
- `./bin/clawker firewall add example.com && ./bin/clawker firewall list | grep example.com`
- `./bin/clawker firewall remove example.com`

### Wrap Up

1. Update Progress Tracker: Task 6 -> `complete`.
2. Key Learnings.
3. Run all six review subagents.
4. Commit: `refactor(cli): swap f.Firewall for f.AdminClient and delete firewall.Manager`
5. **STOP.** Handoff:

> **Next agent prompt:** "Continue CP Initiative Branch 2. Read memory `cp-initiative-branch-2-firewall-migration-plan` — Task 6 is complete. Begin Task 7: Break-glass `clawker controlplane up/down/status` CLI."

---

## Task 7: Break-glass `clawker controlplane up/down/status` CLI

**Creates/modifies:**
- NEW: `internal/cmd/controlplane/` package with `controlplane.go` (parent), `up.go`, `down.go`, `status.go`, + `_test.go` siblings + `CLAUDE.md`.
- MODIFY: `internal/clawker/cmd.go` (or wherever root command assembles) to register the new parent command.
- REGENERATE: `docs/cli-reference/` via `go run ./cmd/gen-docs --doc-path docs --markdown --website`.
- AUTHOR (not run): E2E coverage for the new `clawker controlplane` verbs.

**Depends on:** Task 6 (f.AdminClient available)

### Implementation Phase

1. Read Spec §"`internal/cmd/controlplane/` (NEW)" + INV-B2-008.
2. Implement parent command with usage examples.
3. `controlplane up`: `controlplane.EnsureRunning(ctx, dc, cfg, log)`; print success.
4. `controlplane down`: `controlplane.Stop(ctx, ...)`; print warning that Envoy/CoreDNS may still be running; suggest `clawker firewall down` first.
5. `controlplane status`: HTTP probe `/healthz` on AdminPort; best-effort `adminClient.FirewallStatus` RPC (tolerate if CP is down — print "CP down, firewall state unknown").
6. Command tests using `docker/mocks.FakeClient` + `AdminServiceClientMock`.
7. Register parent in root command.
8. Regenerate docs.
9. **Author** E2E coverage for `controlplane up/down/status`. Commit; do NOT run.

### Acceptance Criteria (agent-run)

```bash
go build ./...
go vet ./...
go vet ./test/e2e/...
make test
go test ./internal/cmd/controlplane/... -v
bash scripts/check-claude-freshness.sh
```

Verify `docs/cli-reference/clawker_controlplane*.md` exist via `git status`.

### Deferred to final host-side review

- `./bin/clawker controlplane up`
- `./bin/clawker controlplane status`
- `./bin/clawker controlplane down`
- `./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @`

### Wrap Up

1. Update Progress Tracker: Task 7 -> `complete`.
2. Key Learnings.
3. Run all six review subagents.
4. Commit: `feat(cli): add clawker controlplane up/down/status break-glass commands`
5. **STOP.** Handoff:

> **Next agent prompt:** "Continue CP Initiative Branch 2. Read memory `cp-initiative-branch-2-firewall-migration-plan` — Task 7 is complete. Begin Task 8: Delete `internal/firewall/` + final cleanup + docs."

---

## Task 8: Delete `internal/firewall/` + final cleanup + docs

**Creates/modifies:**
- DELETE: `internal/firewall/` directory entirely (code, tests, mocks, CLAUDE.md, embed stubs — whatever survived prior tasks).
- DELETE: `cfg.FirewallPIDFilePath` accessor in `internal/config/config.go` + `consts.go`.
- REGENERATE: `internal/config/mocks/config_mock.go` via `go generate ./...`.
- AUDIT + DELETE if unused: `cfg.FirewallLogFilePath` accessor (OQ-B2-004).
- AUDIT + UPDATE: `internal/hostproxy/daemon.go`, `internal/controlplane/firewall/ebpf/types.go`, `cmd/clawker-cp/main.go`, `cmd/coredns-clawker/main.go` — remove any stale firewall imports.
- MODIFY: `.claude/rules/docker-client.md` — remove `internal/firewall` from exception list.
- MODIFY: `.claude/rules/envoy.md` — update paths `internal/firewall/envoy.go` → `internal/controlplane/firewall/envoy_config.go`.
- MODIFY: `.claude/rules/dependency-placement.md` — remove `internal/firewall` row from Current Package Layout table.
- UPDATE CLAUDE.md files:
  - DELETE: `internal/firewall/CLAUDE.md`
  - UPDATE: `internal/cmd/firewall/CLAUDE.md` (drop `serve`; note gRPC transport)
  - UPDATE: `internal/cmd/factory/CLAUDE.md` (replace `firewallFunc` with `adminClientFunc`)
  - UPDATE: `internal/cmdutil/CLAUDE.md` (Factory field list)
  - UPDATE: `internal/controlplane/CLAUDE.md` (core-only now; remove firewall-specific prose including the "AdminService RPCs (admin_handler.go)" header left stale by Task 1)
  - NEW / EXPAND: `internal/controlplane/firewall/CLAUDE.md` (full — Handler, Stack, cgroup, rules_store, certs, Envoy/CoreDNS config, ebpf/, invariants, test patterns)
- UPDATE: `.claude/docs/ARCHITECTURE.md` (firewall subsystem now CP-owned).
- UPDATE: `.claude/docs/KEY-CONCEPTS.md` (remove `FirewallManager`; add `firewall.Handler`, `firewall.Stack`, `firewall.EBPFCgroupPath`, `controlplane.AgentWatcher`, `f.AdminClient`).
- UPDATE: `.correctless/specs/cp-initiative/CLAUDE.md` — Current State section (firewall gone, ownership inverted).
- UPDATE: `docs/threat-model.mdx` — expanded TB-002; DooD language; MITM CA now CP-owned.
- REGENERATE: `docs/cli-reference/` via `go run ./cmd/gen-docs --doc-path docs --markdown --website`.
- UPDATE: `README.md` if it referenced firewall architecture or daemon.
- UPDATE: `.serena/memories/cp-initiative-status.md` — mark Branch 2 complete.

**Depends on:** Tasks 1–7 (entire migration)

### Implementation Phase

1. Delete `internal/firewall/` with `git rm -r internal/firewall/`.
2. Delete `FirewallPIDFilePath` accessor from `config.Config` interface + `ConfigMock`.
3. Audit `FirewallLogFilePath`: grep for callers. If only the deleted daemon used it, delete the accessor and any call sites; roll firewall log into the CP's main log file.
4. Grep for any lingering `internal/firewall` imports: `grep -rn "schmitthub/clawker/internal/firewall" --include="*.go"` — must be zero.
5. Update rules files.
6. Update all CLAUDE.md files — the `internal/controlplane/firewall/CLAUDE.md` is the centerpiece; draft it fully per Spec §"CLAUDE.md / rules files affected".
7. Update architecture + key-concepts docs.
8. Update threat model.
9. Regenerate CLI reference docs.
10. Run `bash scripts/check-claude-freshness.sh` — zero staleness warnings.
11. Final agent sweep: `go build ./... && go vet ./... && go vet ./test/e2e/... && make test`.

### Acceptance Criteria (agent-run)

```bash
# Deletion confirmed
test ! -d internal/firewall
grep -rn "schmitthub/clawker/internal/firewall" --include="*.go" . | wc -l   # must be 0
grep -n "FirewallPIDFilePath" internal/config/config.go                       # must be 0

# Quality gates
go build ./...
go vet ./...
go vet ./test/e2e/...
make test
bash scripts/check-claude-freshness.sh

# Docs fresh
git status docs/cli-reference/   # should be clean after regeneration
```

### Deferred to final host-side review (the "initiative review" run)

```bash
make test-all
./bin/clawker run @ --detach
./bin/clawker firewall status
./bin/clawker firewall add example.com
./bin/clawker firewall list
./bin/clawker firewall remove example.com
./bin/clawker controlplane status
./bin/clawker container stop @
go test ./test/e2e/... -timeout 10m
```

### Wrap Up

1. Update Progress Tracker: Task 8 -> `complete`.
2. Key Learnings — full retrospective on the migration.
3. Run all six review subagents — this is the final pass.
4. Commit: `chore(firewall): delete internal/firewall/ — migration complete`
5. Update `.serena/memories/cp-initiative-status.md` — Branch 2 done, ready for Branch 3.
6. **STOP.** Present final handoff:

> **Branch 2 agent work complete.** The firewall package is gone. The CP owns firewall state, container lifecycle, and the watcher. CLI calls go through `f.AdminClient(ctx)` and hit the 13-method scope-corrected AdminService. All E2E tests are authored and committed — user now runs the final host-side review (all deferred `bin/clawker ...` smokes + `go test ./test/e2e/... -timeout 10m` + `make test-all`). Branch 3 (daemon consolidation — hostproxy + socketbridge under CP, Docker events subscription replacing watcher polling) can begin after the host-side review signs off. Open a PR against `main` when ready.
