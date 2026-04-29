# Task 05 — agentdial: result struct, log fields, FD leak ceiling, ring buffer + tests

**Status**: pending
**Claimed by**: —
**Blocks**: —
**Blocked by**: 01, 04

## Findings covered

- **Y3** — `establishWithRetry` returns 8-tuple `(*ClientConn, Stream, int, string, string, string, bool, bool)`. Two booleans `gone`+`ok` carry distinct outcomes; `(ok=true, gone=true)` is illegal but compiles.
- **S1** — `agentdial/dialer.go:380` — `grpc.NewClient` is non-blocking; real failure surfaces in `client.Session(ctx)` at L386. The "dial %s: %w" wrap misleads operators triaging connect_retry log lines.
- **S3** — `agentdial/dialer.go:234-236` — `conn.Close()` failure logged but loop continues. No FD-leak ceiling — accumulates indefinitely on broken keepalive.
- **S9** — `agentdial/dialer.go:770-784` — `publish` logs informer Upsert errors but execution continues. `runDial` has shutdown-skip logic at L243-246 but other publish sites (e.g. publishConnecting) still log Error during shutdown.
- **S15** — `agentdial/dialer.go:515-517` — `consumeAnnounceSlot` logs at Warn when err is NOT `ErrSlotInvalid`. Per Registry contract, Consume returns only that or success — other err means a registry impl regression and deserves Error.
- **S10 (dial half)** — `agentdial/subscribe.go:84-96` — `panicTimes` slice grows unbounded under sustained panic rate just under threshold.
- **T2** — `agentdial/dialer.go` (809 lines) + `subscribe.go` zero unit tests. Untested: `establishWithRetry` retry/backoff, `verifyChainOnly` cert validation, `recordRegistryProvenance` cross-check, Subscribe informer event filtering.

## Decisions

1. **Y3**: Result struct + outcome enum. Single return value, single switch at caller, no string sentinels in `drainStream`.
2. **S1**: Split log fields — `dial_target` (the addr from `d.dial`) + `stream_err` (from `Session()` open). Operator triage now matches reality.
3. **S3**: Track close-error count; bail dial loop after threshold (e.g. 5 consecutive close failures). Surface as informer attribute on the SessionEvent (see Task #4 — the typed event includes a `Reason` or similar).
4. **S9**: Route every `publish*` call through a single ctx-aware skip helper (`publishOrSkipOnShutdown(ctx, ...)`). Centralize the L243-246 logic.
5. **S15**: Promote non-`ErrSlotInvalid` Consume err path to `log.Error`.
6. **S10**: Replace `panicTimes []time.Time` slice with a fixed-size ring buffer (size = `subscribePanicWindowMaxHits`). Constant memory regardless of panic rate.
7. **T2**: Add `dialer_test.go` and `subscribe_test.go` with full coverage of the four areas above.

## Affected files

| File | Change |
|------|--------|
| `internal/controlplane/agentdial/dialer.go` | All six dialer-side decisions above. |
| `internal/controlplane/agentdial/subscribe.go` | Ring buffer (S10). Migrate to per-kind subscribe API from Task #4. |
| `internal/controlplane/agentdial/dialer_test.go` | NEW — establishWithRetry retry/backoff, verifyChainOnly cert chain, recordRegistryProvenance cross-check, FD-leak threshold. |
| `internal/controlplane/agentdial/subscribe_test.go` | NEW (or expand existing) — Subscribe informer event filtering with new per-kind events. |
| `internal/controlplane/agentdial/CLAUDE.md` (if present) | Document outcome enum + FD-leak threshold semantics. |

## Implementation plan

1. **Migrate to new informer API** (Task #4 must be complete). Update all `publish*` to use kind-specific `UpsertAgentSession`. Drop bare-string SessionLifecycle constants in favor of typed enum from informer package.

2. **Define result types** for `establishWithRetry`:
   ```go
   type establishOutcome int
   const (
       outcomeSuccess establishOutcome = iota
       outcomeContainerGone
       outcomeRetryExhausted
       outcomeCtxDone
   )

   type establishResult struct {
       Conn      *grpc.ClientConn
       Stream    clawkerdv1.ClawkerdService_SessionClient
       Agent     string
       Project   string
       Addr      string
       Attempt   int
       Outcome   establishOutcome
   }
   ```
   Refactor `establishWithRetry` to return `(establishResult, error)`. Remove the 8-tuple. Caller in `runDial` becomes a single switch on `Outcome`.

3. **Split log fields in attemptLog** (S1):
   ```go
   d.log.Warn().
       Str("dial_target", addr).
       Err(streamErr).Msg("open Session stream")
   ```
   Drop the "dial %s: %w" wrap. The dial step never errors with `grpc.NewClient`; the stream open is where transport failure surfaces.

4. **FD-leak ceiling** (S3):
   - Add `closeErrCount int` field on `Dialer` (per-target in the dedup map, or process-global — measure pragmatism vs precision; **per-target is cleaner**).
   - On `conn.Close()` error in `runDial`, increment counter.
   - On successful `conn.Close`, reset counter.
   - When counter exceeds `closeErrThreshold` (e.g. 5), bail the dial loop for this target. Emit a `SessionLifecycleFailed` informer event with `Reason: "fd-leak-ceiling: too many close failures"`.

5. **publishOrSkipOnShutdown helper** (S9):
   ```go
   func (d *Dialer) publishOrSkipOnShutdown(ctx context.Context, ev SessionEvent) {
       if ctx.Err() != nil { return }
       if err := d.inf.UpsertAgentSession(ctx, ev); err != nil && ctx.Err() == nil {
           d.log.Error().Err(err).Str("container_id", ev.ContainerID).Msg("informer upsert failed")
       }
   }
   ```
   Replace every `d.publish` call with `d.publishOrSkipOnShutdown`. Delete the L243-246 ad-hoc skip in `runDial`.

6. **Promote consume error level** (S15):
   ```go
   if err != nil {
       if errors.Is(err, agentslots.ErrSlotInvalid) {
           d.log.Warn().Err(err)...
       } else {
           d.log.Error().Err(err)...  // Registry contract regression
       }
   }
   ```

7. **Ring buffer for panicTimes** (S10):
   - Replace `var panicTimes []time.Time` with `var panicTimes [subscribePanicWindowMaxHits]time.Time` and a `head int` index.
   - On panic, `panicTimes[head] = now; head = (head + 1) % len(panicTimes)`.
   - On window check, count entries within `cutoff` from current ring contents (linear scan over fixed-size array — O(window-max) but window-max is bounded).
   - When count >= `subscribePanicWindowMaxHits`, terminate.

8. **Tests** (T2):
   - `TestEstablishWithRetry_SuccessOnFirstAttempt` — outcomeSuccess, attempt=1.
   - `TestEstablishWithRetry_BackoffSequence` — fail N times, succeed; verify attempt count + backoff intervals (use injected clock).
   - `TestEstablishWithRetry_RetryExhausted` — fail forever; outcomeRetryExhausted.
   - `TestEstablishWithRetry_CtxCanceledMidRetry` — outcomeCtxDone.
   - `TestEstablishWithRetry_ContainerGoneFromRegistry` — outcomeContainerGone (registry returns ErrUnknownAgent mid-retry).
   - `TestVerifyChainOnly_AcceptsValidChain` — leaf signed by CA, CA in roots.
   - `TestVerifyChainOnly_RejectsUntrustedRoot` — leaf signed by unknown CA.
   - `TestVerifyChainOnly_RejectsExpiredLeaf` — fixed clock past cert.NotAfter.
   - `TestRecordRegistryProvenance_MismatchLogsAndFails` — registry CN doesn't match peer cert CN.
   - `TestRecordRegistryProvenance_HappyPath` — full match.
   - `TestSubscribe_FiltersOnContainerKind` — emit a SessionEvent (own producer) and a ContainerEvent; subscribe receives only what it asked for. (Or: confirm that agentdial's Subscribe is now AgentSession-specific via Task #4 — adjust accordingly.)
   - `TestPanicRingBuffer_BoundedMemory` — emit > window-max panics; assert ring buffer state stays bounded; consumer terminates after threshold.
   - `TestFDLeakCeiling_BailsAfterThreshold` — inject N close errors; verify outcome=Failed with Reason mentioning "fd-leak-ceiling".

## Verification

```bash
go build ./...
go vet ./internal/controlplane/agentdial/...
go test ./internal/controlplane/agentdial/... -race -v

# Confirm 8-tuple is gone
grep -n 'establishWithRetry' internal/controlplane/agentdial/dialer.go
# signature must show single return value (plus error)

# Confirm bare-string SessionLifecycle constants gone
grep -n 'SessionLifecycleConnecting\|SessionLifecycleConnected\|SessionLifecycleFailed\|SessionLifecycleBroken' internal/controlplane/agentdial/

make test
```

## Dependencies

- **Task #1**: agentdial calls `agentregistry.Registry.Lookup` and `EvictByContainerID` — uses the new interface signatures.
- **Task #4**: agentdial publishes via per-kind `UpsertAgentSession` and subscribes via `SubscribeContainer`. Bare-string SessionLifecycle replaced with typed enum from informer package.

## Risks / gotchas

- **Big file** (809 lines). Read it in full before editing. The architectural praise from the type-design analyzer was about the existing TOCTOU-defense + jitter-backoff + dedup map structure — preserve that.
- **`drainStream` consumes the deprecated string sentinel `"ctx_done"`** (per type analyzer's note). After Y3, that sentinel disappears — `drainStream` reads outcome enum or signals via channel.
- **`recordRegistryProvenance`** is the load-bearing trust boundary (registry CN vs peer cert CN). Tests must cover both legs.
- **In-process gRPC test infrastructure**: use `grpc.NewServer` + `bufconn` for `establishWithRetry` tests so they don't bind real ports. Pattern matches what Task #8 will do for the listener.
- **Clock injection**: backoff tests need a fake clock. Inject `clock func() time.Time` as a Dialer option (test-only) or use the existing pattern if Dialer already supports it.
- **Don't accidentally break the dedup map** — concurrent `runDial` invocations for the same container_id should still dedupe.
- **The `Reason` field** on the new `SessionEvent` (Task #4) becomes load-bearing for FD-leak surfaceability.

## Reference reading

- `internal/controlplane/agentdial/dialer.go` (current 809-line file)
- `internal/controlplane/agentdial/subscribe.go` (current subscribe + panicTimes pattern)
- `internal/controlplane/agentregistry/subscribe.go` (sibling — has the same panicTimes pattern; Task #6 mirrors this fix)
- Task #1 file (new agentregistry.Registry interface)
- Task #4 file (new informer per-kind API)
- `internal/auth/cp/` (CP outbound client cert helpers used by `verifyChainOnly`)

## Resolution

(Filled in on completion.)

- Commit SHA:
- Notes:
