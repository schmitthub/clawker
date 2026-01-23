# Integration Test Strategy

**Updated:** 2026-01-22
**Status:** IMPLEMENTED

## Summary

Integration tests are designed to verify:
1. CLI mechanics - flags parsed correctly, arguments passed through
2. Docker API calls - correct API methods called with correct parameters
3. Whail isolation - `com.clawker.managed=true` label applied
4. Naming conventions - containers named `clawker.project.agent`
5. Resource filtering - only managed resources returned in list operations

## Key Decisions

### Use Vanilla Alpine Images
Integration tests use `alpine:latest` instead of clawker-built images. This:
- Eliminates slow image building (was 10+ minutes, now ~10 seconds)
- Removes flaky Claude Code npm installation
- Tests the actual Docker client integration, not Claude Code functionality

### Test Categories

| Category | Build Tag | Location | Docker Required | Image Type |
|----------|-----------|----------|-----------------|------------|
| Unit | (none) | `*_test.go` | No | N/A |
| Integration | `integration` | `*_integration_test.go` | Yes | alpine:latest |
| E2E | `e2e` | `*_e2e_test.go` | Yes | clawker-built |

### File Split Strategy
- **Unit tests** (`*_test.go`): No Docker dependency, fast
- **Integration tests** (`*_integration_test.go`): Require Docker, test client integration
- E2E tests that need Claude Code functionality should be marked with `//go:build e2e`

### Client Usage Rules

```
pkg/cmd/* → internal/docker.Client → pkg/whail.Engine → Docker daemon
```

In tests:
- Use `testutil.NewTestClient()` for standard tests - returns `*docker.Client`
- Use `testutil.NewRawDockerClient()` only for isolation testing (creating unmanaged resources)

### Wait Mechanisms

For vanilla containers (no clawker entrypoint):
- `testutil.WaitForContainerExit()` - waits for container to exit with code 0
- Simple polling, no ready signal expected

For clawker images (with entrypoint):
- `testutil.WaitForContainerCompletion()` - checks for ready signal
- `testutil.WaitForReadyFile()` - checks for ready file in container

## Test Files Changed

| File | Action |
|------|--------|
| `pkg/cmdutil/resolve_unit_test.go` | Created - unit tests without Docker |
| `pkg/cmdutil/resolve_integration_test.go` | Created - Docker-dependent tests |
| `pkg/cmdutil/resolve_test.go` | Deleted |
| `internal/docker/client_integration_test.go` | Created - all Docker client tests |
| `internal/docker/client_test.go` | Deleted |
| `pkg/cmd/container/run/run_integration_test.go` | Modified - converted to alpine |
| `pkg/cmd/container/exec/exec_integration_test.go` | Modified - converted to alpine |

## Test Utilities

### Wait Functions (internal/testutil/docker.go)

| Function | Purpose | Use Case |
|----------|---------|----------|
| `WaitForContainerRunning` | Wait for container to be in running state | After `ContainerStart`, before exec/attach |
| `WaitForContainerExit` | Wait for container to exit with code 0 | Integration tests with vanilla images |

```go
// WaitForContainerRunning waits for a container to exist and be in running state.
// Polls every 500ms until the context is cancelled or the container is running.
// Use this after starting a container before exec/attach operations.
func WaitForContainerRunning(ctx context.Context, cli *client.Client, name string) error

// WaitForContainerExit waits for a container to exit with code 0.
// Use for integration tests with vanilla images that don't emit ready signals.
func WaitForContainerExit(ctx context.Context, cli *client.Client, containerID string) error
```

### Readiness Functions (internal/testutil/ready.go)

| Function | Purpose | Use Case |
|----------|---------|----------|
| `WaitForReadyFile` | Wait for ready file in container | Clawker images with entrypoint |
| `WaitForReadyLog` | Wait for ready log message | Clawker images with emit_ready |
| `WaitForHealthy` | Wait for health check | Containers with HEALTHCHECK |
| `WaitForLogPattern` | Wait for specific log pattern | General log-based readiness |

**Key Pattern**: Don't duplicate wait functions in test files - use the testutil functions.

## Performance Results

- Integration tests: ~10-55 seconds (depending on caching)
- Target: < 3 minutes for full suite
- Previous: 10-15+ minutes (with BuildTestImage)

## Best Practices Learned

### Don't Duplicate testutil Functions
**Problem**: `exec_integration_test.go` had a local `waitForContainerRunning` that duplicated `testutil.WaitForContainerRunning`.

**Solution**: Always check `internal/testutil` before writing wait/helper functions. The testutil versions are:
- More robust (ticker-based polling vs sleep loops)
- Better error messages with context
- Consistent across all tests

### Wait Function Locations
- `internal/testutil/docker.go` - Container state waiting (running, exit)
- `internal/testutil/ready.go` - Application readiness (ready file, health, logs)

## Testcontainers Integration Tests (`internal/integration/`)

A separate integration test package using [testcontainers-go](https://golang.testcontainers.org/) for testing clawker scripts in lightweight containers.

### Package Structure

| File | Purpose |
|------|---------|
| `container.go` | `LightContainer` builder and `ContainerResult` wrapper |
| `hostproxy.go` | `MockHostProxy` for testing host proxy interactions |
| `scripts_test.go` | Entrypoint, git config, SSH known hosts, host-open, git-credential tests |
| `firewall_test.go` | Firewall rule verification (iptables, ipset, blocked domains) |
| `firewall_startup_test.go` | Firewall script startup flow tests |
| `sshagent_test.go` | SSH agent proxy forwarding tests |
| `testdata/` | Dockerfiles for Alpine and Debian test containers |

### Key Components

**LightContainer Builder:**
```go
container := NewLightContainer().
    WithBaseImage("alpine:latest").
    WithScripts("entrypoint.sh", "host-open.sh").
    WithCapabilities("NET_ADMIN", "NET_RAW").
    WithEnv("CLAWKER_HOST_PROXY", proxyURL).
    WithExposedPorts("8080/tcp")
result, err := container.Start(ctx, t)
```

**StartFromDockerfile:**
```go
result, err := StartFromDockerfile(ctx, t, "testdata/Dockerfile.alpine", func(req *testcontainers.ContainerRequest) {
    req.CapAdd = []string{"NET_ADMIN", "NET_RAW"}
    req.User = "root"
    req.ExtraHosts = []string{"host.docker.internal:host-gateway"}
})
```

**ContainerResult Methods:**
- `Exec(ctx, cmd)` - Execute command and return `ExecResult`
- `WaitForFile(ctx, path, timeout)` - Wait for file to exist
- `GetLogs(ctx)` - Retrieve container logs
- `CleanOutput()` - Strip Docker stream headers from output

**MockHostProxy:**
```go
proxy := NewMockHostProxy(t)
// proxy.URL() - Get the mock server URL
// proxy.GetOpenedURLs() - Check URLs sent to /open/url
// proxy.GetGitCreds() - Check git credential requests
// proxy.SetCallbackReady(sessionID, path, query) - Simulate OAuth callback
```

### Running Tests

```bash
# All testcontainers integration tests
go test -tags=integration ./internal/integration/... -v -timeout 10m

# Specific test suites
go test -tags=integration ./internal/integration/... -run "Firewall" -v -timeout 10m
go test -tags=integration ./internal/integration/... -run "Entrypoint" -v -timeout 5m
go test -tags=integration ./internal/integration/... -run "GitCredential" -v -timeout 5m
go test -tags=integration ./internal/integration/... -run "SshAgent" -v -timeout 5m
```

### Script Testing Pattern

Tests copy scripts from `pkg/build/templates/` into containers:

```go
// Copy script to container
copyScriptToContainer(ctx, t, result, "init-firewall.sh")

// Run script
execResult, err := result.Exec(ctx, []string{"bash", "/tmp/init-firewall.sh"})
require.NoError(t, err)
require.Equal(t, 0, execResult.ExitCode, "script failed: %s", execResult.CleanOutput())
```

**IMPORTANT:** These tests use the actual scripts from `pkg/build/templates/`, providing regression testing when scripts are modified.

### Firewall Test Details

The firewall tests verify:
1. **iptables rules** - DROP policy on OUTPUT, ACCEPT for allowed domains
2. **ipset creation** - `allowed-domains` hash:net set with GitHub IPs
3. **Blocked domains** - example.com unreachable, GitHub reachable
4. **Host access** - host.docker.internal and Docker networks allowed
5. **IPv6 handling** - IPv6 CIDRs from GitHub API are skipped (ipset is IPv4 only)

**IPv6 Skip Logic** (fixed 2026-01-22):
```bash
# Skip IPv6 ranges (ipset is IPv4 only)
if [[ "$cidr" =~ : ]]; then
    echo "Skipping invalid range: $cidr"
    continue
fi
```

## Notes

- Tests requiring Claude Code functionality (Claude flags, entrypoint behavior) should be in E2E tests
- BuildTestImage is still available for E2E tests but not used in integration tests
- Deleted ClaudeFlagsPassthrough test with TODO for future E2E implementation
- Always use `testutil.WaitForContainerRunning` after `ContainerStart` - never write local implementations
- Testcontainers tests in `internal/integration/` are separate from `pkg/cmd/*_integration_test.go` tests