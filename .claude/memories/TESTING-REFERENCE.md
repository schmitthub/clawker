# Testing Reference — Detailed Examples

> Extended test patterns and examples. For essential rules, see `.claude/rules/testing.md`.

## Mock Docker Client — Full Example

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
    m := testutil.NewMockDockerClient(t)

    m.Mock.EXPECT().
        ImageList(gomock.Any(), gomock.Any()).
        Return(whail.ImageListResult{
            Items: []whail.ImageSummary{
                {RepoTags: []string{"clawker-myproject:latest"}},
            },
        }, nil)

    result, err := SomeFunctionThatNeedsDocker(ctx, m.Client)
    require.NoError(t, err)
}
```

> **Note:** The mock is generated from `github.com/moby/moby/client.APIClient`. Post-processing is required because mockgen copies unnamed variadic parameters (`_`) which is invalid Go. The Makefile handles this automatically.

---

## Container Exit Detection (Fail-Fast)

`WaitForContainerRunning` fails fast when a container exits:

```go
err := testutil.WaitForContainerRunning(ctx, rawClient, containerName)
if err != nil {
    // Error includes exit code: "container xyz exited (code 1) while waiting for running state"
    t.Fatalf("Container failed to start: %v", err)
}
```

For detailed diagnostics:

```go
diag, err := testutil.GetContainerExitDiagnostics(ctx, rawClient, containerID, 50)
if err == nil {
    t.Logf("Exit code: %d", diag.ExitCode)
    t.Logf("OOMKilled: %v", diag.OOMKilled)
    t.Logf("FirewallFailed: %v", diag.FirewallFailed)
    t.Logf("ClawkerError: %v (%s)", diag.HasClawkerError, diag.ClawkerErrorMsg)
    t.Logf("Logs:\n%s", diag.Logs)
}
```

**ContainerExitDiagnostics fields:** `ExitCode`, `OOMKilled`, `Error`, `Logs` (last N lines), `StartedAt`/`FinishedAt`, `HasClawkerError`/`ClawkerErrorMsg`, `FirewallFailed`

---

## Golden File Testing

```go
testutil.CompareGolden(t, actualBytes, "testdata/expected.golden")
testutil.CompareGoldenString(t, actualString, "testdata/expected.golden")
testutil.GoldenAssert(t, actualBytes, "testdata/expected.golden")
```

Update golden files: `UPDATE_GOLDEN=1 go test ./...`

---

## Build Test Images

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

```go
func TestStopIntegration_BothPatterns(t *testing.T) {
    tests := []struct {
        name    string
        useFlag bool
    }{
        {"with agent flag", true},
        {"with container name", false},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
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

```go
//go:build e2e

func TestRunE2E_InteractiveMode(t *testing.T) {
    testutil.RequireDocker(t)
    binaryPath := buildClawkerBinary(t)

    projectDir := t.TempDir()
    // ... setup clawker.yaml

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

## Error Handling Examples

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

---

## Testcontainers Integration Tests (`internal/testutil/integration/`)

Uses [testcontainers-go](https://golang.testcontainers.org/) for testing scripts in lightweight containers.

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

### Running

```bash
go test -tags=integration ./internal/testutil/integration/... -v -timeout 10m
go test -tags=integration ./internal/testutil/integration/... -run "Firewall" -v -timeout 10m
```

### Key Components

**StartFromDockerfile:**
```go
result, err := StartFromDockerfile(ctx, t, "testdata/Dockerfile.alpine", func(req *testcontainers.ContainerRequest) {
    req.CapAdd = []string{"NET_ADMIN", "NET_RAW"}
    req.User = "root"
    req.ExtraHosts = []string{"host.docker.internal:host-gateway"}
})
```

**ContainerResult:** `Exec(ctx, cmd)`, `WaitForFile(ctx, path, timeout)`, `GetLogs(ctx)`, `CleanOutput()`

**MockHostProxy:**
```go
proxy := NewMockHostProxy(t)
proxyURL := strings.Replace(proxy.URL(), "127.0.0.1", "host.docker.internal", 1)
```

### Script Testing Pattern

Tests copy scripts from `pkg/build/templates/` into containers:

```go
copyScriptToContainer(ctx, t, result, "init-firewall.sh")
execResult, err := result.Exec(ctx, []string{"bash", "/tmp/init-firewall.sh"})
require.Equal(t, 0, execResult.ExitCode, "script failed: %s", execResult.CleanOutput())
```

**IMPORTANT:** Tests use actual scripts from `pkg/build/templates/`. Script changes are automatically tested.
