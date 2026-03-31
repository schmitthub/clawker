# QA Audit — Round 3 (2026-03-30)

Branch: `audit/qa-2026-03-30` | Agents: 6 (git/worktree, update/keyring/containerfs, CLI commands, cross-subsystem, config/storage, regression hunter)

**New unique findings: 3 CRITICAL, 13 HIGH, 18 MEDIUM, 9 LOW, 1 doc bug** (6 R1/R2 duplicates excluded)

**Regression hunter confirmed all 8 R1/R2 CRITICALs (C1-C8) as real. No false positives.**

Full findings: `.serena/memories/correctless/audit-qa-round3-2026-03-30.md`

## NEW CRITICAL
- **C9**: Restart doesn't stop socket bridge — stale bridge daemons break SSH/GPG forwarding (`restart.go:140`, `socketbridge/manager.go:78`)
- **C10**: Firewall daemon container watcher can kill firewall stack mid-restart — missedCheckThreshold=2 with no restart grace period (`firewall/daemon.go:239`)
- **C11**: Data race on `s.prov`/`s.layers` in Store — read without lock, written under lock (`storage/store.go:187,203,219` vs `689,738,759`)

## NEW HIGH
- **H20**: `DeleteBranch` bypasses `ErrIsCurrentBranch` guard on detached HEAD (`git/git.go:461`)
- **H21**: TOCTOU race on duplicate worktree check (`project/worktree_service.go:61`)
- **H22**: Cleanup rollback creates orphaned git worktree metadata (`project/worktree_service.go:87`)
- **H23**: In-memory record cleared even when `RemoveWorktree` fails (`project/manager.go:378`)
- **H24**: Expired credentials silently fall through to file-based injection (`containerfs/containerfs.go:120`)
- **H25**: Update state file fixed `.tmp` suffix — corruption under concurrent processes (`update/update.go:228`)
- **H26**: Container logs garbled — raw multiplexed stream for non-TTY (`cmd/container/logs/logs.go:144`)
- **H27**: `loop tasks` silently ignores `--append-system-prompt` (`cmd/loop/tasks/tasks.go:228`)
- **H28**: Firewall rules persisted before daemon starts — stale on failure (`container_start.go:131`, `firewall/manager.go:196`)
- **H29**: `InjectPostInitScript` failure leaves config/history volumes orphaned (`container_create.go:1652`)
- **H30**: `time.Duration` fields written as nanosecond integers (`storage/write.go:182`, `config/schema.go:354`)
- **H31**: `Write(ToLayer(N))` with virtual layer writes to path="" (`storage/store.go:544`)
- **H32**: Delete from in-memory tree + write to wrong-layer = field reappears (`storeui/edit.go:235`)

## NEW MEDIUM
M31-M48: HandleError double-print (11 commands), success to stderr (4 remove commands), WorktreeGitOnly never assigned, SetupWorktree branch mismatch, stale entry provider, context.Background in worktree add, rewriteJSONPaths recursion skip, credential staging 0o755, pre-release version stripping, firewall daemon cross-user, bypass timer orphaned, marshalYAMLValue silent data loss, migration drops empty-cmd items, zero-value int pollution, resolveProjectRoot no EvalSymlinks, worktree remove partial failure counted as success, StopBridge double-kill, lookupLayerFieldValue worsens H10

## NEW LOW
L13-L21: Registry O(n) saves in prune, silent w.Remove cleanup, keyring goroutine leak, state file no size limit, copyDir circular symlink, restart --signal no StopBridge, Refresh discards dirty paths, WithOnlyPaths+WithSkipPaths interaction, ValidateDirectories symlink collisions

## DOC BUGS
- **D4**: shared/CLAUDE.md describes firewall in PreStart — actual code has it in PostStart

## CUMULATIVE TOTALS (R1+R2+R3)
| Severity | R1 | R2 | R3 | Total |
|----------|----|----|----|----|
| CRITICAL | 5 | 3 | 3 | **11** |
| HIGH | 9 | 10 | 13 | **32** |
| MEDIUM | 12 | 18 | 18 | **48** |
| LOW | 4 | 8 | 9 | **21** |
| Doc bugs | 3 | 0 | 1 | **4** |
| Test gaps | 6 | 0 | 0 | **6** |
| **Total** | 39 | 39 | 44 | **122** |