# QA Audit — Round 4 (2026-03-30) — REVIEWED 2026-04-01

Branch: `audit/qa-2026-03-30` | Agents: 6 | Review: 6 specialized subagents + Serena
**Original: 9 CRITICAL, 19 HIGH, 26 MEDIUM, 10 LOW**
**Post-review: 27 VALID, 4 PARTIAL, 33 INVALID**

---

## CRITICAL

### C12: Dashboard data race on `result`/`runErr`
**Verdict: INVALID** — `close(ch)` provides proper happens-before synchronization. Goroutine writes complete before deferred close, main goroutine reads after channel closes.

### C13: Rate limiter busy-loop spin when `waitDuration <= 0`
**Verdict: INVALID** — Non-positive duration falls through to normal loop iteration. Rate limit window already expired, proceeding without wait is correct.

### C14: TTY cache cannot be overridden to `false`
**Verdict: VALID | Revised severity: MEDIUM**
`SetStdoutTTY(false)` sets field to false, but next `IsStdoutTTY()` call re-detects real terminal and sets back to true. Only affects callers with real `*os.File` fds (not current test usage with `*bytes.Buffer`).
**Fix:** Add `ttyOverride` flag that bypasses auto-detection when set.

### C15: `ContainerWait` goroutine leaks on every normal exit
**Verdict: VALID | Severity: HIGH**
Goroutine blocks on `<-waitResult.Error` forever on normal exits (SDK only sends to Result channel). Cumulative leak per `ContainerWait` call. Loop runner's long sessions affected most.
**Fix:** Add `select` with `waitResult.Result` and `ctx.Done()` cases so goroutine can exit.

### C16: `EnsureVolume` double-passes labels
**Verdict: PARTIAL | Severity: LOW** — Labels passed in both `options.Labels` and `extraLabels`. Redundant but no functional suppression. Code smell.

### C17: `restartContainer` wraps nil error
**Verdict: VALID | Severity: HIGH**
`if err != nil || len(result.Items) == 0 { return fmt.Errorf("...: %w", err) }` — when err==nil but Items empty, wraps nil error. Returns `"finding container clawker-envoy: <nil>"`. Blocks firewall recovery. Same pattern at line 1029.
**Fix:** Split compound condition into separate `if err != nil` and `if len(Items) == 0`.

### C18: Docker name filter substring match
**Verdict: PARTIAL | Severity: LOW** — Docker's name filter IS substring-based. But `clawker-envoy`/`clawker-coredns` are distinctive names only clawker creates. Practical risk very low.

### C19: Dockerfile ARG template injection
**Verdict: INVALID** — Config file is trusted input (project owner controls it). ARG is declarative, not executed. Project already has intentional injection points (RootRun, UserRun, Inject blocks).

### C20: Package names injected without shell validation
**Verdict: INVALID** — Same as C19. Config file is trusted. Project allows arbitrary RUN commands by design. Validating packages while allowing arbitrary shell commands is security theater.

---

## HIGH

### H33: Circuit breaker updated on infra errors — **INVALID** — Docker failures break loop, never reach UpdateWithAnalysis.

### H34: Rate token consumed before container creation — **VALID (LOW)** — Token consumed by Allow() before CreateContainer. If creation fails, token wasted. Minimal impact (one per failed run).

### H35: Circuit breaker counters lost across sessions — **VALID (MEDIUM)** — Only `NoProgressCount`/`Tripped`/`TripReason` persisted. 9 other counters reset on CLI restart. Stagnation detection resets.

### H36: Ctrl+C during active iteration hangs — **VALID (HIGH)** — Context cancellation doesn't close hijacked connection. `StdCopy` blocks on socket read. `hijacked.Close()` deferred to function exit (deadlock cycle). Requires SIGKILL.
**Fix:** Close `hijacked.Conn` on context cancellation via separate goroutine.

### H37: Negative TasksCompleted decrements totals — **VALID (LOW)** — `strconv.Atoi` parses negative strings. `sess.TotalTasksCompleted += status.TasksCompleted` with no bounds check. Unlikely, cosmetic.

### H38: StopSpinner deadlocks on slow writer — **INVALID** — select checks done channel. 120ms tick worst case, not deadlock.

### H39: WrapLines miscounts ANSI escapes — **VALID (MEDIUM)** — Uses `utf8.RuneCountInString(word)` counting ANSI escape chars. `CountVisibleWidth` exists but unused by WrapLines.

### H40: Field browser label alignment byte vs width — **PARTIAL (LOW)** — Real inconsistency (`len()` vs `CountVisibleWidth`), but inputs are ASCII-only in practice.

### H41: BuildKit drainProgress no panic recovery — **VALID (LOW)** — Claim about wg.Wait hang is wrong (defer runs on panic). But lack of recovery means panic crashes entire CLI.

### H42: SuppressOutput ignored in BuildKit — **VALID (LOW)** — `SuppressOutput` not checked. Works by accident when `OnProgress` is nil.

### H43: Legacy ImageBuild body not drained — **INVALID** — `defer resp.Body.Close()` handles it. Transport discards connection, not pool exhaustion.

### H44: matchPattern basename-first depth matching — **VALID (LOW)** — Basename-first matching is intentional gitignore-like behavior. Undocumented design choice.

### H45: CA cert write non-atomic — **VALID (MEDIUM)** — `os.WriteFile` directly, no temp+rename. Crash mid-write leaves partial/corrupt cert.

### H46: RotateCA removes certs dir while Envoy running — **VALID (MEDIUM-HIGH)** — `os.RemoveAll(certDir)` while Envoy bind-mounts it. TLS broken during regeneration window.
**Fix:** Write to temp dir, atomically swap.

### H47: ExecInspect before exit code recorded — **INVALID** — `io.ReadAll(hijack.Reader)` blocks until exec completes. Exit code available after stream EOF.

### H48: Bypass shell injection via hostProxy URL — **VALID (MEDIUM)** — `hostProxyArg` interpolated into `sh -c` string. `Enable()` correctly uses `[]string` args. Inconsistency. Limited exploitability (host sets the env var).
**Fix:** Use `[]string` args, not `sh -c` interpolation.

### H49: EscapeMDXProse double-escapes backtick content — **VALID (LOW-MEDIUM)** — Regex has no backtick context awareness. Pre-backtick-wrapped `<project>` gets double-wrapped.

### H50: Semver prerelease lexicographic comparison — **VALID (MEDIUM)** — Compares entire prerelease string lexicographically. SemVer spec requires numeric identifiers compared as integers. `"10" < "9"` lexicographically.

### H51: SetupSignalContext goroutine cleanup — **INVALID** — After first signal, context cancelled, goroutine exits via `ctx.Done()`, calls `signal.Stop`. Correct design.

---

## MEDIUM (M49-M74)

### Valid (5):
- **M53** (MEDIUM): KVEditor unsafe type assertion panic in fieldbrowser.go:504 (not comma-ok)
- **M65** (MEDIUM): emitAgentMapping JSON sprintf — duplicate of R1 M2
- **M66** (MEDIUM): EnsureCA half-state — cert written, key write fails, orphaned cert
- **M67** (LOW): readPIDFile no trim — `strconv.Atoi` without trimming whitespace
- **M71** (LOW): Install checksum grep fallback has no anchoring

### Partial (1):
- **M63**: RemoveRules calls `regenerateAndRestart` even when no rules changed. AddRules has early return, RemoveRules doesn't.

### Invalid (20):
M49 (bytes intentional), M50 (tasks read once by design), M51 (safety-first ordering correct), M52 (pointer receivers preserve state), M54 (Cmds propagated correctly), M55 (centering convention correct), M56 (standard BubbleTea init), M57 (no double newline), M58 (exact string comparison, not regex), M59 (no nil channel), M60 (deduplication exists), M61 (not dead code), M62 (error types correct), M64 (port mapping correct), M68 (KeyUsage correct for ECDSA), M69 (SEE ALSO has subcommands), M70 (Version defaults to "DEV"), M72 (struct{} intentional), M73 (context.Background correct for fire-and-forget), M74 (rules persisted before daemon by design)

---

## LOW (L22-L31)

### Valid (6):
- **L23**: CountVisibleWidth counts runes not display cells (CJK/emoji 2-column chars undercounted)
- **L26**: isContainerRunning misses "restarting" state (All:false only returns "running")
- **L27**: EnsureCA cross-process race (no file lock on cert generation)
- **L29**: EmbeddedScripts error swallow (duplicate of R1 L11)
- **L30**: Install script stderr mixed into response variable
- **L31**: PID reuse false positive (duplicate of R2 M15)

### Partial (1):
- **L25**: alt-screen unchecked write error (not a lock issue, minor hygiene)

### Invalid (3):
- **L22** (non-blocking select already used), **L24** (no percentage display exists), **L28** (separator is correct)

---

## Review Summary

| Category | Valid | Partial | Invalid | Total |
|----------|-------|---------|---------|-------|
| CRITICAL | 3 (C14,C15,C17) | 2 (C16,C18) | 4 (C12,C13,C19,C20) | 9 |
| HIGH | 13 | 1 (H40) | 5 (H33,H38,H43,H47,H51) | 19 |
| MEDIUM | 5 | 1 (M63) | 20 | 26 |
| LOW | 6 | 1 (L25) | 3 | 10 |

### Priority fixes (by impact):
1. **C15**: Fix ContainerWait goroutine leak (HIGH — cumulative per-call leak)
2. **C17**: Split nil-error wrapping compound condition (HIGH — blocks firewall recovery)
3. **H36**: Close hijacked connection on context cancel (HIGH — Ctrl+C hangs process)
4. **H46**: Atomic cert dir swap in RotateCA (MEDIUM-HIGH — breaks Envoy TLS)
5. **H35**: Persist full circuit breaker state across sessions (MEDIUM)
6. **H45**: Atomic cert/key writes (MEDIUM — crash safety)
7. **H48**: Use []string args in Bypass, not sh -c (MEDIUM — shell injection)
8. **H50**: Fix semver prerelease comparison (MEDIUM — incorrect version ordering)
9. **H39**: Use CountVisibleWidth in WrapLines (MEDIUM — ANSI line breaks)
10. **C14**: Add ttyOverride flag to SetStdoutTTY (MEDIUM)