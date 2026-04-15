# Firewall Action Queue — Implementation Initiative

**Branch:** `feat/firewall-queue`
**PRD Reference:** `.serena/memories/firewall-queue-prd.md`

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Queue primitive + unit tests | `complete` | Opus 4.6 |
| Task 2: Wire queue into Handler (all 13 RPCs) | `pending` | — |
| Task 3: CP shutdown integration + E2E tests | `pending` | — |

## Key Learnings

### Task 1 (2026-04-15)

- **sync.Cond over buffered channel for the work queue.** The initiative sketch suggested a buffered channel (size 64). That deadlocks: Submit must hold the mutex to guard `closed`, but if the buffer is full it blocks while Close needs the same mutex to `close(ch)`. Switched to a `sync.Mutex` + `sync.Cond` + `[]actionItem` ring. Submit appends + `Signal()` under the mutex (never blocks on the worker); Close sets `closed`, `Broadcast()`, cancels ctx, waits on `done`.
- **Slice-leak trap when re-slicing the buffer head.** `q.buffer = q.buffer[1:]` advances the slice header but leaves the old `actionItem` in the backing array — holding refs to the `ActionFunc` closure (and everything it captures) for the queue's lifetime. Pop via `q.buffer[0] = actionItem{}` before re-slicing (`popHead` helper).
- **Trailing-reconcile-after-teardown must execute (deviation from PRD).** PRD model drops `[R, R, R, T, R]`'s trailing R. This would let a user who runs `firewall down` then `firewall remove evil.com` see the removal silently lost — the rules store persists across teardown so the next `firewall up` would re-allow `evil.com`. The initiative locks this in; Task 1's `CoalescingMatrix/R_R_R_T_R` test case pins the behavior. `coalesces` stops at the first kind mismatch, so trailing items always get their turn.
- **Close must cancel the ctx, but drained closures still execute with it.** `Close` sets `closed=true` → `Broadcast` → releases mutex → `cancel()` → waits on `done`. Every drained closure gets the already-cancelled ctx. This is by design (well-behaved closures can observe shutdown) but it creates a subtle contract: a ctx-aware drained closure will deliver `ctx.Err()` to its submitter, which is technically a non-`ErrClosed` error — still a non-silent outcome. Task 2 RPC closures need to decide per-closure whether to honor ctx during drain.
- **Panic recovery in the worker is load-bearing for the drain invariant.** Without `defer recover()` a panicking closure kills the worker goroutine, which strands `q.done` and every coalesced peer's reply channel. `execute` converts the panic into `ActionResult{Err: %w ErrClosurePanic}` so peers get a clean failure and the worker survives.
- **Coalesces on the type, not as a package-level predicate.** Moved `coalesces(k)` to `(k ActionKind) Coalesces() bool` so the coalescing property is discoverable from the `ActionKind` definition and future kinds force a decision.
- **Hold the worker with a gate in tests instead of sleeping.** Submitting a slow `ActionBringup` behind a `gate` lets the test stack coalescing peers into the buffer deterministically. Without the gate, scheduler timing decides whether coalescing fires — sleep-based tests here would be flaky under load.
- **`t.Fatal` inside a queued closure is a footgun.** Go's `t.Fatal` from a non-test goroutine calls `runtime.Goexit` on the worker — which means the panic-recovery path doesn't fire (it's not a panic), `q.done` is stranded, and every future test hangs. `TestActionQueue_PostCloseSubmitReturnsErrClosed` uses a signal channel instead.

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents to review this task's changes, then fix any and all findings
5. Commit all changes from this task with a descriptive commit message
6. Present the handoff prompt from the task's Wrap Up section to the user
7. Wait for the user to start a new conversation with the handoff prompt

Each task is self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### Wire-level invariant — no breaking changes for CLI callers

The gRPC surface (13 RPCs in `api/admin/v1/admin.proto`) stays as-is. Every existing response field keeps its current meaning. The queue is a server-side change; clients don't need to know it exists.

**What clients observe post-change:**

1. Calls still block on the response (gRPC is already synchronous per-call). Coalescing happens server-side and is invisible — three rapid AddRules calls each return their own response; they just share the underlying reconcile.
2. Latency for a given call may be slightly higher under load because the queue serializes work. Target: sub-second under normal load, worst-case bounded by a single Stack.Reload cycle (~200–500ms).
3. `stack_restarted` field semantics on `FirewallAddRulesResponse` / `FirewallRemoveRulesResponse` / `FirewallReloadResponse` gets sharper: `true` = regen + restart happened; `false` = stack was down, rule is persisted but no live restart occurred. Today the code always sets it to `true` after a successful Reload — the new semantics are a refinement, not a breaking change.
4. Error responses gain optional `errdetails.ErrorInfo` entries with `Reason` fields naming specific sentinels. Clients that ignore details still work (the status code + message remain the same class of information). Clients that read details get per-step remediation.

**CLI-side changes (small, scoped):**

1. Add a **15-second context timeout** per RPC call. Previously calls could hang indefinitely on a stuck CP; the queue makes unbounded hangs more visible because actions may wait behind other queued work.
2. **Progress indicator while waiting.** Every firewall CLI command shows an iostreams goroutine spinner on stderr while the RPC is in flight (auto-disabled in non-TTY contexts). Without it the CLI looks hung during the worst-case few seconds.
3. For `AddRules` / `RemoveRules` / `Reload` / `RotateCA` responses: when `stack_restarted=false`, print an informational note ("rule persisted; firewall not running, will take effect on next `clawker firewall up`") instead of the current "stack reloaded" message. The response is still `err=nil` — this is a successful outcome.
4. For error responses: decode `errdetails.ErrorInfo` entries (if present) and print a remediation line per Reason. Fall back to the status message if no details are attached.

**What's changing for callers, precisely:**

| Layer | Change |
|-------|--------|
| `.proto` file | **Rename** all 13 `Firewall*Response` messages to `Firewall*Result` to match the `(Result, error)` Go convention. **Add** `bool stack_restarted = 1` to `FirewallRotateCAResult`. No other field edits, no field-number changes on retained fields. |
| Generated Go wire types | Renamed (`*adminv1.FirewallAddRulesResponse` → `*adminv1.FirewallAddRulesResult` etc.). All consumers update their imports/type references. Mechanical but widespread. |
| Semantic meaning of `stack_restarted` on AddRules/RemoveRules/Reload/**RotateCA** responses | **This is the real behavior change.** Today the server always sets it `true` after a successful Reload (dummy signal). After this change: `true` = stack was up and got reloaded; `false` = stack was down, pre-Submit work is persisted but no live restart occurred. CLI must interpret both cases. |
| Error responses | Additive: gain `errdetails.ErrorInfo` entries with sentinel `Reason` strings. Clients that ignore details still work (same status code + message). Clients that decode details print per-sentinel remediation hints. |
| Internal Go Result types in firewall package | New plain structs (`StackReloadResult{Restarted bool}`, empty markers like `TeardownResult{}`). Handler-side only — distinct from proto types. Handler maps Result fields into the proto Result message when returning from the RPC. |

### The Core Idea (read this before the PRD)

**Take what the Handler does today, put a FIFO queue in front of the expensive parts.** That's it.

Every `Firewall*` RPC today calls into the Handler and mutates state (stack restart, eBPF maps, rules store) synchronously. Rapid-fire calls collide — a second restart fires while Envoy+CoreDNS are mid-restart.

The fix is a single-goroutine worker that pulls action closures off a channel one at a time. Every RPC submits to the queue and blocks until the worker runs it. Coalesce consecutive same-kind reconciles so three rapid `AddRules` calls trigger one stack restart, not three.

**Decouple persistent state mutations from stack restarts.** This is the key refinement over the PRD. Rule-CRUD RPCs (AddRules, RemoveRules) do their **store write synchronously in the RPC handler, before Submit**. RotateCA does its **CA+per-domain cert regeneration synchronously, pre-Submit**. Reload has no pre-Submit work — it's a pure "reconcile the running stack" signal. All three kinds share a single queued closure: `reconcileStackClosure`, which is pure "apply current on-disk state to the running stack, if running":

```
AddRules / RemoveRules RPC handler:
  validate args
  store.Set(mutation) + store.Write           // synchronous, pre-Submit — rule is durable from here on
  ar := <-queue.Submit(ActionReconcile, reconcileClosure)
  rr := ar.Value.(StackReloadResult)
  // caller sets response.StackRestarted = rr.Restarted

RotateCA RPC handler:
  regenerate CA keypair + per-domain certs on disk   // synchronous, pre-Submit
  ar := <-queue.Submit(ActionReconcile, reconcileClosure)
  rr := ar.Value.(StackReloadResult)

Reload RPC handler:
  // no pre-Submit work — Reload is a pure "reconcile signal"
  ar := <-queue.Submit(ActionReconcile, reconcileClosure)
  rr := ar.Value.(StackReloadResult)

reconcileClosure (shared):
  if stack NOT running:
    return StackReloadResult{Restarted: false}, nil   // pre-Submit work already committed; nothing else to do
  stack.Reload                                            // regenerates envoy.yaml+Corefile from store, restarts containers
  ebpf.SyncRoutes(routes from current store)
  return StackReloadResult{Restarted: true}, nil
```

Every closure returns a Result value on success, never a bare `nil`. The queue carries it back via `ActionResult{Value: result, Err: nil}`. Callers map the Result to proto response fields or whatever else they need.

**Universal closure contract** — every queued closure returns `(Result, error)` — a Result on clean success, a wrapped sentinel error on failure. Never both, never nil-both. So every caller can report both "what went well, exactly" (Result) and "what went wrong, and why" (sentinel chain).

```go
// Success: return SomeResult, nil
// Failure: return nil, fmt.Errorf("%w: %v", ErrXxx, cause)
// Multi-step failure: return nil, errors.Join(wrappedSentinel1, wrappedSentinel2, ...)
```

Caller dispatch:

```go
res := <-queue.Submit(kind, closure)
if res.Err != nil {
    // errors.Is each sentinel the caller cares about.
    // CLI prints a remediation line per matched sentinel.
    return nil, toStatus(res.Err)
}
result := res.Value.(SomeResult)
// map result to proto response fields, log it, etc.
```

**Defining Result types per closure.** Results are plain Go structs in the firewall package — no enum variants, just fields. When there's no info to return, the Result is `struct{}` (empty marker). When there is, the struct carries named fields. The RPC handler reads the Result struct's fields and populates the corresponding proto Result message (the proto type is renamed `Firewall*Response` → `Firewall*Result` across the board — see "What's changing for callers" above).

```go
// Closure Result types live in internal/controlplane/firewall/ (Go-side).
// They are distinct from the proto-generated types; the handler maps one
// to the other when returning from the RPC.

type StackReloadResult struct{ Restarted bool }   // true = regen + restart happened; false = stack was down, file write only
type InitResult      struct{ EnvoyIP, CoreDNSIP, NetworkID string }
type TeardownResult  struct{}
type EnableResult    struct{}
type DisableResult   struct{}
type BypassResult    struct{}
type ListRulesResult struct{ Rules []config.EgressRule }
type StatusResult    struct{ /* mirrors firewall.Status */ }
type SyncRoutesResult struct{ Applied int }
type ResolveResult   struct{ Addresses []string }
```

Handler pattern (AddRules):

```go
func (h *Handler) FirewallAddRules(ctx context.Context, req *adminv1.FirewallAddRulesRequest) (*adminv1.FirewallAddRulesResult, error) {
    // validate + synchronous store write pre-Submit
    rules := ProtoRulesToConfig(req.GetRules())
    for _, r := range rules {
        if err := ValidateDst(r.Dst); err != nil {
            return nil, toStatus(fmt.Errorf("%w: %v", ErrRuleInvalid, err))
        }
    }
    added, err := h.addRulesToStore(rules)
    if err != nil {
        return nil, toStatus(fmt.Errorf("%w: %v", ErrRuleStoreWrite, err))
    }
    // Submit reconcile closure — queue, coalesce, serialize
    ar := <-h.queue.Submit(ActionReconcile, h.reconcileStackClosure)
    if ar.Err != nil {
        return nil, toStatus(ar.Err)
    }
    rr := ar.Value.(StackReloadResult)
    return &adminv1.FirewallAddRulesResult{
        AddedCount:     int32(added),
        StackRestarted: rr.Restarted,
    }, nil
}

func (h *Handler) reconcileStackClosure(ctx context.Context) (any, error) {
    running, err := h.stack.IsRunning(ctx)
    if err != nil {
        return nil, fmt.Errorf("%w: %v", ErrStackProbe, err)
    }
    if !running {
        return StackReloadResult{Restarted: false}, nil   // clean success, file write already persisted
    }
    reloadErr := h.stack.Reload(ctx)
    rules, _ := NormalizeAndDedup(h.store.Read().Rules)
    syncErr := h.ebpf.SyncRoutes(routesFromRules(rules))
    if syncErr != nil {
        syncErr = fmt.Errorf("%w: %v", ErrRouteSync, syncErr)
    }
    if err := errors.Join(reloadErr, syncErr); err != nil {
        return nil, err
    }
    return StackReloadResult{Restarted: true}, nil
}
```

**Rule: add a struct field only when an existing consumer uses it.** For this initiative, only `StackReloadResult.Restarted` is load-bearing (the CLI needs it to distinguish reloaded vs file-write-only success). Every other closure returns a `struct{}` today; fields get added later when something needs them.

**Defining sentinels per closure.** Step-level failures get their own sentinels so `errors.Is` can identify them. Reconcile already has a catalog in the Sentinel section above. Other closures add their own as needed during Task 2 — keep them in `internal/controlplane/firewall/errors.go` next to the existing ones. CLI-side remediation hints live alongside each sentinel.

**SyncRoutes RPC note:** unclear whether it should run the full `reconcileStackClosure` (Stack.Reload + ebpf.SyncRoutes) or a narrower ebpf-only closure. It's a break-glass route resync — arguably no config regen needed. Implementer should decide during Task 2 based on current call sites in the tree. Either way, it's a queued action; just pick the right closure.

Because store writes happen pre-Submit, coalescing is trivial and safe — all pending mutations are already persisted by the time the worker wakes; three coalesced submitters see the same regen cycle capture all three writes. No dropped mutations, no pulling actions forward.

**When the stack is down, a rule mutation is just a file write.** No regeneration. No restart. No eBPF sync (there are no enrolled containers anyway). The caller gets success — the rule is durable and will be picked up on the next `firewall up`.

**No new business logic.** No `RestoreEnforcement` action type. No new methods on Stack/eBPF/Store (except possibly gating `Stack.Reload`'s `ensureConfigs` behind a running-check to skip wasted regeneration on a down stack).

RPC body shapes:

| RPC | Pre-Submit work (synchronous) | Queued closure |
|-----|-------------------------------|----------------|
| AddRules, RemoveRules | validate, store write | `reconcileStackClosure` |
| RotateCA | regenerate CA + per-domain certs on disk | `reconcileStackClosure` |
| Reload | none | `reconcileStackClosure` |
| SyncRoutes | none | `reconcileStackClosure` (or narrower ebpf-only — see note in section above) |
| Init | none | `EnsureRunning` body (bring stack up) |
| Remove | none | teardown body (stop containers, delete generated files, FlushAll eBPF; rules store preserved) |
| Enable, Disable, Bypass | none | existing per-container body |
| ListRules, Status, ResolveHostname | none | existing read body |

### Defense-in-depth invariant: queued mutations always execute and always surface a result

Once the queue accepts a submission, that action will run. Callers always block on the result channel and receive success or the error from the action. This gives two guarantees:

1. **No silent drops.** A user who runs `firewall remove evil.com` cannot have that mutation discarded — even mid-shutdown. Either it executes (possibly failing with a real error like "stack is down"), or it was never accepted (submission during or after Close returns `ErrClosed` before the action was queued).
2. **No silent success.** The CLI exits non-zero when the queued action fails, so the user sees "this change didn't land" instead of a misleading green success.

`Close` is drain-then-stop: every submission accepted before `Close` returns runs to completion. This preserves the invariant across CP shutdown.

### Sentinel errors + CLI remediation

Errors are only useful if the user knows what to do next. Every failure mode that maps to a concrete user action needs a typed sentinel so the CLI can print an actionable hint (not a raw stack trace).

**Error catalog** (define in `internal/controlplane/firewall/errors.go`; extend the existing `errors.go` alongside `ErrEnvoyUnhealthy` etc.):

| Sentinel | Where raised | Surfaced when | CLI hint |
|----------|--------------|---------------|----------|
| `ErrCPNotRunning` | CLI dial layer (`f.AdminClient`) | gRPC dial fails (connection refused, container not found) | "control plane is not running — run `clawker controlplane up`" |
| `ErrQueueClosed` | Queue | Submit after `Close` returns | "control plane is shutting down, retry in a moment" |
| `ErrFirewallNotInitialized` | Handler RPC (pre-Submit) | RPC requires a live stack AND stack is down. Applies to: Enable, Disable, Bypass. **Does NOT apply to rule-CRUD** — those succeed with file-write semantics when the stack is down (see "decouple rule mutations from stack restarts") | "firewall is not running — run `clawker firewall up`" |
| `ErrContainerGone` | Handler RPC (resolver) | Resolver returns `exists=false` (replaces ad-hoc `FailedPrecondition`) | "container no longer exists: <cid>" |
| `ErrRuleInvalid` | Handler RPC (pre-Submit) | `ValidateDst` rejects a rule | "invalid rule: <reason>" |
| `ErrRuleStoreWrite` | Handler RPC (pre-Submit) | Store Set/Write fails (disk full, flock contention) | "rule change was not persisted: <cause>. state is unchanged, safe to retry" |
| `ErrCertRegen` | Handler RPC (pre-Submit, RotateCA) | CA keypair or per-domain cert write fails | "CA rotation failed during cert regeneration: <cause>. stack was not restarted; old certs still active" |
| `ErrStackProbe` | Queued closure | Can't determine if containers are running (Docker API unreachable) | "cannot determine firewall stack state: <cause>. check Docker daemon health" |
| `ErrConfigRegen` | Queued closure (Stack.Reload internal) | `ensureConfigs` fails — rule healing, cert regen, envoy.yaml write, or Corefile write failed | "stack config regeneration failed: <cause>. rule is persisted; stack was NOT restarted" |
| `ErrEnvoyRestart` | Queued closure (Stack.Reload internal) | Envoy container failed to restart or failed health check | "Envoy restart failed: <cause>. run `clawker container logs clawker-envoy`" |
| `ErrCoreDNSRestart` | Queued closure (Stack.Reload internal) | CoreDNS container failed to restart or failed health check | "CoreDNS restart failed: <cause>. run `clawker container logs clawker-coredns`" |
| `ErrStackUnhealthy` | Queued closure (Stack.Reload internal) | Containers started but `WaitForHealthy` timed out | "firewall containers started but are not healthy: <cause>. inspect: `clawker firewall status`" |
| `ErrRouteSync` | Queued closure | `ebpf.SyncRoutes` failed — BPF map update errored | "BPF route map sync failed: <cause>. stack is running with potentially stale routes. rerun `clawker firewall reload`" |

**No swallowed step failures.** Multiple steps can fail in one reconcile cycle (e.g. both Envoy and CoreDNS restart fail, then route sync fails). Use `errors.Join` to combine them so the caller sees every failed step, not just the first. CLI iterates the joined error via `errors.Is(err, ErrEnvoyRestart)` / `errors.Is(err, ErrCoreDNSRestart)` / etc. and prints a remediation line per matched sentinel.

The reconcile closure returns `StackReloadResult{Restarted: bool}` on clean success, or `nil + wrapped sentinel(s)` on failure. See the `reconcileStackClosure` example in "Defining Result types per closure" above.

`Stack.Reload` needs internal restructure for this to work — today it returns the first error and drops the rest. Task 2 should convert it to collect per-step failures and return via `errors.Join`. Specifically:

1. `ensureConfigs` failures wrap `ErrConfigRegen` (with sub-context: which file, which step of regen)
2. Envoy `restart` + `WaitForHealthy` failures wrap `ErrEnvoyRestart` or `ErrStackUnhealthy`
3. CoreDNS same pattern with `ErrCoreDNSRestart`
4. Top-level returns `errors.Join(envoyErr, corednsErr, ...)` not the first non-nil

**Cross-wire preservation:** `errdetails.ErrorInfo` can only carry one Reason string. To preserve the full Join chain across gRPC, attach a `Reason` per detected sentinel (multiple `ErrorInfo` entries on the same status). CLI reads all ErrorInfo details and dispatches one remediation line per Reason. If this gets awkward, fall back to putting the first sentinel in `Reason` and the full `err.Error()` string in the status message — CLI still parses `err.Error()` for a best-effort multi-sentinel display, but `errors.Is` loses fidelity across the wire. Prefer multi-ErrorInfo.

**Three-layer failure model:**

| Layer | Failure class | Sentinel flavor | Store mutation? |
|-------|---------------|-----------------|-----------------|
| CLI dial | CP container down / unreachable | `ErrCPNotRunning` — never enters queue | N/A (never reached server) |
| RPC pre-Submit | Validation, store write, resolver | `ErrRuleInvalid`, `ErrRuleStoreWrite`, `ErrContainerGone`, `ErrFirewallNotInitialized` | No — caller sees clean failure |
| Queue closure | Stack restart / eBPF sync | `ErrStackRestart` | Already committed — partial success |

The three-layer split is load-bearing: store writes must commit before the queue runs, so a closure failure means "the rule is durable but the running stack didn't reload." CLI must print that honestly.

**Cross-wire mechanism:** gRPC carries sentinels via `status.Error` with a matching code (e.g. `codes.Unavailable` for `ErrCPNotRunning`, `codes.FailedPrecondition` for `ErrFirewallNotInitialized`) plus an `errdetails.ErrorInfo` whose `Reason` field is a stable string constant (e.g. `"CP_NOT_RUNNING"`, `"FIREWALL_NOT_INITIALIZED"`). Define `Reason` constants next to the sentinels. Server wrapper reads the returned sentinel and attaches the matching status+details. CLI decodes via `status.FromError` + `status.Details` and dispatches on Reason.

Avoid string-matching gRPC error messages — stable constants + typed details are the contract.

**Security invariant around sentinels:** a rule CRUD mutation that succeeded in the store but failed to apply to the live stack is NOT a total failure. The store is authoritative. Return `ErrStackRestart` with partial-success context so the CLI can print: "rule stored, but stack reload failed — rule is persisted and will take effect on next reload". Do not swallow the error; do not claim total success.

### Key Decisions (from scoping conversation)

| Topic | Decision |
|-------|----------|
| Queue scope | All 13 RPCs route through queue (reads too — read-after-write consistency) |
| RPC response | Sync — caller blocks on queue completion, returns result to CLI |
| Coalescing | Consecutive same-kind rule-mutation actions (AddRules, RemoveRules, Reload, RotateCA, SyncRoutes) coalesce into one stack restart. Different kinds never coalesce |
| `FirewallRemove` behavior | **Preserve `egress-rules.yaml`**. Wipe `envoy.yaml`, `Corefile`, all eBPF pinned state. Current code wipes the store — change it |
| `SyncRoutes` bug | `FirewallAddRules` / `RemoveRules` today call `stack.Reload` but NOT `ebpf.SyncRoutes`. CLAUDE.md says they should. Fix as part of the Reconcile closure |
| Bypass timer | Timer goroutine submits `ebpf.Enable(cgroupID)` to the queue instead of calling directly. No new action type — just queued invocation of existing method |
| Bypass across CP restart | Dies on restart (current behavior, INV-B2-013 `CleanupStaleBypass`) |
| `Notify` resilience | Submit API is close-safe — post-Close calls return a pre-closed channel carrying `ErrClosed`. Never panics |
| Drain order | `reconciler.Close` → `CancelBypass` → `Stack.Stop` → `ebpf.FlushAll` |

### Key Files

- `internal/controlplane/firewall/handler.go` — 13 RPCs, Handler struct, HandlerDeps
- `internal/controlplane/firewall/stack.go` — Envoy+CoreDNS lifecycle
- `internal/controlplane/firewall/ebpf/` — eBPF loader + EBPFManager interface
- `cmd/clawker-cp/main.go` — CP startup + drain callback composition
- `test/e2e/firewall_test.go` — E2E firewall scenarios

### Design Patterns

- **Factory DI**: Handler is constructed in `cmd/clawker-cp/main.go` via `NewHandler(HandlerDeps{...})`. Queue will be a new field on HandlerDeps.
- **Interface seams**: `StackLifecycle`, `EBPFManager`, `ContainerResolver` — all swappable in tests. Queue primitive should have its own interface so Handler tests can swap a synchronous fake.
- **Close-safe channel idioms**: guard with sync.Mutex + closed bool, not `recover()` tricks.

### Rules

- Read `CLAUDE.md`, `internal/controlplane/CLAUDE.md`, `internal/controlplane/firewall/CLAUDE.md`, `.claude/rules/envoy.md`, `.claude/rules/testing.md` before starting.
- Use Serena tools for code exploration — read symbol bodies only when needed.
- All tests must pass. Unit tests mandatory. E2E required for Task 3.
- No TDD (per `MEMORY.md` — feedback_no_tdd_clawker.md). Write tests alongside or after code, not before.
- Follow existing test patterns: `FakeClient`, `configmocks`, `EBPFManagerMock`, injected fakes via HandlerDeps.

---

## Task 1: Queue primitive + unit tests

**Creates:** `internal/controlplane/firewall/queue.go`, `internal/controlplane/firewall/queue_test.go`
**Depends on:** Nothing — pure primitive.

### Implementation

Define a single-goroutine FIFO queue that executes action closures serially with same-kind coalescing.

```go
// internal/controlplane/firewall/queue.go

package firewall

type ActionKind int

const (
    ActionUnknown ActionKind = iota
    ActionBringup       // FirewallInit
    ActionTeardown      // FirewallRemove
    ActionReconcile     // AddRules, RemoveRules, Reload, RotateCA, SyncRoutes
    ActionRead          // ListRules, Status, ResolveHostname
    ActionEnable        // FirewallEnable
    ActionDisable       // FirewallDisable, bypass timer restore
    ActionBypass        // FirewallBypass
)

// ActionQueue serializes action execution behind a single goroutine.
// Consecutive same-kind actions with coalescing=true are drained and
// share a single execution; all submitters receive the same result.
type ActionQueue struct { ... }

func NewActionQueue(log *logger.Logger) *ActionQueue

// ActionResult is what the closure produces. Value is the action's
// Result (e.g. StackReloadResult, InitResult) — caller type-asserts.
// On clean success Value is non-nil and Err is nil; on failure
// Value is nil and Err carries wrapped sentinel(s).
type ActionResult struct {
    Value any
    Err   error
}

// Submit enqueues an action closure and returns a channel that will
// receive one ActionResult. Safe to call during shutdown — a submission
// accepted before Close returns is guaranteed to execute. Submissions
// after Close returns receive a pre-closed channel carrying
// ActionResult{Err: ErrClosed}. Never panics.
func (q *ActionQueue) Submit(kind ActionKind, fn func(context.Context) (any, error)) <-chan ActionResult

// Close drains the queue and stops the worker goroutine. Every action
// that was submitted before Close returns runs to completion and its
// submitter receives the result. Submissions after Close returns
// receive ErrClosed. Idempotent.
func (q *ActionQueue) Close() error
```

**Coalescing rule:** consecutive actions of the same kind are drained and coalesced into a single execution. All drained submitters receive the same result. The worker drains by peeking the queue head after each dequeue; if the kind matches, pull it off, take its submitter channel, continue peeking.

**Which kinds coalesce:** only `ActionReconcile`. The five RPCs mapped to it (`AddRules`, `RemoveRules`, `Reload`, `RotateCA`, `SyncRoutes`) all regenerate stack state from the current rules store — three rapid calls produce identical output, so one execution serves all of them. Other kinds do not coalesce: per-container actions (`Enable`, `Disable`, `Bypass`) carry distinct `container_id` args per call; `Bringup`/`Teardown`/`Read` execute individually.

**PRD coalescing table** (Task 1 must reproduce this behavior via test fixtures — R = ActionReconcile, T = ActionTeardown):

| Queue Contents     | Execution                          |
|--------------------|------------------------------------|
| [R]                | 1 reconcile                        |
| [R, R, R]          | 1 reconcile                        |
| [R, R, T]          | 1 reconcile, 1 teardown            |
| [R, R, R, T, R]    | 1 reconcile, 1 teardown, 1 reconcile |
| [T]                | 1 teardown                         |
| [R, T, R]          | 1 reconcile, 1 teardown, 1 reconcile |

**Deviation from PRD:** the PRD says teardown terminates the reconciler and trailing actions are dropped ([R, R, R, T, R] → 1 reconcile + 1 teardown, trailing R dropped). We execute trailing actions instead.

**Why (security):** the rules store persists across teardown. If the user runs `firewall down` and then `firewall remove evil.com` in quick succession, the PRD model drops the remove — and when the user later brings the firewall back up, `evil.com` is still in the store, silently allowed. The user who issued the remove thinks they're safe; they aren't. This is the same silent-drift failure mode the PRD flags for priority-based ordering, just shifted post-teardown. Trailing mutations must execute against the store even when the stack is down.

**Mechanics:** a trailing `Reconcile` on a torn-down stack mutates the store and then no-ops the stack reload (existing `Stack.Reload` returns early when containers aren't running). A subsequent `FirewallInit` brings the stack up via `EnsureRunning` and reads the current (post-mutation) store. Rules store is the source of truth; derived state (Envoy config, Corefile, eBPF maps) is always regenerated from it on bring-up.

**Context:** worker owns a long-lived `context.Context` tied to Close. Each closure gets passed this ctx. Close cancels it — in-flight action sees `ctx.Done()` and can abort if it chooses.

**Concurrency details:**
- Submit uses a `sync.Mutex` + `closed bool` flag. If closed, return a pre-closed channel carrying `ErrClosed`. If not, send on the internal channel.
- Channel is buffered (reasonable size, e.g. 64) — Submit shouldn't block the caller while worker is mid-action.
- Worker exits when its internal channel is closed (by Close).

### Tests

`queue_test.go` unit tests — no Docker, no eBPF, just the primitive with injected test closures:

- **FIFO ordering**: submit 10 actions with different kinds, each appends its ID to a shared slice; assert order.
- **Coalescing**: submit 5 Reconcile actions in rapid succession; assert the closure ran once, all 5 submitters received the same result.
- **No cross-kind coalescing**: submit Reconcile + Teardown + Reconcile; assert 3 executions.
- **Result propagation**: closure returns error; all coalesced submitters receive it.
- **Post-Close submit**: Close then Submit; receive `ErrClosed` without panic.
- **Close drains in-flight**: submit a closure that blocks on a signal channel; Close concurrently; unblock the closure; Close returns.
- **Close drains queued work** (defense-in-depth): submit 5 actions while a first slow action is running; Close concurrently; unblock the slow action; assert all 5 queued actions executed AND their submitters each received a non-ErrClosed result. No queued mutation is dropped by Close.
- **Close then Submit race**: submit concurrently with Close from many goroutines; for each submission, assert it either ran to completion with its closure's real result, or received ErrClosed — never both, never silent.
- **Race**: submit from N goroutines with `-race`; no data races.
- **Context cancel on Close**: closure checks `ctx.Done()`; Close cancels; closure sees cancellation.

### Acceptance Criteria

```bash
go test ./internal/controlplane/firewall/ -run TestActionQueue -race -v
go vet ./internal/controlplane/firewall/...
```

All queue tests pass. `go vet` clean. No lint warnings.

### Wrap Up

1. Update Progress Tracker: Task 1 → `complete`
2. Append key learnings (coalescing edge cases, close-safety implementation details, anything surprising)
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents — fix findings
4. Commit: `feat(firewall): add ActionQueue primitive with FIFO + coalescing`
5. **STOP.** Present this handoff:

> **Next agent prompt:** "Continue the firewall-queue initiative. Read the Serena memory `firewall-queue-initiative` — Task 1 is complete. Begin Task 2: Wire queue into Handler (all 13 RPCs)."

---

## Task 2: Wire queue into Handler (all 13 RPCs)

**Modifies:** `internal/controlplane/firewall/handler.go`, `internal/controlplane/firewall/handler_test.go`, `cmd/clawker-cp/main.go`
**Depends on:** Task 1 (ActionQueue primitive).

### Implementation

Every `Firewall*` RPC submits its existing body as a closure to the queue and blocks on the result channel. No business logic changes — the closures contain today's code verbatim.

**Step 0 (proto rename):** Rename all 13 `Firewall*Response` messages to `Firewall*Result` in `api/admin/v1/admin.proto`. Update the `rpc` method return types to match. Regenerate via `make proto-gen` (or the project's proto-gen target — see `MEMORY.md` → `project_proto_gen_test_dep.md`). Update every Go reference from `*adminv1.Firewall*Response` → `*adminv1.Firewall*Result` across `internal/controlplane/firewall/handler.go`, `internal/cmd/firewall/`, tests, mocks (`internal/controlplane/mocks/AdminServiceClientMock`), and any other call site. Mechanical but widespread. Run `go build ./...` + `go test ./...` to verify. Single commit is fine.

**Step 0b (proto field addition):** Add `bool stack_restarted = 1;` to `FirewallRotateCAResult` — its closure now shares `reconcileStackClosure` which produces that distinction. No other proto field additions.

**Step 1:** Add `Queue *ActionQueue` field to `HandlerDeps` and `Handler`. `NewHandler` panics if nil (like EBPF/Resolver). `cmd/clawker-cp/main.go` constructs the queue before `NewHandler` and passes it. Also define the closure Result structs in `internal/controlplane/firewall/` — `StackReloadResult{Restarted bool}`, plus empty markers (`TeardownResult{}`, etc.) for every other closure.

**Step 2:** For each of the 13 RPCs, wrap the existing body. Rule-CRUD RPCs (AddRules, RemoveRules) split store-write (synchronous, pre-Submit) from regen+restart (queued closure). RotateCA splits cert-regen (pre-Submit) from regen+restart. Reload is a pure reconcile signal. All four submit `reconcileStackClosure`. The remaining 9 RPCs move their bodies verbatim into closures with the appropriate kind.

See the canonical handler + closure example in the "Defining Result types per closure" section above — it shows:
- The full `FirewallAddRules` handler pattern (validate → store write → Submit → map `StackReloadResult` to proto response)
- The full `reconcileStackClosure` with all per-step sentinel wrapping
- Note: proto type is `*adminv1.FirewallAddRulesResult` (renamed in Step 0) and closure returns `(any, error)` where the value is a `StackReloadResult`

Action kind mapping:

| RPC | Kind |
|-----|------|
| FirewallInit | ActionBringup |
| FirewallRemove | ActionTeardown |
| FirewallAddRules | ActionReconcile |
| FirewallRemoveRules | ActionReconcile |
| FirewallReload | ActionReconcile |
| FirewallRotateCA | ActionReconcile |
| FirewallSyncRoutes | ActionReconcile |
| FirewallListRules | ActionRead |
| FirewallStatus | ActionRead |
| FirewallResolveHostname | ActionRead |
| FirewallEnable | ActionEnable |
| FirewallDisable | ActionDisable |
| FirewallBypass | ActionBypass |

**Step 3:** Fix `FirewallRemove` teardown behavior inside its closure:
- **Remove** the `store.Set(func(f *EgressRulesFile) { f.Rules = nil })` wipe.
- **Add** deletion of `envoy.yaml` and `Corefile` via `os.Remove` (paths via `consts.EnvoyConfigPath()` / `consts.CorefilePath()`). Missing-file = not an error.
- Keep: `CancelAllBypassTimers`, `stack.Stop`, `ebpf.FlushAll`, clearing `storedCgroupID` map.

**Step 4:** Fix the SyncRoutes bug. The `reconcileStackClosure` (Step 2) already calls `ebpf.SyncRoutes` after `stack.Reload`. Build the routes list via a helper `routesFromRules([]config.EgressRule) []ebpf.Route` — mirror the mapping logic already in `FirewallSyncRoutes` (see `handler.go`) and put the helper next to `NormalizeAndDedup` in `rules_store.go` so it's reusable.

**Step 4b:** Restructure `Stack.Reload` to surface step-level failures via wrapped sentinels + `errors.Join`. Today it returns the first error and drops the rest. New shape:

```go
func (s *Stack) Reload(ctx context.Context) error {
    if _, err := s.ensureConfigs(); err != nil {
        return fmt.Errorf("%w: %v", ErrConfigRegen, err)  // no point continuing — no valid configs to restart with
    }
    envoyRunning, err := s.isRunning(ctx, envoyContainerName)
    if err != nil { return fmt.Errorf("%w: %v", ErrStackProbe, err) }
    corednsRunning, err := s.isRunning(ctx, corednsContainerName)
    if err != nil { return fmt.Errorf("%w: %v", ErrStackProbe, err) }
    if !envoyRunning || !corednsRunning { return nil }

    var errs []error
    if err := s.restart(ctx, envoyContainerName); err != nil {
        errs = append(errs, fmt.Errorf("%w: %v", ErrEnvoyRestart, err))
    }
    if err := s.restart(ctx, corednsContainerName); err != nil {
        errs = append(errs, fmt.Errorf("%w: %v", ErrCoreDNSRestart, err))
    }
    if len(errs) == 0 {
        if err := s.WaitForHealthy(ctx); err != nil {
            errs = append(errs, fmt.Errorf("%w: %v", ErrStackUnhealthy, err))
        }
    }
    return errors.Join(errs...)
}
```

`ErrConfigRegen` short-circuits — if configs didn't regenerate, restarting doesn't help and would just thrash. Envoy/CoreDNS restart failures are independent and collected. `WaitForHealthy` only runs if both restarts succeeded (otherwise the partial failures are already the primary signal). Any existing Stack.Reload tests that asserted specific error strings need to move to `errors.Is(err, ErrX)`.

Non-`reconcileStackClosure` callers of `Stack.Reload` (any direct callers, search for them) need their own wrapping review — but the sentinels themselves are now load-bearing for downstream CLI remediation.

**Step 5:** Bypass timer restore. In `handler.go::bypassTimerFired`, change the direct `h.ebpf.Enable(enableID)` call to submit through the queue:

```go
done := h.queue.Submit(ActionDisable, func(ctx context.Context) error {
    return h.ebpf.Enable(enableID)
})
if err := <-done; err != nil { ... existing retry logic ... }
```

The timer goroutine still owns the retry loop — it's just calling through the queue now instead of directly touching eBPF.

**Step 6:** Sentinel errors. Extend `internal/controlplane/firewall/errors.go` with the sentinel catalog from "Sentinel errors + CLI remediation" above. Each RPC closure returns a sentinel (or a wrapped sentinel with `fmt.Errorf("%w: …", ErrFoo, …)`) on its known failure modes; the RPC wrapper translates sentinel → gRPC `status.Error` with code + `errdetails.ErrorInfo{Reason: …}`. Add a helper:

```go
// toStatus maps a firewall sentinel to a gRPC status with typed details.
// Unknown errors get codes.Internal with no details.
func toStatus(err error) error
```

CLI side (separate commit within Task 2, or split to a Task 2.5 if diff gets big — use judgement):

1. **15-second context timeout per RPC.** Wrap each `f.AdminClient(ctx)` call site with `ctx, cancel := context.WithTimeout(ctx, 15*time.Second); defer cancel()`. Keep timeouts per-command, not global, so slow commands (e.g. future streaming) aren't capped by it. If a call times out, surface it as `ErrQueueClosed`-class error ("control plane is unresponsive or overloaded; retry").
2. **Decode `errdetails.ErrorInfo`.** On any error response from AdminService, iterate details via `status.FromError(err).Details()`, type-assert each to `*errdetails.ErrorInfo`, dispatch on `Reason` to print per-sentinel remediation from the catalog. Fall back to the status message when no details are attached.
3. **Interpret `stack_restarted=false` on rule-CRUD success.** Current CLI probably prints a generic "reloaded" message. New behavior: on `stack_restarted=false`, print "rule persisted; firewall is not running, will take effect on next `clawker firewall up`". This is a success message, not an error.
4. **Remediation for common failures must be wired:** `ErrCPNotRunning` → "run `clawker controlplane up`"; `ErrFirewallNotInitialized` → "run `clawker firewall up`"; `ErrStackRestart` family (ErrEnvoyRestart, ErrCoreDNSRestart, ErrConfigRegen, ErrStackUnhealthy, ErrRouteSync) → per-sentinel hints from the catalog.
5. **Progress indicator while waiting on the RPC.** Every firewall CLI command that calls through `f.AdminClient` wraps the call in a spinner. With queue serialization, a single RPC can wait behind other queued actions + its own reconcile — worst case a few seconds. Without an indicator the CLI looks hung.
   - Use the existing `iostreams` goroutine spinner (`SpinnerFrame()` — see `.claude/rules/code-style.md` §Presentation Layer). Do NOT pull in `tui`/bubbletea for this — static-output scenario, iostreams-only.
   - Spinner renders to `ios.ErrOut` (stderr) with a contextual message, e.g. `"Adding rules..."`, `"Reloading firewall..."`, `"Bringing firewall up..."`. Spinner stops and clears the line on response (success or error).
   - **Auto-disable in non-TTY.** Check `ios.IsStderrTTY()` (or equivalent) — when stderr isn't a terminal (piped, scripted, CI), don't start the spinner at all. Call still runs normally; just no cursor-manipulating output to pollute pipes.
   - Implement once as a small helper, e.g. `cmdutil.WithSpinner(ios, message, fn func() error) error` in `internal/cmdutil/` or alongside the existing spinner helpers. Every firewall command call site uses it.

**Step 7:** Unit tests. Handler tests already use `EBPFManagerMock` + fake StackLifecycle. Add a test ActionQueue seam — use the real queue with a real goroutine; it proves the wiring. Existing Handler tests should pass after the queue is wired; update fixtures/wiring as needed.

Add these new scenarios:
- **Coalescing test**: submit 3 `FirewallAddRules` RPC calls concurrently; assert `stack.Reload` called once (via fake call counter); assert all 3 submitters received the same success result.
- **FIFO cross-kind**: submit `FirewallAddRules` then `FirewallRemove` concurrently; assert Reload runs before Stop.
- **Sentinel: firewall not initialized**: call `FirewallAddRules` with Stack.Status reporting not running; assert returned gRPC error decodes to `ErrFirewallNotInitialized` (Reason `"FIREWALL_NOT_INITIALIZED"`).
- **Sentinel: partial success on stack restart failure**: store write succeeds, stack.Reload fails; assert `ErrStackRestart` returned; assert rules store contains the new rule (persisted); assert submitter received the error (not silent success).
- **SyncRoutes fix**: `FirewallAddRules` triggers `ebpf.SyncRoutes` call (assert via `EBPFManagerMock.SyncRoutesCalls()`).

### Acceptance Criteria

```bash
go test ./internal/controlplane/firewall/... -race -v
go test ./internal/controlplane/... -race -v
go vet ./...
make pre-commit
```

All tests pass. Coalescing + FIFO tests demonstrate the new behavior. SyncRoutes bug fixed (route_map updated after rule mutations).

### Wrap Up

1. Update Progress Tracker: Task 2 → `complete`
2. Append key learnings (especially: anything subtle about passing queue ctx vs RPC ctx through the closure, test wiring gotchas, SyncRoutes route construction)
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents — fix findings
4. Commit: `feat(firewall): route all AdminService RPCs through ActionQueue`
5. **STOP.** Present this handoff:

> **Next agent prompt:** "Continue the firewall-queue initiative. Read the Serena memory `firewall-queue-initiative` — Tasks 1 and 2 are complete. Begin Task 3: CP shutdown integration + E2E tests."

---

## Task 3: CP shutdown integration + E2E tests

**Modifies:** `cmd/clawker-cp/main.go`, `test/e2e/firewall_test.go`
**Depends on:** Tasks 1 and 2.

### Implementation

**Step 1:** Wire `ActionQueue` into CP shutdown. In `cmd/clawker-cp/main.go`, the drain-to-zero callback composed around `AgentWatcher` currently calls (in order): `CancelAllBypassTimers` → `grpcServer.GracefulStop` → `stack.Stop` → `ebpf.FlushAll`.

New order:
1. `reconciler.Close()` — blocks in-flight action, rejects new submits
2. `grpcServer.GracefulStop()` — waits for in-flight RPC handlers (now returning ErrClosed from Submit) to return
3. `handler.CancelAllBypassTimers()` — stops bypass timer goroutines (they'd get ErrClosed from Submit anyway, but this prevents them spinning on retries)
4. `stack.Stop(ctx)`
5. `ebpf.FlushAll()`

Note: `stack.Stop` / `ebpf.FlushAll` run AFTER `reconciler.Close` even though they used to be driven by the Handler via the queue. Post-Close they need to run host-side (directly from `cmd/clawker-cp/main.go`) because the queue is dead. This is the "final teardown" path and bypasses the queue by design.

Alternative: submit a final Teardown closure BEFORE Close, let the queue execute it, then Close. Either works. Prefer the direct-after-Close approach — simpler to reason about, no "is this the last action?" ambiguity.

Confirm by reading the existing drain callback — it's short, composed in `cmd/clawker-cp/main.go` around `AgentWatcher`. Update it in place.

**Step 2:** E2E tests in `test/e2e/firewall_test.go`. Must go through `harness.Run` — the CLI plumbing. No direct Handler construction (see `MEMORY.md` → `feedback_e2e_definition.md`).

Scenarios to add:
- **Rapid rule mutations coalesce**: issue 5 `clawker firewall add` CLI commands in quick succession via `harness.Run`. Assert Envoy container restart count (from Docker inspect) increments by ≤ 1 after all 5 complete, not 5.
- **Mutation-then-remove ordering**: `clawker firewall remove evil.com` concurrent with `clawker firewall remove` (the stack teardown). Assert the rule removal applies to a running stack before teardown (check Envoy config file contents before teardown deletes it, or check a log marker).
- **Teardown preserves rules**: run `firewall add example.com`, `firewall remove` (teardown), `firewall up` (re-init), `firewall list`. Assert `example.com` is still listed. Today's behavior would lose it.
- **Teardown wipes generated state**: after `firewall remove` with no subsequent rule-CRUD calls, assert `envoy.yaml` and `Corefile` do not exist in `FirewallDataSubdir`. (Rule-CRUD after teardown is now a pure file write — does NOT regenerate envoy.yaml/Corefile because `reconcileStackClosure` short-circuits when the stack is down. See "Core Idea".)
- **Post-teardown rule removal takes effect** (security-critical): `firewall add evil.com`, `firewall down`, `firewall remove evil.com`, `firewall up`, `firewall list`. Assert `evil.com` is NOT listed after the sequence. This proves trailing mutations after teardown are not dropped — a user who removes a domain after shutting the firewall down must see that removal reflected when the firewall comes back up, otherwise they think they're safe when they aren't.
- **CLI remediation: firewall down**: with CP running but firewall not initialized, run `clawker firewall add example.com`. Assert CLI exits non-zero, stderr contains the remediation hint referencing `clawker firewall up`.
- **CLI remediation: CP down**: with CP container stopped, run `clawker firewall list`. Assert CLI exits non-zero, stderr contains the remediation hint referencing `clawker controlplane up`. (Note: today's CLI may auto-start CP via `cpboot.EnsureRunning`; if that's still the behavior, this test covers the failure path where EnsureRunning itself fails. Confirm behavior during implementation.)

### Acceptance Criteria

```bash
go test ./internal/controlplane/firewall/... -race -v
go test ./test/e2e/... -run TestFirewall -v -timeout 10m
make test-all
make pre-commit
```

All tests pass. E2E demonstrates coalescing behavior on a real Docker stack. Teardown preserves rules file, wipes generated configs.

**Note:** E2E runs host-side only per `MEMORY.md` → `feedback_no_e2e_in_container.md`. If the agent is in-container, flag this and defer E2E runs to the host.

### Wrap Up

1. Update Progress Tracker: Task 3 → `complete`
2. Append key learnings (especially: drain-callback order trap, E2E timing tolerance for coalescing assertions, any Docker quirks that surfaced)
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents — fix findings
4. Update documentation:
   - `internal/controlplane/firewall/CLAUDE.md` — note the ActionQueue lives between RPCs and Stack/eBPF/Store; describe coalescing; SyncRoutes is now called from the Reconcile closure
   - `internal/controlplane/CLAUDE.md` — update drain sequence to reflect `reconciler.Close` first
5. Commit: `feat(firewall): wire ActionQueue into CP shutdown drain + E2E coverage`
6. **STOP.** Initiative complete. Inform the user.
