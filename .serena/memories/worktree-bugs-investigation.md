# Worktree Feature Bugs Investigation

## STATUS: FIXED

Bug 3 fixed (slashed branch names). Bug 1 was already fixed. Bug 2 was user error.

**NEW (2026-02-04):** Added `clawker worktree prune` command and handle-based registry API to fix stale worktree entries.

See also: `internal/config/registry.go` (ProjectHandle, WorktreeHandle, WorktreeStatus types)

### Changes Made

**Bug 3 Fix (Slashes in Branch Names):**
- `internal/git/git.go`: `SetupWorktree()` now uses `filepath.Base(wtPath)` as worktree name
- `internal/git/git.go`: `RemoveWorktree()` now uses `filepath.Base(wtPath)` for removal
- `internal/git/git.go`: `ListWorktrees()` now takes `[]WorktreeDirEntry` instead of `WorktreeDirProvider`
- `internal/git/types.go`: Added `WorktreeDirEntry` struct for name/slug/path mapping
- `internal/cmd/worktree/list/list.go`: Updated to convert config types to git types
- Tests added for slashed branch names (`feature/test-slash`, `a/b/c/deep-branch`)

**Bug 1 (Warnings Not Logged):**
- Already fixed — all container command `PrintWarning()` calls have corresponding `logger.Warn()` calls

---

## Bug 1: Warnings Not Logged to File in Interactive Mode

### Symptom
When running `clawker run -it --worktree <name> --agent ralph @ --dangerously-skip-permissions`, error/warning output appears briefly then vanishes. **Warnings don't appear in log files** even though file logging is supposed to capture everything.

### Root Cause
**`cmdutil.PrintWarning()` bypasses the logger entirely**

The issue is NOT with the terminal/TUI (that's expected behavior). The issue is that warnings should be logged to file for later review, but they're not.

1. `cmdutil.PrintWarning()` (output.go:64-66) writes directly to `ios.ErrOut` via `fmt.Fprintf()`
2. It does NOT call `logger.Warn()`, so it **never goes to the log file**
3. `logger.Warn()` (logger.go:268-278) correctly writes to file via `fileOnlyLog.Warn()` when in interactive mode
4. But code uses `PrintWarning()` instead of `logger.Warn()` for user-facing messages

### Key Code Locations

| File | Lines | Description |
|------|-------|-------------|
| `internal/cmdutil/output.go` | 64-66 | `PrintWarning()` - writes ONLY to stderr, not log file |
| `internal/logger/logger.go` | 268-278 | `Warn()` - writes to file even in interactive mode |
| `internal/cmd/container/run/run.go` | 237-244 | Host proxy warnings use `PrintWarning()` |

### Suggested Fix
**Call `logger.Warn()` at the call site**, not inside `PrintWarning()`.

Rationale:
- Not all user-facing warnings need file logging
- The caller should explicitly decide what gets logged vs displayed
- No hidden side effects — `PrintWarning()` stays a simple stderr writer

**At call sites that need file logging**, use both:
```go
// Example in run.go for host proxy warnings:
logger.Warn().Err(err).Msg("failed to start host proxy server")  // File logging
cmdutil.PrintWarning(ios, "Host proxy failed to start. Browser authentication may not work.")  // User output
```

**Audit needed**: Find all `PrintWarning()` calls during worktree/container setup and add corresponding `logger.Warn()` calls where file logging is important.

Key locations to audit:
- `internal/cmd/container/run/run.go` — host proxy, worktree setup warnings
- Any other places that print warnings before interactive mode takes over

---

## Bug 2: NOT A BUG — Worktree Remove Behavior Clarification

### Initial Report
User initially reported error when running `clawker worktree remove <name>`.

### Clarification
This was **user error** — the `--delete-branch` flag was NOT used.

**Expected behavior:**
- `clawker worktree remove <name>` — Removes worktree directory, **preserves branch**
- `clawker worktree remove --delete-branch <name>` — Removes worktree AND deletes branch

**Why preserving branch makes sense:**
Once the worktree is removed, the branch is no longer associated with a worktree. The branch continues to exist as a normal git branch that can be checked out or used to create a new worktree.

### No Fix Required
Current behavior is correct.

---

## Bug 3: Branches with Slashes Fail — "invalid worktree name"

### Symptom
`clawker run --worktree a/output-styling --agent ralph` fails with:
```
Error: setting up worktree "a/output-styling" for agent "ralph": creating git worktree: 
adding worktree "a/output-styling" at /Users/andrew/.local/clawker/projects/clawker/worktrees/a-output-styling: 
invalid worktree name "a/output-styling"
```

### Root Cause
**go-git rejects slashes in worktree NAMES (not branch names)**

The directory path IS correctly slugified (`a-output-styling`), but the worktree NAME still has the slash (`a/output-styling`), which go-git rejects.

**Call flow in `SetupWorktree()` (git.go:107-157):**
1. Line 109: `dirs.GetOrCreateWorktreeDir(branch)` → creates `worktrees/a-output-styling/` ✓
2. Line 147: `branchRef := plumbing.NewBranchReferenceName(branch)` → creates ref for `a/output-styling` ✓
3. Line 148: `wt.AddWithNewBranch(wtPath, branch, branchRef, baseCommit)` ← **PROBLEM HERE**
   - `branch` = `"a/output-styling"` passed as worktree NAME
   - go-git creates `.git/worktrees/<name>/` directory
   - Slashes would create subdirectories, which go-git rejects

**Why go-git rejects slashes:**
The worktree name is used to create `.git/worktrees/<name>/` directory. A name like `a/output-styling` would try to create `.git/worktrees/a/output-styling/`, which is invalid.

### Key Code Locations

| File | Lines | Description |
|------|-------|-------------|
| `internal/git/git.go` | 148 | `branch` passed as worktree name (should be slugified) |
| `internal/git/worktree.go` | 44 | `w.wt.Add(wtFS, name, opts...)` — name goes to go-git |
| `internal/config/project_runtime.go` | 95 | `Slugify(name)` used for directory only |
| `internal/config/registry.go` | 335-352 | `Slugify()` function |

### Suggested Fix
**Use directory basename as worktree name (already slugified)**

Since `git` is a leaf package (cannot import `config`), use the directory path basename as the worktree name — it's already slugified by `GetOrCreateWorktreeDir()`.

In `SetupWorktree()` (git.go:148):

```go
// Current (broken):
if err := wt.AddWithNewBranch(wtPath, branch, branchRef, baseCommit); err != nil {

// Fixed - use directory basename (already slugified):
wtName := filepath.Base(wtPath)  // e.g., "a-output-styling"
if err := wt.AddWithNewBranch(wtPath, wtName, branchRef, baseCommit); err != nil {
```

This means:
- Worktree NAME (`.git/worktrees/<name>/`): `a-output-styling` (from directory basename)
- Worktree DIRECTORY: `worktrees/a-output-styling/` (slugified by `GetOrCreateWorktreeDir`)
- Git BRANCH: `a/output-styling` (original, slashes allowed in git branches)

**go-git worktree name validation** (from `x/plumbing/worktree/worktree.go`):
```go
worktreeNameRE = regexp.MustCompile(`^[a-zA-Z0-9\-]+$`)
```
Only allows: letters, digits, hyphens. NO slashes, underscores, or dots.

**Manual verification with native git:**
```bash
$ git worktree add -b test/slashed/branch /tmp/test-worktree-slashed HEAD
Preparing worktree (new branch 'test/slashed/branch')

$ ls .git/worktrees/
test-worktree-slashed   # <-- Uses PATH BASENAME, not branch name!

$ cat .git/worktrees/test-worktree-slashed/HEAD
ref: refs/heads/test/slashed/branch   # <-- Branch name preserved with slashes
```

**Conclusion**: Native git uses the **filesystem path basename** as the worktree metadata directory name, NOT the branch name. Our fix aligns with native git behavior.

**Related functions to check:**
- `RemoveWorktree()` — may need same fix if it passes branch name to `wt.Remove()`
- `ListWorktrees()` — may need to handle name-to-branch mapping

---

## Test Considerations

### Existing Tests
- `internal/cmdutil/worktree_test.go` has tests for slashes in branch names (parsing only)
- `internal/config/project_runtime_test.go` tests `GetOrCreateWorktreeDir()` 
- `internal/cmd/worktree/remove/remove_test.go` tests worktree removal
- `internal/git/git_test.go` tests `SetupWorktree()` and `RemoveWorktree()`

### Tests Needed
1. **Bug 1**: Test that `cmdutil.PrintWarning()` messages appear in log file
2. **Bug 3**: Test worktree creation with slashed branch names (`feature/foo`, `a/b/c`)
3. **Bug 3**: Verify branch name vs worktree name distinction (`a/foo` branch + `a-foo` worktree name)

---

## Summary of Required Fixes

| Bug | Severity | Fix Location | Description |
|-----|----------|--------------|-------------|
| 1 | Medium | `internal/cmdutil/output.go:64-66` | Add `logger.Warn()` call to `PrintWarning()` |
| 2 | N/A | — | Not a bug, expected behavior |
| 3 | High | `internal/git/git.go:148` | Slugify worktree name, keep branch ref original |

---

## Related Files

- `.claude/memories/DESIGN.md` - Design principles
- `.serena/memories/worktree_test_cases.md` - Behavioral test cases for worktrees
- `internal/git/CLAUDE.md` - Git package documentation
