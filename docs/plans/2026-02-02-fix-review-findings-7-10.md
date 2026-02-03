# Fix PR Review Findings 7-10 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix the remaining four review findings from the `a/build-refactor` PR audit: include response body in forward errors, validate HTTP method from proxy, skip symlinks in tar walk, and log test cleanup errors.

**Architecture:** Four independent, small fixes across three files. Each is a leaf change with no cross-file dependencies. TDD where testable (findings 7-8), direct fix where test infrastructure is heavy (findings 9-10).

**Tech Stack:** Go stdlib (`io`, `net/http`, `net/http/httptest`, `os`, `archive/tar`), testify

---

### Task 1: Include response body in `forwardCallback` error (Finding 7)

**Files:**
- Modify: `internal/hostproxy/internals/cmd/callback-forwarder/main.go:283-284`

**Step 1: Write the fix**

In `forwardCallback`, replace the status-only error with a truncated body read:

```go
if resp.StatusCode >= 400 {
    body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
    if len(body) > 0 {
        return fmt.Errorf("local server returned status %d: %s", resp.StatusCode, string(body))
    }
    return fmt.Errorf("local server returned status %d", resp.StatusCode)
}
```

**Step 2: Build to verify compilation**

Run: `cd /Users/andrew/Code/clawker && go build ./internal/hostproxy/internals/cmd/callback-forwarder/`
Expected: Success, no errors.

**Step 3: Commit**

```bash
git add internal/hostproxy/internals/cmd/callback-forwarder/main.go
git commit -m "fix: include response body in forwardCallback error messages"
```

---

### Task 2: Validate `data.Method` before creating request (Finding 8)

**Files:**
- Modify: `internal/hostproxy/internals/cmd/callback-forwarder/main.go:267`

**Step 1: Write the fix**

In `forwardCallback`, add method validation before `http.NewRequest`. Insert after the URL construction block (after the `if data.Query != ""` block) and before `http.NewRequest`:

```go
if data.Method == "" {
    return fmt.Errorf("callback data has empty HTTP method")
}
```

**Step 2: Build to verify compilation**

Run: `cd /Users/andrew/Code/clawker && go build ./internal/hostproxy/internals/cmd/callback-forwarder/`
Expected: Success, no errors.

**Step 3: Commit**

```bash
git add internal/hostproxy/internals/cmd/callback-forwarder/main.go
git commit -m "fix: validate HTTP method in callback data before forwarding"
```

---

### Task 3: Skip symlinks in `CreateBuildContextFromDir` tar walk (Finding 9)

**Files:**
- Modify: `internal/bundler/dockerfile.go:642-646`

**Step 1: Write the fix**

In the `filepath.Walk` callback inside `CreateBuildContextFromDir`, add a symlink check before creating the tar header. Insert after the `.git` skip block (after line 640) and before the `tar.FileInfoHeader` call:

```go
// Skip symlinks — they produce broken entries in tar archives
if info.Mode()&os.ModeSymlink != 0 {
    return nil
}
```

**Important note:** `filepath.Walk` resolves symlinks before calling the walk function, so `info.Mode()` won't have the symlink bit set for followed links. The real protection here is against broken symlinks that `Walk` can't resolve (which would surface as an `os.Lstat` error earlier). This is a defensive guard. The more impactful fix is to switch to `filepath.WalkDir` which does NOT follow symlinks and use `d.Type()` for symlink detection — but that is a larger refactor out of scope for this PR. Add a code comment noting this.

Actually, since `filepath.Walk` follows symlinks and the `os.FileInfo` won't have `ModeSymlink` set, the correct minimal fix is just a comment documenting the limitation. Skip this task — it's a pre-existing issue unrelated to the PR changes, and a proper fix requires switching to `filepath.WalkDir` which is a separate change.

**Decision: Skip Task 3.** The symlink issue is pre-existing, not introduced by this PR, and a proper fix requires `filepath.WalkDir` migration. File an issue instead.

---

### Task 4: Log test cleanup errors in `manager_test.go` (Finding 10)

**Files:**
- Modify: `internal/hostproxy/manager_test.go:234` and `internal/hostproxy/manager_test.go:241`

**Step 1: Fix line 234 — `TestManagerSecondInstanceRecoversProxy` main body**

Replace:
```go
_ = m1.Stop(ctx)
```

With:
```go
if err := m1.Stop(ctx); err != nil {
    t.Logf("warning: m1.Stop failed: %v", err)
}
```

**Step 2: Fix line 241 — `TestManagerSecondInstanceRecoversProxy` cleanup**

Replace:
```go
_ = m2.Stop(ctx)
```

With:
```go
if err := m2.Stop(ctx); err != nil {
    t.Logf("warning: m2.Stop failed: %v", err)
}
```

**Step 3: Run tests to verify**

Run: `cd /Users/andrew/Code/clawker && go test ./internal/hostproxy/ -run TestManager -v -count=1`
Expected: All tests pass.

**Step 4: Commit**

```bash
git add internal/hostproxy/manager_test.go
git commit -m "fix: log test cleanup errors instead of silently discarding"
```

---

### Task 5: Run full unit test suite

**Step 1: Run make test**

Run: `cd /Users/andrew/Code/clawker && make test`
Expected: All tests pass, 0 failures.

---

## Summary

| Task | Finding | Severity | Action |
|------|---------|----------|--------|
| 1 | 7: Error body discarded | MEDIUM | Include truncated body in error |
| 2 | 8: No method validation | MEDIUM | Guard against empty method |
| 3 | 9: No symlink handling | LOW | **Skipped** — pre-existing, needs `WalkDir` migration |
| 4 | 10: Test cleanup errors | LOW | Log with `t.Logf` |
| 5 | — | — | Full test suite verification |
