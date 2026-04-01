# QA Audit — Round 2 (2026-03-30) — REVIEWED 2026-04-01

Branch: `audit/qa-2026-03-30` | Agents: 6 (state mgmt, subprocess lifecycle, container orchestration, TUI, build/bundler, deep-dive)
Review: 5 specialized subagents + Serena code verification

**Original: 3 CRITICAL, 10 HIGH, 18 MEDIUM, 8 LOW**
**Post-review: 23 VALID, 3 PARTIAL, 13 INVALID**

---

## CRITICAL

### C6: Container exit code lost in loop runner
**Verdict: VALID | Severity: CRITICAL**
Location: `internal/cmd/loop/shared/runner.go:632-673`

Block-scoped `exitCode :=` in first select (line 637) discards the value. Second select falls to `default` with `exitCode = 0`. Non-zero exit codes (crash=1, OOM=137) silently become 0 (success). Corrupts circuit breaker logic.

**Proposed fix:** Declare `var exitCode int` before first select, use `=` not `:=` at line 637, remove redundant second select block.

---

### C7: Config volume orphaned when history volume creation fails
**Verdict: INVALID**

`EnsureVolume` is idempotent (checks existence first). Caller (`CreateContainer`) tracks created volumes and has cleanup on error paths. Empty config volume is a valid state (defaults work fine).

---

### C8: Firewall CA cert install broken on Alpine
**Verdict: INVALID**

Alpine's `ca-certificates` package provides `update-ca-certificates` at the same path, uses same `/usr/local/share/ca-certificates/` input and `/etc/ssl/certs/ca-certificates.crt` output. Paths and commands are cross-distro compatible.

---

## HIGH

### H10: StoreUI `MarkForWrite` skipped for default-value fields
**Verdict: INVALID**

Default values have provenance from the defaults layer (in-memory). When saving to any real file target, `prov.Path != target.Path` triggers `MarkForWrite`. The write logic correctly handles persisting default values explicitly.

---

### H11: ContainerStart PostStart failure leaves container running
**Verdict: VALID | Revised severity: MEDIUM**
Location: `internal/cmd/container/shared/container_start.go:180-207`

No rollback when `BootstrapServicesPostStart` fails. Container runs without firewall iptables or socket bridge. Envoy/CoreDNS infrastructure still routes traffic, partially mitigating the security concern.

**Proposed fix:** Add `client.ContainerStop()` in PostStart error path.

---

### H12: SocketBridge cleanup SIGTERM no wait
**Verdict: VALID | Revised severity: MEDIUM**
Location: `internal/socketbridge/manager.go:306-314`

`killProcess` sends SIGTERM and returns immediately. PID file removed before process exits. New bridge could compete for same Unix socket. Race window is small but real.

**Proposed fix:** Add wait-with-timeout after SIGTERM (poll `isProcessAlive`, SIGKILL after 3s).

---

### H13: Hostproxy startDaemon orphaned daemon blocks future EnsureRunning
**Verdict: INVALID**

`isDaemonRunning` is two-part (PID file + port health check). If daemon is unhealthy, `isPortInUse` check handles it. Error propagates to caller. Design is reasonable.

---

### H14: Output decline counter not reset on zero-output loops
**Verdict: VALID | Revised severity: MEDIUM**
Location: `internal/cmd/loop/shared/circuit.go:213-227`

When `outputSize == 0`, the entire decline check is skipped but `declineCount` is NOT reset. Stale counter causes false circuit trips after a productive loop interrupts the sequence.

**Proposed fix:** Add `else { cb.declineCount = 0 }` to the outer `if` block.

---

### H15: `limitedBuffer.Write` short count violates io.Writer contract
**Verdict: VALID | Severity: HIGH**
Location: `internal/cmd/loop/shared/runner.go:698-714`

Returns truncated `len(p)` without error when buffer near capacity. Violates `io.Writer` contract. `StdCopy` may abort stream prematurely, losing container stdout.

**Proposed fix:** Capture original `len(p)` before truncation, return it with nil error (report all bytes consumed).

---

### H16: `EnsureImage` content hash excludes firewall CA cert
**Verdict: VALID | Severity: HIGH**
Location: `internal/docker/builder.go:93`, `internal/bundler/hash.go:17-57`

`ContentHash(dockerfile, nil, ...)` — `nil` includes means CA cert not hashed. After CA rotation, cached image has old cert, TLS through firewall breaks.

**Proposed fix:** Pass CA cert path in `includes` slice.

---

### H17: `post-init-done` marker created as root
**Verdict: VALID | Revised severity: MEDIUM**
Location: `internal/bundler/assets/entrypoint.sh:219`

`touch "$POST_INIT_DONE"` runs as root in entrypoint context. Marker owned by root in user directory. User can't delete without sudo.

**Proposed fix:** `gosu "$_USER" touch "$POST_INIT_DONE"`

---

### H18: `GIT_CONFIG_COUNT=1` hardcoded — clobbers user git config env
**Verdict: VALID | Severity: HIGH**
Location: `internal/docker/env.go:130`

Unconditionally sets `GIT_CONFIG_COUNT=1`, overwriting any user-configured git env config entries. User's env-based git config silently vanishes.

**Proposed fix:** Read existing count, increment, append at next index.

---

### H19: `VersionsFile.MarshalJSON` loses sort order
**Verdict: VALID | Revised severity: MEDIUM**
Location: `internal/bundler/registry/types.go:68-89`

Sorted entries dumped into plain `map[string]*VersionInfo` before `json.Marshal`. Go 1.12+ gives lexicographic order, not the intended semver order. Only affects visual ordering in versions.json.

**Proposed fix:** Build JSON manually preserving semver sort.

---

## MEDIUM

### M13: projectRegistry TOCTOU
**Verdict: VALID | Severity: LOW** — Read-check-write without lock. Single-user CLI, unlikely concurrent registration.

### M14: dirty paths stranded
**Verdict: INVALID** — Dirty paths properly cleared on write/refresh. Preserved on failure for retry (correct).

### M15: PID reuse false positive
**Verdict: VALID | Severity: MEDIUM** — All 3 daemon packages (firewall, hostproxy, socketbridge) use `Signal(0)` only, never verify process identity. PID reuse could cause stale daemon or killing unrelated process.

### M16: StopDaemon no wait
**Verdict: VALID | Severity: MEDIUM** — Firewall and hostproxy StopDaemon send SIGTERM and return immediately. Restart could conflict with still-running old process.

### M17: bridge goroutine tracking
**Verdict: INVALID** — Socket bridge uses OS processes (exec.Command + Release), not goroutines. Tracked via PID files.

### M18: fd leak (firewall daemon)
**Verdict: VALID | Severity: LOW** — `startDaemonProcess` opens log file, never closes parent's copy after `cmd.Start()`. One fd per daemon spawn. Fix: mirror socketbridge pattern (`logFile.Close()` after start).

### M19: snapshot volume orphan
**Verdict: VALID | Severity: MEDIUM** — `Cleanup()` never called from production code. Container removal without `--volumes` orphans snapshot volumes permanently. Contradicts ephemeral design intent.

### M20: partial config init
**Verdict: INVALID** — `NewConfig` returns `(nil, error)` on any store failure. No partial state exposure.

### M21: resolveVolumePath semantics
**Verdict: PARTIAL | Severity: LOW** — Bare relative paths not resolved, but Docker provides its own validation. UX gap, not functional bug.

### M22: IsOutsideHome symlink bypass
**Verdict: INVALID** — `filepath.EvalSymlinks()` called on both dir and home before comparison. Symlinks properly resolved.

### M23: sendWarning cancelled ctx
**Verdict: VALID | Severity: MEDIUM** — Deferred cleanup passes cancelled ctx to sendWarning, making warnings invisible on context cancellation.

### M24: completion signal hides loopErr
**Verdict: VALID | Severity: MEDIUM** — Completion check breaks before loopErr propagation. Error persisted to session but not returned in Result.

### M25: SelectField Ctrl+C stuck
**Verdict: VALID | Severity: MEDIUM** — Ctrl+C returns tea.Quit which is filtered by fbFilterQuit. Silent no-op (Escape works). UX confusion.

### M26: cursor heading bug
**Verdict: VALID | Severity: LOW** — Cursor can land on heading row after refresh. Cosmetic only; Enter on heading safely rejected.

### M27: DashboardResult nil err
**Verdict: VALID | Severity: MEDIUM** — Type assertion failure returns nil Err, masking the issue. Caller proceeds as if successful.

### M28: lastSaveError persists
**Verdict: VALID | Severity: LOW** — Stale error message shown until next successful save. Cosmetic.

### M29: symlinks skipped in build context
**Verdict: VALID | Severity: MEDIUM** — Symlinks silently skipped in tar generation. No warning to user. Causes confusing build failures.

### M30: ResolveVersions raw stderr
**Verdict: VALID | Severity: MEDIUM** — `fmt.Fprintf(os.Stderr, ...)` bypasses IOStreams. Breaks --quiet, --format json, testing.

---

## LOW

- **L5**: VALID — `StopAll` always returns nil despite errors
- **L6**: INVALID — `ringBuffer` actively used in tui/progress.go
- **L7**: PARTIAL — KV duplicate keys: by-design (last-wins) but no user warning
- **L8**: VALID — Silent CWD fallback on project root failure
- **L9**: INVALID — Only one `parseEnvFile` implementation exists
- **L10**: INVALID — Version comments are in sync
- **L11**: VALID — embed.FS errors silently discarded in `EmbeddedScripts()`
- **L12**: PARTIAL — Direct slice reference from store in `RemoveByRoot` splice

---

## Review Summary

| Category | Valid | Partial | Invalid | Total |
|----------|-------|---------|---------|-------|
| CRITICAL | 1 (C6) | 0 | 2 (C7,C8) | 3 |
| HIGH | 7 (H11,H12,H14-H19) | 0 | 3 (H10,H13) | 10 |
| MEDIUM | 12 | 1 (M21) | 5 (M14,M17,M20,M22) | 18 |
| LOW | 3 (L5,L8,L11) | 2 (L7,L12) | 3 (L6,L9,L10) | 8 |

### Priority fixes (by impact):
1. **C6**: Fix block-scoped exitCode in loop runner (CRITICAL — corrupts loop outcomes)
2. **H18**: Fix GIT_CONFIG_COUNT clobbering (HIGH — silently destroys user git config)
3. **H15**: Fix limitedBuffer.Write io.Writer violation (HIGH — StdCopy may abort early)
4. **H16**: Include CA cert in content hash (HIGH — stale image after rotation)
5. **H11**: Add rollback in ContainerStart on PostStart failure (MEDIUM)
6. **H14**: Reset decline counter on zero-output loops (MEDIUM — false circuit trips)
7. **M19**: Auto-cleanup snapshot volumes on container removal (MEDIUM — disk leak)
8. **M15+M16**: Add process identity verification + wait-after-SIGTERM in all 3 daemon packages (MEDIUM)
9. **H17**: Use gosu for post-init-done marker (MEDIUM)
10. **M29**: Warn on symlink skip in build context (MEDIUM)
11. **H19**: Fix MarshalJSON to preserve semver order (MEDIUM)
12. **M24+M27**: Fix loopErr propagation + DashboardResult nil err (MEDIUM)