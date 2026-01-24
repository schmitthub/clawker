# Integration Tests for Scripts and Components

**Status: COMPLETE** - All integration tests pass.
**Updated:** 2026-01-22 - Added firewall IPv6 handling fix, documented in TESTING.md

**Goal:** Implement comprehensive integration tests for container start command and component integration tests for clawker scripts (entrypoint, firewall, ssh-agent, git-credentials, callback-forwarder).

## Final Summary

All **17 component integration tests** now pass:
- 5 firewall tests
- 5 SSH agent tests  
- 3 git credential tests
- 4 script tests (entrypoint, host-open, callback-forwarder)

All **7 container start integration tests** continue to pass.

## Completed Tasks

- [x] **Task 1: Add testcontainers-go dependency**
- [x] **Task 2: Create start_integration_test.go** 
- [x] **Task 3: Create internal/integration package structure**
- [x] **Task 4: Create component integration test files**
- [x] **Task 4.1: Fix permission issue in copyScriptToContainer**
- [x] **Task 4.2: Fix entrypoint sourcing issue in scripts_test.go**
- [x] **Task 4.3: Fix sshagent_test.go sourcing issues**
- [x] **Task 4.4.1: Add StripDockerStreamHeaders helper**
- [x] **Task 4.4.2: Fix TestFirewall_AllowedDomainsReachable**
- [x] **Task 4.4.3: Fix TestCallbackForwarder_PollsProxy mock response format**
- [x] **Task 4.4.4: Fix TestFirewall_HostDockerInternalAllowed** - VERIFIED PASSING
- [x] **Task 4.4.5: Fix TestCallbackForwarder_PollsProxy** - Added `received: true` field to mock response
- [x] **Task 4.4.6: Fix TestGitCredential tests** - Changed mock from `operation` to `action` field, added `success: true`
- [x] **Task 4.5: All component tests verified passing**
- [x] **Task 5: Final verification** - All tests pass

## Key Lessons Learned

1. **Sourcing entrypoint.sh runs global code**: When you `source entrypoint.sh`, all the global-level code executes including `exec "$@"`. Fix: Inline specific functions instead of sourcing.

2. **Docker exec output contains multiplexed stream headers**: The 8-byte headers (stream type + size) must be stripped. Added `StripDockerStreamHeaders()` and `CleanOutput()` helper.

3. **host.docker.internal DNS resolution varies**: Use both `getent hosts` AND `getent ahostsv4` to get all possible IPs. Also need to allow HOST_NETWORK (the /24 subnet from default gateway).

4. **Mock responses must match script expectations**: Scripts check for specific fields like `received`, `success`, use `action` vs `operation`.

## Files Modified

- `internal/integration/container.go` - Added `StripDockerStreamHeaders()` and `CleanOutput()`
- `internal/integration/sshagent_test.go` - Fixed entrypoint sourcing issues
- `internal/integration/firewall_test.go` - Updated to use `CleanOutput()`, fixed host network detection
- `internal/integration/scripts_test.go` - Updated to use `CleanOutput()`, fixed `Operation` -> `Action`
- `internal/integration/hostproxy.go` - Fixed callback response format (added `received`), fixed git credential mock (changed `operation` to `action`, added `success`)

## Verification Commands

```bash
# Run all unit tests
go test ./...

# Run component integration tests
go test -tags=integration ./internal/integration/... -v -timeout 10m

# Run all integration tests
go test -tags=integration ./internal/cmd/... ./internal/integration/... -v -timeout 15m
```
