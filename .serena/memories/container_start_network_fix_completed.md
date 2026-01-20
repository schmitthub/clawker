# Container Start Network Fix - COMPLETED

## Status: DONE ✅

**Date:** 2026-01-19
**Branch:** a/start-fix

## Problem Fixed

`clawker start --agent main` was failing with:
```
Error: Failed to start container 'd11b6def...'
  Details: Failed to connect container 'd11b6def...' to network 'clawker-net'
```

The issue was that when a container was created with `EnsureNetwork` (which configures `NetworkingConfig`), it was already connected to the network. Then when `ContainerStart` tried to connect it again via `EnsureNetwork`, Docker returned an error that wasn't properly detected by `isAlreadyConnectedError`.

## Solution Implemented

**File: `pkg/whail/container.go`** (lines 115-167)

In `ContainerStart`, before calling `NetworkConnect`, added a pre-check:
1. Inspect the container to get current network settings
2. Check if already connected to the target network via `info.Container.NetworkSettings.Networks[networkName]`
3. Only call `NetworkConnect` if NOT already connected
4. Keep the `isAlreadyConnectedError` fallback for race conditions

## Test Added

**File: `pkg/whail/container_test.go`**

Added `TestContainerStartWithEnsureNetworkAfterCreateWithEnsureNetwork` (after `getNetworkNames` function) which tests:
1. Create container WITH `EnsureNetwork`
2. Start container (first time)
3. Stop container
4. Start container again with `EnsureNetwork` (the bug scenario)
5. Verify container is running and connected to network

## Files Modified

1. `pkg/whail/container.go` - Pre-check for network connection in `ContainerStart`
2. `pkg/whail/container_test.go` - Integration test for the scenario

## Test Results

All tests pass:
- `go test ./...` ✅
- `go test ./pkg/whail/...` ✅
- Specific new test passes ✅

## Commits Needed

Changes are NOT committed yet. Files modified:
- `pkg/whail/container.go`
- `pkg/whail/container_test.go`

## Next Steps (if continuing)

1. Commit the changes with message like:
   "Fix: Container start fails when already connected to network"
2. Manual verification (optional):
   ```bash
   cd /some/project/with/clawker.yaml
   ./bin/clawker run -it --agent test -- sh -c "echo hello"
   # Ctrl+C to stop
   ./bin/clawker start --agent test  # Should succeed now
   ```
