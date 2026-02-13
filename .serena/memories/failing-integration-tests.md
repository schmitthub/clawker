# Failing Integration Tests (Follow-up PRs Needed)

Status: **Open** — discovered during label prefix migration (branch `a/config-consts`)

---

## 1. TestContainerFs_AgentPostInit_Failure (test/internals)

**File:** `test/internals/containerfs_test.go:1110`
**Status:** **FIXED** — The entrypoint.sh no longer uses a pipeline with `head` for post-init execution. The current code directly runs `"$POST_INIT"` and properly captures the exit code.

**Verify:** Re-run `go test ./test/internals/... -run TestContainerFs_AgentPostInit_Failure -v` to confirm the fix.

---

## 2. TestRunE2E_InteractiveMode & TestRunE2E_ContainerExitDetection (test/agents)

**File:** `test/agents/run_test.go:44` and `run_test.go:234`
**Symptom:** Image build fails during `git-delta` package download (wget from GitHub releases).
**Root Cause:** Network/infrastructure issue — GitHub releases download fails during the `dpkg` install step in the test Dockerfile.

```
The command '/bin/sh -c ARCH=$(dpkg --print-architecture) && DEB="git-delta_${GIT_DELTA_VERSION}_${ARCH}.deb" && ... wget -O ... && dpkg -i ...' returned a non-zero code: 1
```

**Fix:** Likely needs retry logic or cached download for `git-delta`. May also be a transient network issue — re-running may pass. Could also pin to a working version or use a fallback mirror.

**Impact:** Flaky in environments with unreliable GitHub access. Does not affect production code.
