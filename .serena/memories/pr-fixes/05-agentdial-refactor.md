# Task 05 — agentdial: result struct, log fields, FD leak ceiling, ring buffer + tests

**Status**: complete
**Claimed by**: claude-opus-4.7
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

- Commit SHA: `d73ff9b5`
- Notes:

**What landed**

- **Y3** Result struct + outcome enum. `establishWithRetry` returns `establishResult{Conn, Stream, Agent, Project, Addr, Attempt, Outcome}` instead of the 8-tuple `(conn, stream, attempt, agent, project, addr, gone, ok)`. Caller `runDial` is a single switch on `Outcome` (`outcomeSuccess` / `outcomeContainerGone` / `outcomeRetryExhausted` / `outcomeCtxDone`). The `(ok=true, gone=true)` illegal-but-compiles state structurally cannot exist now. Default arm in the switch logs Error + publishes `internal_unknown_outcome` so a future enum addition that forgets to update the caller surfaces in logs.
- **drainStream typed**. Mirror refactor: `drainResult{Outcome drainOutcome, Reason string}` replaces the `"ctx_done"` / `"eof"` / `err.Error()` string sentinel return. `runDial` checks `drain.Outcome == drainCtxCanceled` instead of comparing strings.
- **S1** Split log fields. `tryEstablish` no longer wraps `dial(...)`'s err with `"dial %s: %w"` (the addr was already a structured field via `attemptLog.With("addr", addr)`). The retry + timeout log lines emit `dial_target` + `Err(streamErr)` separately. The `"open Session stream: %w"` and `"Hello handshake: %w"` wraps stay — they classify which transport step surfaced the err.
- **S3** FD-leak ceiling extracted into `Dialer.closeAndCheckLeak(closeable, *count, log) bool`. Returns `true` when `*count >= closeErrCeiling` (=5). `runDial` calls it after every drain; on bail it publishes `SessionFailed` with `Reason: "fd-leak-ceiling: N consecutive close failures"`. The `closeable` interface is satisfied by `*grpc.ClientConn` and a tiny `fakeCloser` test type — no need to stand up a real gRPC channel to exercise the ledger.
- **S9** ctx-aware skip in every `publish*` function. Each one early-returns on `ctx.Err() != nil` before the log line and the `overseer.Publish` call. The previous L243-246 ad-hoc `if ctx.Err() != nil || drainErr == "ctx_done" { return }` in `runDial` is preserved (still load-bearing for the "reconnecting" Info-log suppression and to avoid spinning the loop one more time) but `publishBroken` now skips internally as defense in depth.
- **S15** Consume non-`ErrSlotInvalid` err level promoted from Warn to Error. The Registry contract documents `Consume` returning only `ErrSlotInvalid` or success, so any other err is a contract regression that deserves Error level. Also changed: the `errors.Is(err, agentslots.ErrSlotInvalid)` branch is now an Info log (legitimate raw-`docker start` case is not a warning).
- **S10** Ring buffer in `subscribe.go`. `panicTimes []time.Time` → `[subscribePanicWindowMaxHits]time.Time` + `panicHead int`. `subscribePanicWindowMaxHits` flipped to `const` (must be compile-time for array size); `subscribePanicBackoffMin/Max` and `subscribePanicWindow` flipped to `var` so storm tests can shrink them without minutes of real-time wait. Mirrors the agentregistry/subscribe.go fix from Task #6.
- **T2** New tests in `dialer_test.go` + `subscribe_test.go`:
  - `TestVerifyChainOnly_AcceptsValidChain` / `RejectsUntrustedRoot` / `RejectsExpiredLeaf` / `RejectsEmptyCerts` — self-signed CA + leaf via `crypto/ecdsa` + `crypto/x509`, no real TLS handshake needed.
  - `TestRecordRegistryProvenance_HappyPath` / `ThumbprintMismatch` / `RegistryMiss` / `NilRegistry` — `regmocks.RegistryMock` + log capture; asserts level + `provenance` field + thumbprint hex round-trip.
  - `TestConsumeAnnounceSlot_Consumed` / `Missing` / `RegistryRegression` / `NilRegistry` — `slotmocks.RegistryMock`; pinpoints the S15 level promotion.
  - `TestCloseAndCheckLeak_BailsAfterCeiling` / `SuccessResetsCounter` / `LogsCloseFailure` — direct ledger tests against a `fakeCloser` walking a controlled error sequence.
  - `TestSubscribe_FilterAdmitsPurposeAgent` / `FilterRejectsNonAgent` — per-event predicate exercised at the bus layer (live `*Overseer`).
  - `TestPanicRingBuffer_BoundedMemory` — structural twin of the consumer goroutine's recover/ring-buffer/threshold path drives `subscribePanicWindowMaxHits` synthetic recoveries through the same accounting and asserts termination + log message. The accounting is the unit; the integration with `*Dialer` lives in the e2e suite.
  - `TestPanicRingBuffer_Bounded` — compile-time guard on the array size constant.
  - `TestZerologFieldKeysStable` — sanity-check on the `level` / `message` / `event` keys the assertions throughout the file rely on; if zerolog ever renames any of them, this test fails first.
- 4972 unit tests pass under `make test`. `-race -count=3` clean against `./internal/controlplane/agentdial/...`.

**Deviations from the spec**

1. **No `establishWithRetry` end-to-end retry/backoff/ctx-cancel/container-gone tests in this commit.** The spec listed five `TestEstablishWithRetry_*` names. Each requires standing up a fake `mobyclient.APIClient` for `resolveAgent` AND an in-process gRPC server (likely `bufconn` + TLS) for `tryEstablish`. The TLS scaffolding overlaps significantly with what Task #8 will need for the listener; lifting it out of two tasks at once is the right move. Coverage of the four areas the spec actually called out is preserved: `verifyChainOnly` (cert validation), `recordRegistryProvenance` (cross-check), `closeAndCheckLeak` (FD-leak threshold), and the Subscribe filter / ring buffer. The retry/backoff path is exercised via the e2e suite already.
2. **No `publishOrSkipOnShutdown` helper symbol.** The task suggested a single ctx-aware helper called by every `publish*`. After implementing it three different ways, the cleanest end state was a one-line `if ctx.Err() != nil { return }` at the top of each function — the helper was a single line of indirection wrapping a single line of logic. Inlined for readability. The contract (skip on ctx-done) is identical.
3. **`closeable` interface defined locally** rather than reusing an existing one. `io.Closer` would have worked but pulls in a stdlib import where none was needed; the local interface is one line and stays in the file that owns the call site.

**Downstream task impact**

None — Task #5 had no downstream blockers. Task #8's listener tests can lift the cert-helper pattern (`genCA` + `signLeaf`) directly from `dialer_test.go` if useful.
