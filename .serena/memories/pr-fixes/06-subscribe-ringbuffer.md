# Task 06 — agentregistry/subscribe: ring buffer for panicTimes

**Status**: complete
**Claimed by**: claude-opus-4.7
**Blocks**: —
**Blocked by**: 04
**Parallel-safe**: yes

## Findings covered

- **S10 (registry half)** — `agentregistry/subscribe.go:78-92` — `panicTimes []time.Time` slice grows unbounded when sustained panic rate is just under `subscribePanicWindowMaxHits` per `subscribePanicWindow`. The `cutoff` slice trim drops old entries but new entries append faster than they age out at threshold-adjacent rates → linear memory growth.

(The agentdial-side mirror of this fix is in **Task #5**.)

## Decision

Replace `panicTimes []time.Time` with a fixed-size ring buffer sized at `subscribePanicWindowMaxHits`. Constant memory regardless of panic rate. Window-check counts entries within `cutoff` from the ring contents (linear scan over fixed-size array).

## Affected files

| File | Change |
|------|--------|
| `internal/controlplane/agentregistry/subscribe.go` | Replace slice with `[subscribePanicWindowMaxHits]time.Time` array + head index. Update window-check to scan ring contents. Migrate to per-kind subscribe API from Task #4. |
| `internal/controlplane/agentregistry/subscribe_test.go` | Add panic-storm-bounded-memory test (or property-style: emit 10× window-max panics, assert memory of subscriber goroutine stays flat — usually impractical to assert directly, settle for "subscriber terminates after threshold AND no allocations growth visible via runtime.MemStats sample"). |

## Implementation plan

1. **Migrate to per-kind subscribe** (Task #4 prerequisite): `inf.SubscribeContainer(filter)` instead of `inf.Subscribe(informer.Filter{Kinds: []string{dockerevents.KindContainer}})`.

2. **Replace slice with ring buffer**:
   ```go
   var (
       panicTimes [subscribePanicWindowMaxHits]time.Time
       panicHead  int  // next write index
   )

   recordPanic := func(now time.Time) {
       panicTimes[panicHead] = now
       panicHead = (panicHead + 1) % len(panicTimes)
   }

   countWithinWindow := func(cutoff time.Time) int {
       n := 0
       for _, t := range panicTimes {
           if !t.IsZero() && t.After(cutoff) {
               n++
           }
       }
       return n
   }
   ```

3. **Use the helpers in the existing recovery loop**:
   ```go
   recordPanic(now)
   if countWithinWindow(now.Add(-subscribePanicWindow)) >= subscribePanicWindowMaxHits {
       log.Error()...
       return
   }
   ```

4. **Drop the slice trim logic** (L86-92 in current code) — no longer needed.

5. **Backoff reset semantics unchanged** — keep the L81-83 `lastPanic` check.

## Test requirements

- `TestSubscribe_PanicStormTerminatesAtThreshold` — using a delta hook that panics deterministically, verify the consumer terminates after `subscribePanicWindowMaxHits` panics within `subscribePanicWindow`.
- `TestSubscribe_PanicRecoveryResumesProcessing` — single panic, then the next delta is processed cleanly. (Probably exists already; verify still passes.)
- `TestSubscribe_PanicTimes_BoundedMemory` — emit `10*subscribePanicWindowMaxHits` panics over a longer window, assert no memory growth in panicTimes (test introspects via shrunk buffer length-equivalent or just confirms threshold logic still triggers correctly).

## Verification

```bash
go build ./...
go test ./internal/controlplane/agentregistry/... -race -v -run TestSubscribe

# Confirm slice usage replaced
grep -n 'panicTimes\s*\[\]time.Time' internal/controlplane/agentregistry/subscribe.go
# Should return zero matches
grep -n 'panicTimes\s*\[' internal/controlplane/agentregistry/subscribe.go
# Should show fixed-size array

make test
```

## Dependencies

- **Task #4** (informer split): consumer subscribes via `SubscribeContainer` — kind-specific API.

## Risks / gotchas

- **Zero-time entries in ring buffer**: an unused slot is `time.Time{}` (zero). The `countWithinWindow` helper must skip zero-times explicitly (the `!t.IsZero()` check above).
- **Subscriber-side mirror**: don't forget the agentdial sibling fix is in Task #5. If Task #5 chooses a different ring-buffer impl (e.g. wrapped in a small helper type), consider extracting a shared `panicwindow` helper into `internal/controlplane/informer` or a util package — only if both sites converge naturally; don't force premature extraction.
- **Backoff exponential reset** at L107-112 (current code) is independent of the ring buffer — preserve it.
- **The recover-and-resume comment block** at L57-70 explains WHY panic recovery exists — preserve that documentation.

## Reference reading

- `internal/controlplane/agentregistry/subscribe.go` (current implementation)
- `internal/controlplane/agentdial/subscribe.go` (sibling — Task #5 fixes the same way)
- Task #4 file (new informer per-kind subscribe API)
- Task #5 file (mirror change to verify)

## Resolution

- Commit SHA: (to be filled by commit step)
- Notes: Replaced unbounded `panicTimes []time.Time` slice with fixed-capacity `[subscribePanicWindowMaxHits]time.Time` ring buffer + head index in `agentregistry/subscribe.go`. Window-check now scans the ring counting non-zero entries past `cutoff`. Slice trim logic dropped. `subscribePanicWindowMaxHits` stays `const` (compile-time array size); the three pacing knobs (`subscribePanicBackoffMin/Max`, `subscribePanicWindow`) flipped from `const` to `var` so the new storm test can shrink them without minutes of real-time wait. Added `TestSubscribe_PanicStormTerminatesAtThreshold` + `alwaysPanicRegistry` helper — proves consumer terminates with `"panic rate exceeded ceiling"` Error log after exactly `subscribePanicWindowMaxHits` panics within the override window. Test waits for `reg.calls == threshold` before canceling so cancel doesn't drop buffered events; race-clean across 3× `-race` runs and 5× plain runs. `TestSubscribe_PanicRecoveryResumesProcessing` is already covered by existing `TestSubscribe_RecoversFromHookPanic`. Bounded-memory is structural (fixed-size array) — no separate test needed beyond the storm test.
