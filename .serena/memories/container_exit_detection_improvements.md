# Container Exit Detection Improvements

**Goal:** Improve test detection of container startup failures so tests fail fast with useful diagnostics instead of timing out silently.

**Status:** COMPLETE - All implementation done, unit tests pass.

## Problem Summary

`clawker run --rm -it --agent test` resulted in instant container exit without logging any indication as to why. The firewall initialization script (`init-firewall.sh`) has multiple failure points that cause `exit 1`, but tests didn't catch these failures effectively.

**Root Cause:** `WaitForContainerRunning` only polled until container IS running, didn't detect if container already exited. If container starts and exits within the 500ms polling interval, test times out without useful error.

## Implementation Summary

### Task 1: Improved `WaitForContainerRunning` ✅ DONE
**File:** `internal/testutil/docker.go`

Modified to detect container exit (exited or dead states) and fail fast with exit code:
```go
if status == "exited" || status == "dead" {
    return fmt.Errorf("container %s exited (code %d) while waiting for running state",
        name, info.Container.State.ExitCode)
}
```

### Task 2: Added `ContainerExitDiagnostics` Utility ✅ DONE
**File:** `internal/testutil/docker.go`

Added `ContainerExitDiagnostics` struct and `GetContainerExitDiagnostics()` function with:
- Exit code, OOM status, error field
- Last N lines of logs (with Docker stream headers stripped)
- Start/finish timestamps
- Clawker error pattern detection
- Firewall failure detection

### Task 3: Added `stripDockerStreamHeaders` Helper ✅ DONE
**File:** `internal/testutil/docker.go`

Added helper to strip Docker's 8-byte multiplexed stream headers from log output.

### Task 4: Added E2E Test for Container Exit Detection ✅ DONE
**File:** `internal/cmd/container/run/run_e2e_test.go`

Added `TestRunE2E_ContainerExitDetection` that:
- Creates container that exits immediately with code 42
- Verifies WaitForContainerRunning detects exit with code in error message
- Tests GetContainerExitDiagnostics provides useful info

### Task 5: Added Integration Test for Firewall Startup ✅ DONE
**File:** `internal/integration/firewall_startup_test.go` (NEW FILE)

Added tests:
- `TestFirewallStartup_FullScript` - Tests complete init-firewall.sh execution
- `TestFirewallStartup_MissingCapability` - Verifies failure when NET_ADMIN missing
- `TestFirewallStartup_ExitCodeOnFailure` - Verifies non-zero exit on failures

### Task 6: Updated TESTING.md Documentation ✅ DONE
**File:** `.claude/rules/TESTING.md`

Added section on "Container Exit Detection (Fail-Fast)" with:
- Updated WaitForContainerRunning behavior
- GetContainerExitDiagnostics usage examples
- Field descriptions and use cases

## Verification Status

```bash
# Unit tests - PASSED ✅
go test ./...

# Integration tests - PASSED ✅ (2026-01-22)
go test -tags=integration ./internal/integration/... -v -timeout 10m

# E2E tests - PASSED ✅ (2026-01-22)
go test -tags=e2e ./internal/cmd/container/run/... -v -timeout 15m
# TestRunE2E_ContainerExitDetection passed
```

**All tests verified working with Docker.**

## Files Modified

| File | Change |
|------|--------|
| `internal/testutil/docker.go` | Improved `WaitForContainerRunning`, added `GetContainerExitDiagnostics`, `stripDockerStreamHeaders`, `getContainerLogsTail` |
| `internal/cmd/container/run/run_e2e_test.go` | Added `TestRunE2E_ContainerExitDetection` |
| `internal/integration/firewall_startup_test.go` | NEW FILE - firewall startup flow tests |
| `.claude/rules/TESTING.md` | Added container exit detection documentation |

## Additional Fix: Firewall IPv6 Handling (2026-01-22)

During verification, discovered that `init-firewall.sh` was failing on IPv6 CIDRs from GitHub's API (e.g., `2a0a:a440::/29`).

**Fix applied to `pkg/build/templates/init-firewall.sh`:**
```bash
# Skip IPv6 ranges (ipset is IPv4 only)
if [[ "$cidr" =~ : ]]; then
    echo "Skipping invalid range: $cidr"
    continue
fi
# Validate IPv4 CIDR
if [[ ! "$cidr" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}/[0-9]{1,2}$ ]]; then
    echo "ERROR: Invalid CIDR range from GitHub meta: $cidr"
    exit 1
fi
```

**Note:** `.devcontainer/init-firewall.sh` still has the old bug - may need updating if used.

---

## Status: COMPLETE ✅

All implementation verified working. This memory can be deleted if no longer needed.
