# Failing Integration Tests (Follow-up PRs Needed)

Status: **Open** — discovered during label prefix migration (branch `a/config-consts`)

---

## 1. TestContainerFs_AgentPostInit_Failure (test/internals)

**File:** `test/internals/containerfs_test.go:1110`
**Symptom:** Container never exits after post-init script failure. Times out at 181s.
**Root Cause:** Missing `set -o pipefail` in `internal/bundler/assets/entrypoint.sh`.

At line 107:
```bash
if post_init_output=$("$POST_INIT" 2>&1 | head -c 10000); then
```

Without `pipefail`, the pipeline exit code comes from `head` (always 0), not `$POST_INIT` (exit 1). So:
1. The failing script's exit code is swallowed
2. `touch "$POST_INIT_DONE"` marker is created (false positive)
3. Container continues to `exec sleep infinity` instead of calling `emit_error` → `exit 1`

**Fix:** Add `set -o pipefail` after `set -e` on line 2 of `entrypoint.sh`, OR capture `PIPESTATUS[0]` after the pipeline.

**Impact:** Post-init script failures are silently ignored in real usage too — not just tests.

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
