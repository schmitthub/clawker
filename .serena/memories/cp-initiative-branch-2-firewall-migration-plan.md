# CP Initiative — Branch 2: CP Owns the Firewall (Implementation Plan)

**Branch:** `feat/firewall-cp-migration`
**Parent memory:** `cp-initiative-status`
**Spec:** `.correctless/specs/cp-initiative/branch-2-cp-owns-firewall.md`
**Context doc:** `.correctless/specs/cp-initiative/CONTEXT.md`

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Relocate firewall package files into `internal/controlplane/firewall/` (pure move) | `pending` | — |
| Task 2: Add `firewall.Stack` + `firewall/cgroup.go` helpers | `pending` | — |
| Task 3: Add `controlplane/bootstrap.go` — host-side `EnsureRunning` | `pending` | — |
| Task 4: `AgentWatcher` + CP self-shutdown + restart policy `on-failure` | `pending` | — |
| Task 5: Protocol migration — proto rewrite + handler rewrite to 13-method scope-corrected surface | `pending` | — |
| Task 6: Factory `f.AdminClient` + command migration + delete `firewall.Manager` | `pending` | — |
| Task 7: Break-glass `clawker controlplane up/down/status` CLI | `pending` | — |
| Task 8: Delete `internal/firewall/` + final cleanup + docs | `pending` | — |

## Key Learnings

(Agents append here as they complete tasks)

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task.
2. Update the Progress Tracker in this memory.
3. Append any key learnings to the Key Learnings section.
4. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents to review this task's changes, then fix any and all findings.
5. Commit all changes from this task with a descriptive commit message.
6. Present the handoff prompt from the task's Wrap Up section to the user.
7. Wait for the user to start a new conversation with the handoff prompt.

Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### Background

Branch 2 completes the ownership inversion of clawker's firewall stack. Today `internal/firewall/manager.go` (1,715 LoC) is a god package that bootstraps the control plane as if the CP were a firewall dependency; `internal/firewall/daemon.go` (696 LoC) runs a host-side PID-file daemon that holds lifecycle authority. Both are replaced by CP-side equivalents.

Additionally, B1's proto surface (`api/admin/v1/admin.proto`) had scope inversions that a prior agent introduced sloppily: per-container `Install/Remove` overlapped with the per-container enrollment job `Enable`; per-container `Remove` was really a global teardown. B2 corrects this to a 13-method scope-correct surface (see spec §8 and Context table).

**Package itself is deleted.** `internal/firewall/` does not survive the merge. Files migrate to `internal/controlplane/` (CP core) and `internal/controlplane/firewall/` (NEW firewall subpackage). The `FirewallManager` interface is deleted entirely. Commands call `f.AdminClient(ctx) (adminv1.AdminServiceClient, error)` directly.

### Testing Policy (READ THIS FIRST)

**TDD is disabled on this project.** See `.correctless/learnings/tdd-phase-disabled.md` for the full post-mortem. Branch 1 ran `/ctdd` with a subagent and produced garbage unit tests; the next agent saw tests pass and skipped most requirements. User spent 12+ hours rescuing it.

**What this branch uses instead:**
- Integration tests + E2E over real Docker (`test/e2e/`, `test/whail/`).
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
- `internal/controlplane/admin_handler.go` — B1 AdminHandler (moves + renames to `firewall/handler.go` in Task 1)
- `internal/controlplane/ebpf/` — eBPF subsystem (moves to `firewall/ebpf/` in Task 1)
- `internal/firewall/` — entire package deleted in Task 8
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
- **Drift guard**: Every `FirewallEnable` resolves container_id → fresh cgroup_path via Docker API before writing BPF state (INV-B2-016). The existing `resolveBypassCgroupID` in `admin_handler.go:267–311` is the reference implementation — extract into a shared helper in Task 5.
- **Domain handler embedding**: `adminServer` in `server.go` embeds `*firewall.Handler` so Go method promotion surfaces all 13 methods at the composite. Future branches embed `*monitor.Handler`, `*hostproxy.Handler`, etc.

### Rules

- Read `CLAUDE.md`, relevant `.claude/rules/` files, and package CLAUDE.md before starting each task.
- Use Serena tools for code exploration — read symbol bodies only when needed.
- Use deepwiki + Context7 for external library docs before guessing API.
- All new code must compile (`go build ./...`), pass `go vet ./...`, pass `make test`, and preserve `clawker run @` functionality at each task boundary.
- Follow the per-task acceptance gates — a task that builds but regresses E2E is a failed task.
- Do not skip integration tests or rationalize away security boundary tests.
- Never hand-edit moq-generated mocks — regenerate via `go generate ./...`.
- Use `Config` interface accessors (`cfg.FirewallDataSubdir()`, etc.) — never hardcode paths.

---

## Task 1: Relocate firewall package files into `internal/controlplane/firewall/` (pure move, no semantic change)

> **Note on `docker.Client.Info`**: `whail.Engine` embeds `client.APIClient` directly (`pkg/whail/engine.go:34`), and `internal/docker.Client` composes `whail.Engine`, so `Info(ctx) (system.Info, error)` is already promoted end-to-end — no whail/docker changes required. The only test-infra gap is `whailtest.FakeAPIClient`, which embeds a nil `*client.Client` for unexported methods and does NOT explicitly stub `Info`. Task 5 (where `DetectCgroupDriver` is wired into Handler init — via CP-side `internal/docker.Client` with its existing label/name machinery) adds an `InfoFn func(context.Context) (system.Info, error)` field on `FakeAPIClient` and the dispatching method (~10 lines, identical to the existing `ContainerCreateFn` etc. pattern). `internal/docker/mocks/helpers.go` can gain a matching `SetupInfo(info system.Info)` helper if the call pattern shows up in multiple tests.

**Creates/modifies:**
- NEW: `internal/controlplane/firewall/` subpackage + `CLAUDE.md`
- MOVE: `internal/controlplane/admin_handler.go` → `internal/controlplane/firewall/handler.go` (rename `AdminHandler` → `firewall.Handler`; keep B1 semantics, only the type name changes)
- MOVE: `internal/controlplane/admin_handler_test.go` → `internal/controlplane/firewall/handler_test.go`
- MOVE: `internal/controlplane/ebpf/` → `internal/controlplane/firewall/ebpf/` (package rename)
- MOVE: from `internal/firewall/` into `internal/controlplane/firewall/`:
  - `envoy.go` → `envoy_config.go` (package rename to `firewall`)
  - `coredns.go` → `coredns_config.go`
  - `certs.go`
  - `rules.go` + `types.go` → merged into `rules_store.go`
  - `network.go` (swap raw moby for `*docker.Client`; drop the now-unused `ensureNetwork` helper)
  - `coredns_embed.go` → `embed_coredns.go`
  - `testdata/corefile_basic.golden` → `testdata/`
  - `assets/coredns-clawker` → `assets/`
  - Sibling `_test.go` files for each moved source
- MOVE: CP-core embeds to `internal/controlplane/`:
  - `internal/firewall/cp_embed.go` → `internal/controlplane/embed_cp.go`
  - `internal/firewall/ebpf_embed.go` → `internal/controlplane/embed_ebpf.go`
  - `internal/firewall/assets/{clawker-cp,ebpf-manager}` → `internal/controlplane/assets/`
- UPDATE: sentinels + `HealthTimeoutError` → `internal/controlplane/firewall/errors.go`
- UPDATE: imports in `internal/dnsbpf/`, `cmd/clawker-cp/main.go` for the `ebpf/` relocation
- NEW: `internal/controlplane/firewall/CLAUDE.md`

**Depends on:** nothing — pure file moves. `docker.Client` already exposes `Info` via embedding; no prereq needed.

### Implementation Phase

1. Read Spec §"Current State Inventory" + §"Target Structure" for the exact file-mapping table.
2. Create the subpackage skeleton: `internal/controlplane/firewall/` + empty `CLAUDE.md` stub (expanded in Task 5).
3. Move files with `git mv` to preserve blame. Update package declarations (`package firewall` → stays `firewall`; internal/controlplane/admin_handler moves INTO `firewall` and becomes `package firewall`).
4. Rename type `AdminHandler` → `Handler` using Serena's `rename_symbol` or `gopls rename` (NOT sed — preserve comment/docstring references).
5. Update ALL importers of moved packages:
   - `internal/dnsbpf/` — import path `internal/controlplane/ebpf` → `internal/controlplane/firewall/ebpf`
   - `cmd/clawker-cp/main.go` — same + Handler construction uses `firewall.NewHandler` (was `NewAdminHandler`)
   - `internal/firewall/manager.go` — temporarily keeps importing `internal/controlplane/firewall` for moved types; this is OK because manager is deleted in Task 8
6. Swap raw moby for `*docker.Client` in the moved `network.go`. Drop the `ensureNetwork` helper (whail's `EnsureNetwork` container option handles it).
7. Update any CLAUDE.md files touched in the moves to reflect new paths.
8. Regenerate any moq mocks affected by the moves: `cd internal/controlplane/firewall && go generate ./...` (if go:generate directives exist on moved interfaces).

### Acceptance Criteria

```bash
go build ./...
go vet ./...
make test
go test ./test/e2e/firewall_test.go -run TestFirewallEnforcement_E2E -timeout 5m
./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @
```

All tests must pass with B1 semantics intact — this task only moves files, it does NOT change behavior.

### Wrap Up

1. Update Progress Tracker: Task 1 -> `complete`.
2. Append to Key Learnings any file-mapping surprises.
3. Run review subagents as listed in Context Window Management.
4. Commit: `refactor(firewall): relocate package files into internal/controlplane/firewall/ (pure move)`
5. **STOP.** Present handoff:

> **Next agent prompt:** "Continue CP Initiative Branch 2. Read memory `cp-initiative-branch-2-firewall-migration-plan` — Task 1 is complete. Begin Task 2: Add `firewall.Stack` + `firewall/cgroup.go` helpers."

---

## Task 2: Add `firewall.Stack` + `firewall/cgroup.go` helpers (new code, not yet wired)

**Creates/modifies:**
- NEW: `internal/controlplane/firewall/stack.go` — `Stack` type managing Envoy + CoreDNS lifecycle via DooD
- NEW: `internal/controlplane/firewall/stack_test.go`
- NEW: `internal/controlplane/firewall/cgroup.go` — `DetectCgroupDriver`, `EBPFCgroupPath`, `ResolveContainerID`
- NEW: `internal/controlplane/firewall/cgroup_test.go`
- NEW: `internal/controlplane/firewall/status.go` — internal `Status` struct (used by Stack and later by FirewallStatus RPC)

**Depends on:** Task 1 (firewall subpackage must exist)

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
6. Integration test for Stack against real Docker (use `test/e2e/harness/` or test/e2e pattern).
7. Unit tests for cgroup helpers using stubbed `docker.Client.FakeClient`.

### Acceptance Criteria

```bash
go build ./...
go vet ./...
make test
go test ./internal/controlplane/firewall/... -v
go test ./test/e2e/... -run TestFirewallStack -timeout 10m   # if integration test added
./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @
```

### Wrap Up

1. Update Progress Tracker: Task 2 -> `complete`.
2. Append to Key Learnings.
3. Run review subagents.
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

### Acceptance Criteria

```bash
go build ./...
go vet ./...
make test
go test ./internal/controlplane/... -v
./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @
```

New `controlplane.EnsureRunning` is unused by production code yet — `firewall.Manager.EnsureRunning` still owns the CP lifecycle path. Task 6 cuts over.

### Wrap Up

1. Update Progress Tracker: Task 3 -> `complete`.
2. Key Learnings.
3. Review subagents.
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
6. Integration test (E2E) in `test/e2e/`:
   - Start CP with zero agents → observe `(30s × 2) + 60s` grace → assert container exited (0) and NOT restarted (restart policy is `on-failure`).
   - Pre-seed BPF maps with stale entries; start CP; assert maps empty before first RPC.

### Acceptance Criteria

```bash
go build ./...
go vet ./...
make test
go test ./internal/controlplane/... -v
go test ./test/e2e/... -run TestCPSelfShutdown -timeout 5m
go test ./test/e2e/... -run TestCPStartupCleanup -timeout 5m
./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @
```

### Wrap Up

1. Update Progress Tracker: Task 4 -> `complete`.
2. Key Learnings.
3. Review subagents.
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
- NEW: shared drift resolver extracted from `admin_handler.go:267–311` `resolveBypassCgroupID` — callable from both `FirewallEnable` and bypass timer restore.
- MODIFY: `internal/controlplane/server.go` — `adminServer` struct embeds `*firewall.Handler`; register as sole `AdminServiceServer`.
- MODIFY: `internal/controlplane/startup.go` — `CPStartupOrchestrator` constructs Stack + firewall.Handler.
- MODIFY: `internal/firewall/manager.go` — adapter shims for B1-named methods that now call the renamed RPCs internally. **This is temporary** — deleted in Task 6.
- UPDATE: `authz.go` registered-methods test to reflect all 13 methods via `AdminServiceServer` reflection.
- UPDATE: `internal/controlplane/firewall/CLAUDE.md` with the 13-method surface.

**Depends on:** Tasks 2, 3, 4, 5 (subpackage + Stack + bootstrap + watcher all in place)

### Implementation Phase

1. Read Spec §5, §8, §9, §Context table. Study `resolveBypassCgroupID` in B1's `admin_handler.go:267–311`.
2. Rewrite `admin.proto`:
   - Drop B1's 7 short-named RPCs.
   - Add 13 prefixed RPCs per Spec §8.
   - Remove `cgroup_path` field from all per-container requests.
   - `FirewallRemoveRequest` and `FirewallInitRequest` are empty (global).
   - `FirewallEnableRequest` carries `container_id` + `ContainerConfig`.
3. Run `make proto`.
4. Extract drift resolver from `admin_handler.go:267–311` into `internal/controlplane/firewall/drift.go` (or embed in handler.go). Shared by Enable and bypass timer.
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
10. Integration tests:
    - `FirewallEnable` drift case: fake Docker resolver returns a different cgroup path than stored; assert warning logged + fresh ID written.
    - `FirewallEnable` container gone: fake resolver returns `!exists`; assert `FailedPrecondition`.
    - `FirewallBypass` timer expiry: stub time; assert drift-guarded Enable fires.
    - Full enroll → bypass → auto-restore flow end-to-end.
    - E2E against real Docker.

### Acceptance Criteria

```bash
go build ./...
go vet ./...
make test
go test ./api/admin/... -v
go test ./internal/controlplane/firewall/... -v
go test ./test/e2e/firewall_test.go -timeout 10m
./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @
```

### Wrap Up

1. Update Progress Tracker: Task 5 -> `complete`.
2. Key Learnings — this is the highest-risk task, document anything surprising.
3. Review subagents.
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
- DELETE: `internal/firewall/manager.go`, `daemon.go`, and the adapter shims added in Task 5.
- DELETE: `internal/firewall/mocks/manager_mock.go`.

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
9. Delete `internal/firewall/manager.go`, `daemon.go`, `mocks/`.
10. Run full E2E — this is the atomic cutover moment.

### Acceptance Criteria

```bash
go build ./...
go vet ./...
make test
go test ./test/e2e/... -timeout 10m
./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @
./bin/clawker firewall add example.com && ./bin/clawker firewall list | grep example.com
./bin/clawker firewall remove example.com
```

### Wrap Up

1. Update Progress Tracker: Task 6 -> `complete`.
2. Key Learnings.
3. Review subagents.
4. Commit: `refactor(cli): swap f.Firewall for f.AdminClient and delete firewall.Manager`
5. **STOP.** Handoff:

> **Next agent prompt:** "Continue CP Initiative Branch 2. Read memory `cp-initiative-branch-2-firewall-migration-plan` — Task 6 is complete. Begin Task 7: Break-glass `clawker controlplane up/down/status` CLI."

---

## Task 7: Break-glass `clawker controlplane up/down/status` CLI

**Creates/modifies:**
- NEW: `internal/cmd/controlplane/` package with `controlplane.go` (parent), `up.go`, `down.go`, `status.go`, + `_test.go` siblings + `CLAUDE.md`.
- MODIFY: `internal/clawker/cmd.go` (or wherever root command assembles) to register the new parent command.
- REGENERATE: `docs/cli-reference/` via `go run ./cmd/gen-docs --doc-path docs --markdown --website`.

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

### Acceptance Criteria

```bash
go build ./...
go vet ./...
make test
go test ./internal/cmd/controlplane/... -v
./bin/clawker controlplane up
./bin/clawker controlplane status
./bin/clawker controlplane down
./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @
bash scripts/check-claude-freshness.sh
```

Verify `docs/cli-reference/clawker_controlplane*.md` exist and match command output.

### Wrap Up

1. Update Progress Tracker: Task 7 -> `complete`.
2. Key Learnings.
3. Review subagents.
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
  - UPDATE: `internal/controlplane/CLAUDE.md` (core-only now; remove firewall-specific prose)
  - NEW / EXPAND: `internal/controlplane/firewall/CLAUDE.md` (full — Handler, Stack, cgroup, rules_store, certs, Envoy/CoreDNS config, ebpf/, invariants, test patterns)
- UPDATE: `.claude/docs/ARCHITECTURE.md` (firewall subsystem now CP-owned).
- UPDATE: `.claude/docs/KEY-CONCEPTS.md` (remove `FirewallManager`; add `firewall.Handler`, `firewall.Stack`, `firewall.EBPFCgroupPath`, `controlplane.AgentWatcher`, `f.AdminClient`).
- UPDATE: `.correctless/specs/cp-initiative/CLAUDE.md` — Current State section (firewall gone, ownership inverted).
- UPDATE: `docs/threat-model.mdx` — expanded TB-002; DooD language; MITM CA now CP-owned.
- REGENERATE: `docs/cli-reference/` via `go run ./cmd/gen-docs --doc-path docs --markdown --website`.
- UPDATE: `README.md` if it referenced firewall architecture or daemon.
- UPDATE: `.serena/memories/cp-initiative-status.md` — mark Branch 2 complete.

**Depends on:** Tasks 1–8 (entire migration)

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
11. Final sweep: `go build ./... && go vet ./... && make test && make test-all`.

### Acceptance Criteria

```bash
# Deletion confirmed
test ! -d internal/firewall
grep -rn "schmitthub/clawker/internal/firewall" --include="*.go" . | wc -l   # must be 0
grep -n "FirewallPIDFilePath" internal/config/config.go                       # must be 0

# All quality gates
go build ./...
go vet ./...
make test
make test-all
bash scripts/check-claude-freshness.sh

# Functional
./bin/clawker run @ --detach
./bin/clawker firewall status
./bin/clawker firewall add example.com
./bin/clawker firewall list
./bin/clawker firewall remove example.com
./bin/clawker controlplane status
./bin/clawker container stop @

# Docs fresh
git status docs/cli-reference/   # should be clean after regeneration
```

### Wrap Up

1. Update Progress Tracker: Task 8 -> `complete`.
2. Key Learnings — full retrospective on the migration.
3. Review subagents — this is the final pass.
4. Commit: `chore(firewall): delete internal/firewall/ — migration complete`
5. Update `.serena/memories/cp-initiative-status.md` — Branch 2 done, ready for Branch 3.
6. **STOP.** Present final handoff:

> **Branch 2 complete.** The firewall package is gone. The CP owns firewall state, container lifecycle, and the watcher. CLI calls go through `f.AdminClient(ctx)` and hit the 13-method scope-corrected AdminService. Branch 3 (daemon consolidation — hostproxy + socketbridge under CP, Docker events subscription replacing watcher polling) can begin. Open a PR against `main` when ready.
