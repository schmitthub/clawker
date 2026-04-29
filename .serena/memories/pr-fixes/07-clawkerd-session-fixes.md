# Task 07 — cmd/clawkerd/session: audit log, race fix, signal filter, atomic, validate + tests

**Status**: pending
**Claimed by**: —
**Blocks**: —
**Blocked by**: none
**Parallel-safe**: yes (no other task touches `cmd/clawkerd/session.go`)

## Findings covered

- **C3** — `cmd/clawkerd/session.go:233-318` — `ShellCommand` handler runs arbitrary argv with arbitrary uid/gid as root. Audit logging gap: only error paths log full argv. Missing observability for the RCE-as-root threat surface.
- **C4** — `cmd/clawkerd/session.go:383,392,421` — Multiple goroutines write `stageErrs[i] = waitErr` concurrently with post-Wait read at L421 (`stageErrs[len(cmds)-1]`). `reapWG.Wait()` orders writes-before-read so probably benign, but `go test -race` flags it.
- **S2** — `cmd/clawkerd/session.go:138-156` — `runReceiver` returns raw `stream.Recv()` error. `ctx.Err() != nil` skip suppresses logging — if Recv returned a real transport error simultaneously with cancel, that error is silently swallowed.
- **S13** — `cmd/clawkerd/session.go:277,289,301,312` — `_ = stdinW.Close()` swallows real I/O errors during failed pipeline setup. Could mask FD leaks under repeated SPAWN_FAILED.
- **S14** — `cmd/clawkerd/session.go:557-564` — `routeSignal` filters ESRCH but not `os.ErrProcessDone`. Modern Go returns `os.ErrProcessDone` on Signal-after-reap race → false-positive Error logs every time SIGTERM hits a near-exited stage.
- **Y4** — `api/clawkerd/v1/clawkerd.proto:39` `command_id` is free-form string. `cmd/clawkerd/session.go:209` enforces dup-by-`command_id` at runtime, but type layer has no replay/dup invariant. ShellCommand requires non-empty; Hello echo doesn't.
- **T1** — `cmd/clawkerd/session.go` (687 lines) zero unit tests. Untested: dispatch routing, signal forwarding, atomicBool, shutdownRunning concurrency, runShellCommand stream routing.

Plus an internal cleanup the user agreed to:

- **atomicBool → sync/atomic refactor** — replace hand-rolled mutex pattern with stdlib `sync/atomic.Bool` (Go 1.19+).

## Decisions

1. **C3**: Add full-argv audit log on every Started/Done at Info via zerolog with argv + uid/gid + duration. Document the RCE threat surface in `cmd/clawkerd/CLAUDE.md`. (Policy gate / argv allow-listing is out of scope here — TODO for v2.)
2. **C4**: Replace shared `stageErrs` slice writes with per-goroutine local + channel for the final-stage err. Or: since only `stageErrs[len(cmds)-1]` is read, capture the final-stage err in a single `finalStageErr` variable guarded by a mutex (or sent over a 1-buffered channel from the final-stage goroutine). **Channel approach preferred** — clearer ownership.
3. **S2**: Log at Info with wrapped err on ctx-cancel teardown in `runReceiver`. Don't elevate to Error during normal shutdown.
4. **S13**: Wrap `stdinW.Close` in a helper that logs once per goroutine + Warn on real errors. Helper deduplicates per-call-site.
5. **S14**: Add `errors.Is(err, os.ErrProcessDone)` to filter alongside ESRCH in `routeSignal`. Both are race-with-reaper artifacts.
6. **Y4**: Validate non-empty `command_id` at unmarshal in dispatch handler. Reject empty with clear error before dispatch runs (codes.InvalidArgument).
7. **T1**: Add `cmd/clawkerd/session_test.go` with full coverage of dispatch routing, signal forwarding, shutdownRunning concurrency, stream routing.
8. **atomicBool**: Replace with `sync/atomic.Bool`. Stdlib, race-safe, no maintenance.

## Affected files

| File | Change |
|------|--------|
| `cmd/clawkerd/session.go` | All seven decisions above. |
| `cmd/clawkerd/session_test.go` | NEW — full unit coverage. |
| `cmd/clawkerd/CLAUDE.md` | Document RCE-as-root threat surface + audit-log invariant. |
| `api/clawkerd/v1/clawkerd.proto` | Comment on `command_id` clarifying non-empty contract. (No proto change needed — runtime validate.) |

## Implementation plan

1. **Read `cmd/clawkerd/session.go` in full** before editing. 687 lines — understand dispatch, lifecycle, stream routing, signal handling, command tracking maps.

2. **Audit log every Started/Done** (C3):
   ```go
   func (s *clawkerdServer) startShellCommand(...) {
       startedAt := time.Now()
       s.log.Info().
           Str("command_id", cmd.CommandId).
           Strs("argv", cmd.Argv).
           Uint32("uid", cmd.Uid).
           Uint32("gid", cmd.Gid).
           Str("event", "shell_command_started").
           Msg("clawkerd: shell command started")
       // ... existing logic ...
       s.log.Info().
           Str("command_id", cmd.CommandId).
           Strs("argv", cmd.Argv).
           Dur("duration", time.Since(startedAt)).
           Int("exit_code", exitCode).
           Str("event", "shell_command_done").
           Msg("clawkerd: shell command done")
   }
   ```
   `event` field standardized so log shipping pipelines can index audit events.

3. **stageErrs race fix** (C4):
   Replace:
   ```go
   stageErrs := make([]error, len(cmds))
   for i, c := range cmds {
       reapWG.Add(1)
       go func(i int, c *exec.Cmd) {
           defer reapWG.Done()
           waitErr := c.Wait()
           stageErrs[i] = waitErr  // RACE
       }(i, c)
   }
   reapWG.Wait()
   finalErr := stageErrs[len(cmds)-1]  // race with above
   ```
   With:
   ```go
   finalStageErrCh := make(chan error, 1)
   for i, c := range cmds {
       isFinal := i == len(cmds)-1
       reapWG.Add(1)
       go func(c *exec.Cmd, isFinal bool) {
           defer reapWG.Done()
           waitErr := c.Wait()
           if isFinal {
               finalStageErrCh <- waitErr
           }
           // earlier-stage Wait errors are not propagated (matches existing behavior)
       }(c, isFinal)
   }
   reapWG.Wait()
   finalErr := <-finalStageErrCh
   ```
   Buffer size 1 ensures the goroutine never blocks even if the receiver is delayed.

4. **runReceiver shutdown log** (S2):
   ```go
   recvErr := stream.Recv()
   if recvErr != nil {
       if ctx.Err() != nil {
           // Shutdown teardown: log the cause at Info so operators have audit trail
           // but don't elevate to Error (this is normal during graceful stop).
           s.log.Info().Err(recvErr).Msg("clawkerd: Recv ended during ctx-cancel teardown")
           return recvErr
       }
       s.log.Error().Err(recvErr).Msg("clawkerd: Recv error")
       return recvErr
   }
   ```

5. **stdinW close helper** (S13):
   ```go
   func (s *clawkerdServer) closePipeOnce(name string, w io.Closer, loggedAlready *bool) {
       if *loggedAlready { _ = w.Close(); return }
       if err := w.Close(); err != nil {
           s.log.Warn().Str("pipe", name).Err(err).Msg("clawkerd: pipe close failed during pipeline teardown")
           *loggedAlready = true
       }
   }
   ```
   Replace each `_ = stdinW.Close()` with `s.closePipeOnce("stdin", stdinW, &loggedFlag)` where `loggedFlag` is a per-goroutine bool.

6. **routeSignal filter** (S14):
   ```go
   if err := c.Process.Signal(sig); err != nil {
       if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
           s.log.Debug().Str("event", "signal_after_exit").Msg("...")
           return
       }
       s.log.Error().Err(err).Msg("clawkerd: Signal failed")
   }
   ```

7. **command_id non-empty validation** (Y4):
   Inside the dispatch handler at L209 area:
   ```go
   if cmd.CommandId == "" {
       return status.Error(codes.InvalidArgument, "command_id is required")
   }
   ```
   Apply to ShellCommand and any other command type that has dup-detection semantics. Hello echo doesn't need it (no dup tracking) — keep behavior.

8. **atomicBool refactor**: find all uses of the hand-rolled type and replace with `sync/atomic.Bool`. Drop the custom type definition.

9. **Add session_test.go** (T1) — see Test requirements below.

10. **Update `cmd/clawkerd/CLAUDE.md`**:
    - Add a "Threat surface" section documenting clawkerd-runs-as-root + ShellCommand-arbitrary-argv. Note that CN-pin is the load-bearing trust boundary today; argv allow-listing is a v2 concern.
    - Add an "Audit logging" section: every Started/Done emits a structured Info event with full argv. Operators MUST forward these logs to durable storage if compliance requires retention.

## Test requirements

`cmd/clawkerd/session_test.go`:

- `TestDispatch_RoutesShellCommand` — happy-path dispatch hits the right handler.
- `TestDispatch_EmptyCommandID_ReturnsInvalidArgument` — Y4.
- `TestDispatch_DuplicateCommandID_RejectsSecondCall` — existing dup-detect contract.
- `TestRunShellCommand_AuditLogStartedAndDone` — capture log output via test logger; verify both events emitted with expected fields.
- `TestRunShellCommand_RaceClean` — `go test -race` with concurrent shell commands; no race in stageErrs.
- `TestShutdownRunning_DrainsAllRunningCommands` — start N commands, shutdown; all reaped.
- `TestRouteSignal_FiltersErrProcessDone` — fake Process whose Signal returns os.ErrProcessDone; expect Debug log not Error.
- `TestRouteSignal_FiltersESRCH` — same with syscall.ESRCH.
- `TestClosePipeOnce_LogsExactlyOnce` — call helper N times; verify only one Warn line emitted.
- `TestRunReceiver_CtxCancelDuringRecv_LogsAtInfo` — cancel ctx while Recv is blocked; verify Info-level "during ctx-cancel teardown" log.
- `TestAtomicReplacement_Compiles` — placeholder; the real test is the test suite continuing to pass.

## Verification

```bash
go build ./...
go vet ./cmd/clawkerd/...
go test ./cmd/clawkerd/... -race -v

# Confirm atomicBool gone
grep -n 'type atomicBool' cmd/clawkerd/
# Should return zero matches

# Confirm audit log fields present
grep -n '"shell_command_started"\|"shell_command_done"' cmd/clawkerd/session.go
# Should return at least 2 matches

make test
```

## Dependencies

None. Independent task, parallel-safe.

## Risks / gotchas

- **687 lines** — the file is dense. Don't accidentally drop any of the existing concurrency primitives (`reapWG`, `shutdownRunning` map, `lookup` map, `runningCommand` struct).
- **Audit log volume**: every shell command emits 2 log lines with full argv. For a busy clawkerd this could be substantial. Acceptable trade for security observability; document in CLAUDE.md.
- **atomicBool sites**: there may be subtle CAS semantics on the hand-rolled type. `sync/atomic.Bool.CompareAndSwap` matches; verify all call sites.
- **stageErrs race "probably benign"**: don't gloss over. The data race exists per Go memory model — `reapWG.Wait()` provides a happens-before for the wg's internal counter, not for arbitrary slice writes. `go test -race` is correct to flag.
- **closePipeOnce flag**: the `loggedAlready` bool is per-goroutine, not per-pipe. If you want per-pipe dedup, attach the flag to the pipe wrapper. Per-goroutine is simpler and matches the user's intent ("logs once per goroutine").
- **Signal filtering**: `os.ErrProcessDone` is Go 1.17+. `syscall.ESRCH` is unix-only — preserve build tags if relevant.
- **command_id validation placement**: validate as early as possible in dispatch — before any side effect. `codes.InvalidArgument` is the correct gRPC code.

## Reference reading

- `cmd/clawkerd/session.go` (current 687-line file)
- `cmd/clawkerd/CLAUDE.md` (current — note clawkerd-runs-as-root)
- `cmd/clawkerd/listener.go` (sibling — Task #8 covers it; coordinate so audit logs don't double up at listener vs handler)
- `api/clawkerd/v1/clawkerd.proto` (Command/Response message definitions)
- Project root `CLAUDE.md` "Security" section

## Resolution

(Filled in on completion.)

- Commit SHA:
- Notes:
