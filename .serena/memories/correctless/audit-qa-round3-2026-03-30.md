# QA Audit — Round 3 (2026-03-30) — REVIEWED 2026-04-01

Branch: `audit/qa-2026-03-30` | Agents: 6 | Review: 5 specialized subagents + Serena
**Note: R3 "regression hunter confirmed all R1/R2 CRITICALs as real" was wrong — R1 review found C4,C5 invalid, C7,C8 invalid.**

**Original: 3 CRITICAL, 13 HIGH, 18 MEDIUM, 9 LOW, 1 doc bug**
**Post-review: 17 VALID, 5 PARTIAL, 21 INVALID, 1 doc bug (valid)**

---

## CRITICAL

### C9: Restart doesn't stop socket bridge
**Verdict: INVALID**
Bridge daemon subscribes to Docker `die` events and self-terminates when container stops during restart. `EnsureBridge` on PostStart cleans up stale in-memory state and spawns fresh bridge. Three-layer lifecycle defense (documented in socketbridge/CLAUDE.md) handles this exact scenario.

### C10: Firewall daemon watcher kills stack mid-restart
**Verdict: INVALID**
60s grace period + 30s poll interval + 2 missed checks = 120s minimum before shutdown. Restart takes <10s. Also, `ContainerList` shows containers in "restarting" state (label filter, not state filter). Timing makes this impossible.

### C11: Data race on `s.prov`/`s.layers` in Store
**Verdict: PARTIAL | Revised severity: MEDIUM**
Read methods (`Layers()`, `Provenance()`, `ProvenanceMap()`) lack locks while write methods (`Refresh()`, `remerge()`) hold locks. Technically a race under Go memory model. But Store is used single-threaded in practice (command run functions, BubbleTea event loop). Latent bug — comments claiming "immutable after construction" are misleading since `Refresh()` replaces them.

**Proposed fix:** Use `sync.RWMutex`, add `RLock()` to read methods.

---

## HIGH

### H20: `DeleteBranch` bypasses ErrIsCurrentBranch on detached HEAD
**Verdict: INVALID** — Correct behavior matching native git. Detached HEAD has no "current branch."

### H21: TOCTOU race on duplicate worktree check
**Verdict: INVALID** — UUID-based directory names + git filesystem locks prevent real duplicates.

### H22: Cleanup rollback creates orphaned git worktree metadata
**Verdict: INVALID** — Both cleanup paths (SetupWorktree failure + registry failure) remove git metadata.

### H23: In-memory record cleared even when RemoveWorktree fails
**Verdict: VALID | Severity: MEDIUM**
`delete(p.record.Worktrees, branch)` runs unconditionally at `manager.go:378`. If git removal fails before directory removal, in-memory handle reports worktree absent while it exists on disk.

**Proposed fix:** Only delete from in-memory record when `err == nil`.

### H24: Expired credentials silently fall through
**Verdict: INVALID** — Credential expiry checking is not the config-copying function's responsibility. Tokens refreshed at use time.

### H25: Update state file fixed `.tmp` suffix race
**Verdict: VALID | Revised severity: LOW**
Two concurrent processes could corrupt `update-state.yaml.tmp`. Practical impact minimal — corrupted state file just triggers fresh update check.

**Proposed fix:** Use `os.CreateTemp` for unique temp names.

### H26: Container logs garbled — raw multiplexed stream for non-TTY
**Verdict: VALID | Severity: HIGH**
`logs.go:138-146` uses plain `io.Copy` without `stdcopy.StdCopy`. Every other command (attach, exec, run, start) correctly demultiplexes. Users see binary garbage in log output for non-TTY containers.

**Proposed fix:** Check `Config.Tty`; use `stdcopy.StdCopy(ios.Out, ios.ErrOut, reader)` for non-TTY.

### H27: `loop tasks` silently ignores `--append-system-prompt`
**Verdict: INVALID** — Flag properly wired through `AddLoopFlags` → `ApplyLoopConfigDefaults` → `BuildRunnerOptions` → `BuildSystemPrompt`.

### H28: Firewall rules persisted before daemon starts
**Verdict: PARTIAL | Severity: LOW**
Intentional design (rules pre-positioned for daemon startup). Error from `EnsureDaemon` properly propagated. Rules persisted on failure are correct — picked up on next successful start.

### H29: `InjectPostInitScript` failure leaves volumes orphaned
**Verdict: VALID | Severity: HIGH**
`createdVolumes = nil` at line 1654 clears cleanup tracking BEFORE `InjectPostInitScript` at line 1660. On failure, deferred cleanup sees empty list, orphaning volumes.

**Proposed fix:** Move `createdVolumes = nil` to after post-init injection succeeds.

### H30: `time.Duration` fields written as nanosecond integers
**Verdict: VALID | Severity: MEDIUM**
`encodeValue` in `write.go:181-221` has no Duration case. Falls through to `default` returning raw int64. PollInterval=30s becomes `30000000000` in YAML.

**Proposed fix:** Add Duration case: `v.Interface().(time.Duration).String()`.

### H31: `Write(ToLayer(N))` with virtual layer writes to path=""
**Verdict: VALID | Revised severity: LOW**
Virtual/defaults layer has empty path. Write would fail with cryptic OS error. Not silently corrupt, but poor error message. No realistic caller would target virtual layer.

**Proposed fix:** Add guard: `if target == "" { return fmt.Errorf("virtual layer") }`.

### H32: Delete from in-memory tree + write to wrong-layer = field reappears
**Verdict: VALID | Severity: MEDIUM**
Deleting a field removes it from merged tree, but `Write` to a different layer than provenance → `refreshLayers` re-reads provenance layer → field restored. Design limitation of layered store.

**Proposed fix:** Resolve provenance layer first, only allow deletion from source layer.

---

## MEDIUM

### Valid (6):
- **M31**: VALID (MEDIUM) — `HandleError` + `return err` = double error printing in 14+ call sites
- **M32**: VALID (LOW) — 4 remove commands print success to stderr instead of stdout
- **M33**: VALID (LOW) — `WorktreeGitOnly` defined but never assigned (dead code)
- **M42**: VALID (MEDIUM) — `marshalYAMLValue` returns "" on marshal error, data loss risk (TODO exists)
- **M45**: VALID (MEDIUM) — `resolveProjectRoot` lacks `EvalSymlinks`, inconsistent with `ResolvePath`
- **M47**: VALID (LOW) — `StopBridge` double-SIGTERM (harmless but unnecessary)

### Partial (2):
- **M39**: PARTIAL (LOW) — Pre-release version treated as equal to release. Reasonable simplification.
- **M40**: PARTIAL (LOW) — Cross-user concern mitigated by XDG per-user isolation.

### Invalid (10):
- **M34** (branch mismatch — by design), **M35** (stale provider — used synchronously), **M36** (context.Background — no production usage), **M37** (recursion skip — correct behavior), **M38** (credential 0o755 — only directory, files are 0o600), **M41** (bypass timer — Docker exec, not goroutine), **M43** (migration — none exist), **M44** (zero-value int — by design), **M46** (partial failure — errors properly returned), **M48** (lookupLayerFieldValue — working as designed)

---

## LOW

### Valid (5):
- **L13**: O(n) saves in prune (registry.Save per entry instead of batch)
- **L15**: Keyring goroutine leak (spawned goroutine blocks indefinitely on timeout)
- **L18**: `--signal` restart path skips `BootstrapServicesPreStart`
- **L19**: `Refresh()` clears `dirtyPaths` unconditionally, discarding unwritten changes
- **L21**: `ValidateDirectories` no `EvalSymlinks` before collision check

### Partial (1):
- **L17**: Circular symlink concern valid for `copyDir`, but in containerfs not workspace

### Invalid (3):
- **L14** (errors properly returned in RemoveWorktree), **L16** (state file is fixed schema, atomically replaced), **L20** (`WithOnlyPaths`/`WithSkipPaths` don't exist)

---

## DOC BUG

### D4: shared/CLAUDE.md describes firewall in PreStart
**Verdict: VALID** — Code has firewall in PostStart. Documentation is wrong.

---

## Review Summary

| Category | Valid | Partial | Invalid | Total |
|----------|-------|---------|---------|-------|
| CRITICAL | 0 | 1 (C11) | 2 (C9,C10) | 3 |
| HIGH | 6 (H23,H26,H29,H30,H31,H32) | 1 (H28) | 6 (H20-H22,H24,H27) | 13 |
| MEDIUM | 6 | 2 (M39,M40) | 10 | 18 |
| LOW | 5 | 1 (L17) | 3 | 9 |
| Doc | 1 (D4) | 0 | 0 | 1 |

### Priority fixes (by impact):
1. **H26**: Add `stdcopy.StdCopy` for non-TTY container logs (HIGH — garbled output)
2. **H29**: Move `createdVolumes = nil` after post-init injection (HIGH — volume leak)
3. **H30**: Add Duration case to `encodeValue` (MEDIUM — "30000000000" in YAML)
4. **H32**: Restrict delete to provenance layer (MEDIUM — field reappears)
5. **H23**: Conditional in-memory delete on success (MEDIUM — registry desync)
6. **M31**: Remove `HandleError` calls that also return err (MEDIUM — 14+ sites)
7. **M42**: Propagate `marshalYAMLValue` errors (MEDIUM — data loss)
8. **M45**: Add `EvalSymlinks` to `resolveProjectRoot` (MEDIUM — symlink mismatch)
9. **C11**: Add `RLock()` to Store read methods (MEDIUM — latent race)
10. **D4**: Fix shared/CLAUDE.md PreStart/PostStart description