# Task 09 — agentslots: sweep log fields + janitor race test

**Status**: complete
**Claimed by**: claude-opus-4.7
**Blocks**: —
**Blocked by**: 04
**Parallel-safe**: yes (only blocked by Task #4 informer migration)

## Findings covered

- **S11** — `agentslots/registry.go:280-293` — sweep Debug log carries only `container_id`. Operator triaging "why did this announce never get consumed" loses agent/project context.
- **T6** — `agentslots` TTL janitor has `TestRegistry_Janitor_SweepsExpiredSlots` but no "Consume races janitor sweep" test to lock down cleanup ordering.

## Decisions

1. **S11**: Add `slot.AgentName` and `slot.Project` to the Debug line. Operator triage gets full context.
2. **T6**: Add a single-shot test that triggers a sweep mid-Consume via a test hook. Deterministic ordering check.

## Affected files

| File | Change |
|------|--------|
| `internal/controlplane/agentslots/registry.go` | Add agent + project fields to sweep Debug log (L280-293 area). Add a test seam: a function-typed field on the registry struct (or via NewRegistry option) that, if non-nil, fires inside `Consume` after the slot lookup but before deletion — lets a test trigger sweep mid-Consume. Field is unexported and zero-valued in production; only tests set it. |
| `internal/controlplane/agentslots/subscribe.go` | Migrate to per-kind subscribe API from Task #4. |
| `internal/controlplane/agentslots/registry_test.go` | Add `TestRegistry_Janitor_RacesConsume` using the new test seam. |

## Implementation plan

1. **Migrate to per-kind subscribe** (Task #4 prerequisite): `inf.SubscribeContainer(filter)` instead of `inf.Subscribe(informer.Filter{...})`.
2. **Update sweep log** at L280-293 area. Find the Debug line that emits the per-slot sweep event:
   ```go
   r.log.Debug().
       Str("container_id", slot.ContainerID).
       Str("agent", slot.AgentName).
       Str("project", slot.Project).
       Msg("agentslots: sweep evicted expired slot")
   ```
3. **Add test hook** to the registry:
   ```go
   type Registry struct {
       // ... existing fields ...
       // testHookConsumeMidpoint, if non-nil, fires inside Consume after
       // the slot is found but before it is deleted. Lets tests force a
       // sweep to race the deletion. nil in production.
       testHookConsumeMidpoint func()
   }
   ```
   Inside `Consume`:
   ```go
   slot, ok := r.slots[containerID]
   if !ok { return ... }
   if r.testHookConsumeMidpoint != nil {
       r.testHookConsumeMidpoint()
   }
   delete(r.slots, containerID)
   ```
4. **Write the race test**:
   ```go
   func TestRegistry_Janitor_RacesConsume(t *testing.T) {
       r := agentslots.NewRegistry(time.Now, 0, logger.Nop())
       // Reserve a slot that has already expired (or set TTL=0)
       slot := mustReserveExpired(t, r)

       // Set up a hook that triggers a manual sweep mid-Consume
       sweepDone := make(chan struct{})
       r.SetTestHookConsumeMidpoint(func() {
           r.SweepNow()  // exposed test method or via janitor tick
           close(sweepDone)
       })

       _, err := r.Consume(slot.ContainerID, slot.Verifier)
       <-sweepDone
       // Assertion: Consume sees a consistent state — either the slot was
       // already deleted by sweep (returns ErrSlotInvalid) or Consume wins
       // (returns the slot). NOT a panic, NOT a double-delete.
       require.True(t, err == nil || errors.Is(err, agentslots.ErrSlotInvalid))
   }
   ```
5. Update `agentslots/CLAUDE.md` if this task lands before Task #12. (Task #12 rewrites the file anyway — coordinate to avoid double-edits. If Task #12 lands first, just rebase your sweep-log change into the rewritten file.)

## Test requirements

- `TestRegistry_Janitor_RacesConsume` — per above. Must pass with `-race`.
- Existing `TestRegistry_Janitor_SweepsExpiredSlots` continues to pass.

## Verification

```bash
go build ./...
go vet ./internal/controlplane/agentslots/...
go test ./internal/controlplane/agentslots/... -race -v

# Confirm log line has new fields
grep -A 3 'sweep evicted' internal/controlplane/agentslots/registry.go

make test
```

## Dependencies

- **Task #4** (informer split): subscribe.go uses kind-specific Subscribe.

## Risks / gotchas

- **Test hook leaking into production**: the `testHookConsumeMidpoint` field is unexported and nil-in-production. Don't add a public setter that consumers could accidentally call. Use an internal-only helper file (e.g. `registry_testhook.go` with a build tag, OR an `internal/agentslots/testhook` subpackage). Simplest: just an unexported field that test files in the same package set directly via reflection or via a `set_testhook_test.go` helper. **Recommendation: keep the hook field unexported and set it directly from `*_test.go` files in the same package** — no build tag needed, no public surface added.
- **Sweep ordering**: the test should not assume which goroutine wins the race — only assert that the outcome is consistent (no panic, no negative slot count, no double-delete crash). Both possible outcomes are valid.
- **`SweepNow()` test method**: if no public method exists for forcing a sweep, the test might trigger via janitor tick (slow) or call the unexported sweep method via a same-package helper. Same-package access is the cleanest.
- **Coordinate with Task #12** — both touch agentslots. Task #12 doc rewrite is independent of this code change but lands the same file dir.
- **`agentslots/CLAUDE.md` may be stale** about the slot key model (Task #12). Don't accidentally fix the doc as a side effect of this task.

## Reference reading

- `internal/controlplane/agentslots/registry.go` (sweep, Consume, janitor)
- `internal/controlplane/agentslots/subscribe.go`
- `internal/controlplane/agentslots/CLAUDE.md` (CURRENTLY STALE — see Task #12)
- Task #4 file (new informer subscribe API)
- Task #12 file (parallel doc rewrite — coordinate)

## Resolution

- Commit SHA: b0b67e7d779a8bb094700c11d334d47e7290e50d
- Notes: (S11) Added `agent` + `project` Str fields to the sweep Debug log in `registryImpl.sweep` — operators triaging "why did this announce never get consumed" now get full slot identity. (T6) Added unexported `testHookConsumeMidpoint func()` field on `registryImpl` (zero-valued in production); fires inside Consume after the slot lookup but before any delete, while the registry mutex is still held. The new `TestRegistry_Janitor_RacesConsume` uses the hook to spawn a goroutine that calls `impl.sweep()` — that goroutine queues for the same mutex, so the test exercises serialization rather than expecting nondeterministic interleaving. Asserts: Consume wins (returns the slot), no panic, no double-delete crash, slot map settles at len 0. Race-clean across 3× `-race` runs. The Task #4 informer-migration step from the original plan is **obsolete**: per `internal/controlplane/agent/CLAUDE.md` L74, agentslots is intentionally not an Overseer subscriber (TTL janitor is the sole correctness floor). No subscribe.go file exists, so no migration needed. agentslots/CLAUDE.md (Task #12) was already updated to the container_id-keyed model — left untouched per "do not accidentally fix the doc as a side effect" gotcha.
