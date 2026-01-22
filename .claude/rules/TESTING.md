---
paths:
  - "**/*.go"
---

# CLI Testing Guide

> **LLM Memory Document**: Reference this document when writing CLI command tests. Contains test utilities, integration test patterns, and best practices.

## CRITICAL: All Tests Must Pass

**No code change is complete until ALL tests pass.** This is non-negotiable.

```bash
# Unit tests (fast, no Docker required)
go test ./...

# Integration tests (requires Docker)
go test -tags=integration ./pkg/cmd/... -v -timeout 10m

# E2E tests (requires Docker, builds binary)
go test -tags=e2e ./pkg/cmd/... -v -timeout 15m
```

---

## Test Categories

| Category | Build Tag | Location | Docker Required |
|----------|-----------|----------|-----------------|
| Unit | (none) | `*_test.go` | No |
| Integration | `integration` | `*_integration_test.go` | Yes |
| E2E | `e2e` | `*_e2e_test.go` | Yes |

**Naming Convention:**
- Unit tests: `foo_test.go`
- Integration tests: `foo_integration_test.go`
- E2E tests: `foo_e2e_test.go`

---

## Test Utilities (`internal/testutil`)

The `internal/testutil` package provides reusable test infrastructure.

### Key Components

| File | Purpose |
|------|---------|
| `harness.go` | Test harness with project/config setup |
| `docker.go` | Docker client helpers and cleanup |
| `ready.go` | Container readiness detection |
| `config_builder.go` | Fluent config construction |
| `golden.go` | Golden file comparison |
| `hash.go` | Template hashing for cache invalidation |
| `args.go` | Argument parsing helpers |

### Test Harness (`Harness`)

The `Harness` provides isolated test environments with automatic cleanup.

```go
func TestMyCommand(t *testing.T) {
    h := testutil.NewHarness(t,
        testutil.WithProject("myproject"),
        testutil.WithConfigBuilder(
            testutil.NewConfigBuilder().
                WithProject("myproject").
                WithDefaultImage("alpine:latest").
                WithBuild(testutil.DefaultBuild()),
        ),
    )
    // h.ProjectDir contains clawker.yaml
    // h.ContainerName("agent") returns "clawker.myproject.agent"
    // Cleanup is automatic via t.Cleanup()
}
```

**Harness Options:**
- `WithProject(name)` - Set project name
- `WithConfig(cfg)` - Use pre-built config
- `WithConfigBuilder(cb)` - Use config builder

**Harness Methods:**
- `ContainerName(agent)` → `clawker.project.agent`
- `ImageName()` → `clawker-project:hash`
- `VolumeName(purpose)` → `clawker.project.agent-purpose`
- `NetworkName()` → `clawker-project`
- `SetEnv(key, value)` / `UnsetEnv(key)` - Environment manipulation
- `Chdir(path)` - Change working directory (auto-restored)
- `WriteFile(path, content)` / `ReadFile(path)` - File helpers

### Config Builder

Fluent API for constructing `config.Config` objects.

```go
cfg := testutil.NewConfigBuilder().
    WithProject("myproject").
    WithDefaultImage("alpine:latest").
    WithBuild(testutil.BuildWithPackages([]string{"git", "curl"})).
    WithSecurity(testutil.SecurityFirewallEnabled()).
    WithAgent(testutil.AgentWithEnv(map[string]string{"FOO": "bar"})).
    WithWorkspace(testutil.WorkspaceSnapshot()).
    Build()
```

**Presets:**
- `MinimalValidConfig()` - Minimum required fields
- `FullFeaturedConfig()` - All features enabled
- `DefaultBuild()` / `AlpineBuild()` - Common build configs
- `SecurityFirewallEnabled()` / `SecurityFirewallDisabled()`
- `WorkspaceSnapshot()` - Snapshot mode workspace

### Docker Helpers

```go
// Skip test if Docker unavailable
testutil.SkipIfNoDocker(t)

// Require Docker (fail if unavailable)
testutil.RequireDocker(t)

// Create test client (whail.Engine with test labels)
client := testutil.NewTestClient(t)

// Create raw Docker client (for low-level operations)
rawClient := testutil.NewRawDockerClient(t)

// Add labels to container config
config := testutil.AddTestLabels(config, "myproject", "myagent")
config := testutil.AddClawkerLabels(config, "myproject", "myagent")

// Check resource existence
exists := testutil.ContainerExists(t, client, containerID)
running := testutil.ContainerIsRunning(t, client, containerID)
exists := testutil.VolumeExists(t, client, volumeName)
exists := testutil.NetworkExists(t, client, networkName)
```

### Mock Docker Client (Unit Tests)

For unit testing code that uses `docker.Client` **without requiring Docker**, use the mock client:

```go
import (
    "context"
    "testing"

    "github.com/schmitthub/clawker/internal/testutil"
    "github.com/schmitthub/clawker/pkg/whail"
    "github.com/stretchr/testify/require"
    "go.uber.org/mock/gomock"
)

func TestImageResolution(t *testing.T) {
    ctx := context.Background()

    // Create mock client (no Docker required)
    m := testutil.NewMockDockerClient(t)

    // Set expectations using gomock with whail types (NOT moby types directly)
    m.Mock.EXPECT().
        ImageList(gomock.Any(), gomock.Any()).
        Return(whail.ImageListResult{
            Items: []whail.ImageSummary{
                {RepoTags: []string{"clawker-myproject:latest"}},
            },
        }, nil)

    // Pass m.Client to code under test
    result, err := SomeFunctionThatNeedsDocker(ctx, m.Client)
    require.NoError(t, err)
}
```

**MockDockerClient fields:**
- `Mock` - The gomock mock, use `.EXPECT()` to set expectations
- `Client` - The `*docker.Client` to pass to code under test
- `Ctrl` - The gomock controller (rarely needed directly)

**When to use:**
- Unit tests that need to test Docker client interactions without a real daemon
- Testing error handling paths (return errors from mock)
- Fast tests that don't need actual containers

**Regenerating mocks:**

```bash
make generate-mocks
```

> **Note:** The mock is generated from `github.com/moby/moby/client.APIClient`.
> Post-processing is required because mockgen copies the Docker SDK's unnamed
> variadic parameters (using `_`) which is invalid Go syntax. The Makefile
> handles this automatically.

### Cleanup Functions

**CRITICAL:** Always clean up test resources. Use these functions in `t.Cleanup()`:

```go
// Clean up all resources for a project
t.Cleanup(func() {
    ctx := context.Background()
    testutil.CleanupProjectResources(ctx, client, "myproject")
})

// Clean up resources with test label
t.Cleanup(func() {
    ctx := context.Background()
    testutil.CleanupTestResources(ctx, rawClient)
})
```

### Readiness Detection

For tests that need to wait for containers to be ready:

```go
// Wait for ready file (written by entrypoint)
err := testutil.WaitForReadyFile(ctx, rawClient, containerID)

// Wait for health check to pass
err := testutil.WaitForHealthy(ctx, rawClient, containerID)

// Wait for specific log pattern
err := testutil.WaitForLogPattern(ctx, rawClient, containerID, "Server started")

// Wait for ready log (from emit_ready in entrypoint)
err := testutil.WaitForReadyLog(ctx, rawClient, containerID)

// Check for error pattern in logs
hasError := testutil.CheckForErrorPattern(ctx, rawClient, containerID)

// Verify process is running in container
running, err := testutil.VerifyProcessRunning(ctx, rawClient, containerID, "claude")
```

**Timeout Constants:**
- `DefaultReadyTimeout` - 60s (local development)
- `CIReadyTimeout` - 120s (CI environments)
- `E2EReadyTimeout` - 180s (E2E tests)

### Golden File Testing

For output comparison tests:

```go
// Compare bytes against golden file
testutil.CompareGolden(t, actualBytes, "testdata/expected.golden")

// Compare string
testutil.CompareGoldenString(t, actualString, "testdata/expected.golden")

// Assert with automatic update (set UPDATE_GOLDEN=1)
testutil.GoldenAssert(t, actualBytes, "testdata/expected.golden")
```

Update golden files: `UPDATE_GOLDEN=1 go test ./...`

### Build Test Images

For tests requiring custom images:

```go
imageTag := testutil.BuildTestImage(t, testutil.NewRawDockerClient(t),
    "FROM alpine:latest\nRUN apk add bash",
    testutil.BuildTestImageOptions{
        SuppressOutput: true,
        NoCache:        false,
    },
)
// Image is automatically cleaned up via t.Cleanup()
```

---

## Integration Test Patterns

### Basic Command Test

```go
//go:build integration

package mycommand

import (
    "testing"
    "github.com/stretchr/testify/require"
    "github.com/yourorg/clawker/internal/testutil"
)

func TestMyCommand_Integration(t *testing.T) {
    testutil.RequireDocker(t)

    h := testutil.NewHarness(t,
        testutil.WithProject("test-project"),
        testutil.WithConfigBuilder(testutil.NewConfigBuilder().
            WithProject("test-project").
            WithDefaultImage("alpine:latest"),
        ),
    )

    t.Cleanup(func() {
        ctx := context.Background()
        client := testutil.NewTestClient(t)
        testutil.CleanupProjectResources(ctx, client, "test-project")
    })

    // Test implementation
}
```

### Table-Driven Integration Tests

```go
func TestRunIntegration_ArbitraryCommand(t *testing.T) {
    testutil.RequireDocker(t)

    tests := []struct {
        name        string
        args        []string
        wantOutput  string
        wantErr     bool
        errContains string
    }{
        {
            name:       "echo command",
            args:       []string{"run", "--rm", "alpine", "echo", "hello"},
            wantOutput: "hello\n",
        },
        {
            name:        "command not found",
            args:        []string{"run", "--rm", "alpine", "notacommand"},
            wantErr:     true,
            errContains: "not found",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ... test implementation
        })
    }
}
```

### Testing Both Invocation Patterns

All container commands should test BOTH patterns:
1. `--agent` flag: `clawker container stop --agent ralph`
2. Container name: `clawker container stop clawker.project.ralph`

```go
func TestStopIntegration_BothPatterns(t *testing.T) {
    tests := []struct {
        name    string
        useFlag bool // true = --agent, false = container name
    }{
        {"with agent flag", true},
        {"with container name", false},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Setup container...

            var args []string
            if tt.useFlag {
                args = []string{"container", "stop", "--agent", "ralph"}
            } else {
                args = []string{"container", "stop", h.ContainerName("ralph")}
            }

            // Execute and verify...
        })
    }
}
```

---

## E2E Test Patterns

E2E tests build the actual binary and test full workflows.

```go
//go:build e2e

func TestRunE2E_InteractiveMode(t *testing.T) {
    testutil.RequireDocker(t)

    // Build clawker binary
    binaryPath := buildClawkerBinary(t)

    // Create temp project directory
    projectDir := t.TempDir()
    // ... setup clawker.yaml

    // Run binary
    cmd := exec.Command(binaryPath, "run", "--rm", "alpine", "sh")
    cmd.Dir = projectDir

    // ... test interactive I/O
}

func buildClawkerBinary(t *testing.T) string {
    t.Helper()

    projectRoot := testutil.FindProjectRoot()
    binaryPath := filepath.Join(t.TempDir(), "clawker")

    cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/clawker")
    cmd.Dir = projectRoot

    output, err := cmd.CombinedOutput()
    require.NoError(t, err, "build failed: %s", output)

    return binaryPath
}
```

---

## Error Handling in Tests

### NEVER Silently Discard Errors

```go
// BAD - silent failure
_, _ = client.ContainerRemove(ctx, id, true)

// GOOD - collect and report errors
if _, err := client.ContainerRemove(ctx, id, true); err != nil {
    t.Logf("WARNING: cleanup failed: %v", err)
}

// BETTER - collect all errors
var errs []error
for _, c := range containers {
    if _, err := client.ContainerRemove(ctx, c.ID, true); err != nil {
        errs = append(errs, fmt.Errorf("remove %s: %w", c.ID[:12], err))
    }
}
if len(errs) > 0 {
    return errors.Join(errs...)
}
```

### Fail Fast on Container Exit

```go
// When waiting for container readiness, check if it exited
info, err := cli.ContainerInspect(ctx, containerID)
if err == nil && !info.State.Running {
    return fmt.Errorf("container exited (code %d) while waiting", info.State.ExitCode)
}
```

---

## Test Naming Conventions

```go
// Unit tests
func TestFunctionName(t *testing.T)
func TestFunctionName_Scenario(t *testing.T)

// Integration tests
func TestFeature_Integration(t *testing.T)
func TestFeatureIntegration_Scenario(t *testing.T)

// E2E tests
func TestFeature_E2E(t *testing.T)
func TestFeatureE2E_Scenario(t *testing.T)
```

**Agent name uniqueness:**
```go
// Include timestamp AND random suffix for parallel safety
agentName := fmt.Sprintf("test-%s-%s-%d",
    t.Name(),
    time.Now().Format("150405"),
    rand.Intn(10000),
)
```

---

## Common Gotchas

1. **Parallel test conflicts**: Use unique agent names with random suffixes
2. **Cleanup order**: Stop containers before removing them
3. **Context cancellation**: Use `context.Background()` in cleanup functions
4. **Timeout selection**: Use appropriate timeout constants for environment
5. **Docker availability**: Always check with `RequireDocker(t)` or `SkipIfNoDocker(t)`
6. **Resource leaks**: Always use `t.Cleanup()` for resource cleanup
7. **Exit code handling**: Container exit code 0 doesn't mean success if ready file missing
8. **Log streaming**: Connection errors indicate container death, not transient issues

---

## Quick Reference

```go
// Setup
testutil.RequireDocker(t)
h := testutil.NewHarness(t, testutil.WithProject("test"))
client := testutil.NewTestClient(t)

// Create resources
containerID := createTestContainer(t, client, h)

// Wait for ready
err := testutil.WaitForReadyFile(ctx, rawClient, containerID)

// Verify
require.True(t, testutil.ContainerIsRunning(t, client, containerID))

// Cleanup (automatic via t.Cleanup in NewHarness)
```
