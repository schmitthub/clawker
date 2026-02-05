# Worktree Slash Branch Bugfix

## End Goal
Fix bugs in the worktree feature where branch names with slashes (e.g., `feature/test`) were rejected by go-git.

## Status: COMPLETE ✓

All work is done. Tests pass. Code reviewed and documented.

---

## Background

Two bugs were investigated from `.serena/memories/worktree-bugs-investigation.md`:

1. **Bug 1 (Warnings not logged)**: Already fixed - all `PrintWarning` calls have `logger.Warn()` before them
2. **Bug 3 (Slashes in branch names)**: FIXED - see implementation details below

Bug 2 was user error (didn't use `--delete-branch` flag).

---

## Implementation Details

### Root Cause
`SetupWorktree` passed branch name (e.g., `a/output-styling`) directly to go-git as worktree name. go-git creates `.git/worktrees/<name>/` and rejects slashes (would create subdirectories).

### Solution
Use `filepath.Base(wtPath)` as worktree name. The directory path is already slugified by `GetOrCreateWorktreeDir()`, so basename is safe.

### Files Modified

| File | Change |
|------|--------|
| `internal/git/git.go` | `SetupWorktree`: use `filepath.Base(wtPath)` as name |
| `internal/git/git.go` | `RemoveWorktree`: use `filepath.Base(wtPath)` for removal |
| `internal/git/git.go` | `ListWorktrees`: takes `[]WorktreeDirEntry` instead of provider |
| `internal/git/git.go` | Removed unused `isNotFoundError` function |
| `internal/git/types.go` | Added `WorktreeDirEntry` struct |
| `internal/cmd/worktree/list/list.go` | Convert config types to git types |
| `internal/git/git_test.go` | Added tests for slashed branch names |
| `internal/git/CLAUDE.md` | Updated API documentation |

### Key Code Change (git.go:148)
```go
// Before (broken):
if err := wt.AddWithNewBranch(wtPath, branch, branchRef, baseCommit); err != nil {

// After (fixed):
wtName := filepath.Base(wtPath)  // e.g., "a-output-styling" (slugified)
if err := wt.AddWithNewBranch(wtPath, wtName, branchRef, baseCommit); err != nil {
```

---

## TODO Sequence

- [x] Read investigation document
- [x] Verify Bug 1 status (already fixed)
- [x] Fix `SetupWorktree` to use `filepath.Base(wtPath)` as worktree name
- [x] Fix `RemoveWorktree` to use slugified name for removal
- [x] Update `ListWorktrees` to take `[]WorktreeDirEntry` parameter
- [x] Add `WorktreeDirEntry` type to types.go
- [x] Update `internal/cmd/worktree/list/list.go` caller
- [x] Update test fake provider to slugify names
- [x] Add tests for slashed branch names
- [x] Add tests for removing slashed branch worktrees
- [x] Remove unused `isNotFoundError` function
- [x] Update `internal/git/CLAUDE.md` documentation
- [x] Update investigation memory to mark complete
- [x] Run full test suite (2942 tests pass)

---

## Verification Commands
```bash
# Run all unit tests
make test

# Run git package tests specifically
go test ./internal/git/... -v

# Run worktree command tests
go test ./internal/cmd/worktree/... -v
```

---

## Related Files
- `.serena/memories/worktree-bugs-investigation.md` - Original investigation (now marked FIXED)
- `internal/git/CLAUDE.md` - Package documentation

---

## IMPORTANT REMINDER

**All work is complete.** If resuming this memory, ask the user if they want to:
1. Delete this memory (work is done)
2. Verify the fixes manually with `clawker run --worktree feature/test`
3. Continue with any additional related work
