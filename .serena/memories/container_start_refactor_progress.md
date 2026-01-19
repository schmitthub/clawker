# ContainerStart Refactor Progress

## Goal
Refactor `ContainerStart` in `pkg/whail/container.go` using the same options-based pattern as `ContainerCreate`, adding support for auto-connecting containers to networks on start.

## Progress Status: COMPLETED ✅

All refactoring work has been completed and all tests pass.

### COMPLETED:
1. ✅ Added error functions for NetworkConnect/NetworkDisconnect in `pkg/whail/errors.go` (lines 647-672)
2. ✅ Added NetworkConnect/NetworkDisconnect methods to `pkg/whail/network.go` (lines 153-195)
3. ✅ Added ContainerStartOptions struct to `pkg/whail/container.go` (lines 39-50)
4. ✅ Refactored ContainerStart method in `pkg/whail/container.go` (lines 109-162)
   - Added EnsureNetwork support
   - Added isAlreadyConnectedError helper function
5. ✅ Updated `pkg/cmd/container/run/run.go:283` - uses `whail.ContainerStartOptions{ContainerID: containerID}`
6. ✅ Updated `pkg/cmd/container/start/start.go:129-134` - uses whail.ContainerStartOptions with EnsureNetwork
7. ✅ Updated `pkg/cmd/container/restart/restart.go:114` - uses whail.ContainerStartOptions with EnsureNetwork
8. ✅ Updated `internal/docker/client_test.go:107` - uses `whail.ContainerStartOptions{ContainerID: createResp.ID}`
9. ✅ Updated `pkg/whail/container_test.go:187` - uses `ContainerStartOptions{ContainerID: containerID}`
10. ✅ Added TestNetworkConnect and TestNetworkDisconnect tests to `pkg/whail/network_test.go`
11. ✅ All tests pass: `go test ./... -timeout 5m`

## Key Code Added

### ContainerStartOptions struct (container.go:39-50):
```go
type ContainerStartOptions struct {
	client.ContainerStartOptions // Embedded: CheckpointID, CheckpointDir
	ContainerID string
	EnsureNetwork *EnsureNetworkOptions
}
```

### New ContainerStart signature:
```go
func (e *Engine) ContainerStart(ctx context.Context, opts ContainerStartOptions) (client.ContainerStartResult, error)
```

## Notes
- The `start` and `restart` commands will auto-connect existing containers to `clawker-net`
- The `run` command already connects via ContainerCreate, so doesn't need EnsureNetwork on start
- Tests using `APIClient.ContainerStart` directly (bypassing whail) won't need changes
- `isAlreadyConnectedError` helper handles Docker's "endpoint already exists" errors gracefully