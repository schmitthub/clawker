# Testing Patterns - Docker CLI Repository

## Overview

The Docker CLI testing infrastructure demonstrates **exceptional pattern consistency** built on Go's standard testing with strategic enhancements. All 8 core patterns are used uniformly across 400+ test files with no mixing of mocking strategies, assertion libraries, or test organization approaches.

## Pattern 1: FakeCli with Function Field Injection

The most distinctive pattern in the codebase. Replaces traditional mocking frameworks (gomock, testify/mock) with a custom test double using struct fields containing function pointers.

### Implementation

```go
// internal/test/cli.go - Shared FakeCli
type FakeCli struct {
    command.DockerCli
    client    client.APIClient
    outBuffer *bytes.Buffer
    errBuffer *bytes.Buffer
    inBuffer  *bytes.Buffer
    // ... config, notary, etc.
}

func NewFakeCli(client client.APIClient, opts ...func(*FakeCli)) *FakeCli {
    cli := &FakeCli{
        client:    client,
        outBuffer: new(bytes.Buffer),
        errBuffer: new(bytes.Buffer),
    }
    for _, opt := range opts {
        opt(cli)
    }
    return cli
}
```

### Per-Package fakeClient

```go
// cli/command/container/client_test.go
type fakeClient struct {
    client.Client // Embed interface for defaults

    containerListFunc    func(context.Context, container.ListOptions) ([]container.Summary, error)
    containerCreateFunc  func(*container.Config, *container.HostConfig, ...) (container.CreateResponse, error)
    containerInspectFunc func(string) (container.InspectResponse, error)
    containerStartFunc   func(string, container.StartOptions) error
    containerStopFunc    func(string, container.StopOptions) error
    containerRemoveFunc  func(string, container.RemoveOptions) error
    // ... more function fields
}

func (f *fakeClient) ContainerList(ctx context.Context, opts container.ListOptions) ([]container.Summary, error) {
    if f.containerListFunc != nil {
        return f.containerListFunc(ctx, opts)
    }
    return nil, nil
}
```

### Usage Pattern

```go
func TestContainerList(t *testing.T) {
    cli := test.NewFakeCli(&fakeClient{
        containerListFunc: func(_ context.Context, opts container.ListOptions) ([]container.Summary, error) {
            return []container.Summary{
                {ID: "abc123", Names: []string{"/web"}},
            }, nil
        },
    })

    cmd := newListCommand(cli)
    cmd.SetArgs([]string{"--format", "{{.ID}}"})
    assert.NilError(t, cmd.Execute())
    assert.Check(t, is.Equal("abc123\n", cli.OutBuffer().String()))
}
```

### Key Properties
- **Type-safe:** Function signatures match the real API client interface
- **Explicit:** Each test declares exactly which methods it uses
- **Zero dependencies:** No code generation, no reflection, no external libraries
- **Safe defaults:** Nil function fields return zero values (no panics)

## Pattern 2: Builder Pattern for Test Fixtures

9 fluent builders using functional options pattern for composable test data construction.

### Available Builders

| Builder | Location | Builds |
|---------|----------|--------|
| Container | `internal/test/builders/container.go` | `types.Container` |
| Network | `internal/test/builders/network.go` | `network.Summary` |
| Service | `internal/test/builders/service.go` | `swarm.Service` |
| Config | `internal/test/builders/config.go` | `swarm.Config` |
| Secret | `internal/test/builders/secret.go` | `swarm.Secret` |
| Volume | `internal/test/builders/volume.go` | `volume.Volume` |
| Node | `internal/test/builders/node.go` | `swarm.Node` |
| Task | `internal/test/builders/task.go` | `swarm.Task` |
| Swarm | `internal/test/builders/swarm.go` | `swarm.Swarm` |

### Usage

```go
// Composable test data
container := builders.Container("web",
    builders.WithPort(80, 8080, builders.TCP),
    builders.WithLabel("app", "nginx"),
    builders.WithStatus("running"),
    builders.WithSize(1024),
)

// Multiple containers
containers := []types.Container{
    *builders.Container("web", builders.WithLabel("role", "frontend")),
    *builders.Container("api", builders.WithLabel("role", "backend")),
}
```

### Builder Implementation Pattern

```go
// Functional option type
type ContainerOption func(*types.Container)

// Builder function
func Container(name string, opts ...ContainerOption) *types.Container {
    c := &types.Container{
        ID:    "container_id_" + name,
        Names: []string{"/" + name},
    }
    for _, opt := range opts {
        opt(c)
    }
    return c
}

// Option functions
func WithLabel(key, value string) ContainerOption {
    return func(c *types.Container) {
        if c.Labels == nil {
            c.Labels = map[string]string{}
        }
        c.Labels[key] = value
    }
}

func WithPort(private, public uint16, proto string) ContainerOption {
    return func(c *types.Container) {
        c.Ports = append(c.Ports, types.Port{
            PrivatePort: private,
            PublicPort:  public,
            Type:        proto,
        })
    }
}
```

## Pattern 3: Golden File Testing

Extensive use of `gotest.tools/v3/golden` for output validation with version-controlled expected outputs.

### Structure

```
cli/command/container/
├── list.go
├── list_test.go
└── testdata/
    ├── container-list-default.golden
    ├── container-list-json.golden
    ├── container-list-quiet.golden
    └── container-list-custom-format.golden
```

### Usage

```go
func TestContainerListFormat(t *testing.T) {
    cli := test.NewFakeCli(&fakeClient{
        containerListFunc: func(...) ([]container.Summary, error) {
            return testContainers, nil
        },
    })

    cmd := newListCommand(cli)
    cmd.SetArgs([]string{"--format", "table"})
    assert.NilError(t, cmd.Execute())

    // Compare output against golden file
    golden.Assert(t, cli.OutBuffer().String(), "container-list-default.golden")
}
```

### Update Workflow

```bash
# When output intentionally changes, update golden files:
UPDATE_GOLDEN=1 go test ./cli/command/container/...

# Then review and commit the changes:
git diff testdata/
```

### Key Properties
- 100+ golden files across the codebase
- Files stored in `testdata/` subdirectories per package
- Automatic diff generation on mismatch
- `UPDATE_GOLDEN=1` environment variable for acceptance

## Pattern 4: Table-Driven Tests with Subtests

Pervasive pattern using anonymous structs with `name` field for `t.Run()` subtests.

### Standard Structure

```go
func TestContainerRunOptions(t *testing.T) {
    tests := []struct {
        name        string
        args        []string
        expectedErr string
    }{
        {
            name: "no image specified",
            args: []string{},
            expectedErr: "requires at least 1 argument",
        },
        {
            name: "conflicting options",
            args: []string{"--rm", "-d", "alpine"},
            expectedErr: "",  // no error expected
        },
        {
            name: "invalid memory format",
            args: []string{"--memory", "invalid", "alpine"},
            expectedErr: "invalid memory format",
        },
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            cli := test.NewFakeCli(&fakeClient{})
            cmd := newRunCommand(cli)
            cmd.SetArgs(tc.args)
            err := cmd.Execute()

            if tc.expectedErr != "" {
                assert.ErrorContains(t, err, tc.expectedErr)
            } else {
                assert.NilError(t, err)
            }
        })
    }
}
```

### Characteristics
- Typical test has 3-10 cases
- Tests cover edge cases, error conditions, and happy paths
- Anonymous struct with `name` field for clear subtest naming
- Each case is independent and self-documenting

## Pattern 5: Stream Capture Pattern

FakeCli wraps `bytes.Buffer` instances to capture stdout/stderr for assertion.

### Output Capture

```go
func TestCommandOutput(t *testing.T) {
    cli := test.NewFakeCli(&fakeClient{...})

    cmd := newInspectCommand(cli)
    cmd.SetArgs([]string{"container1"})
    assert.NilError(t, cmd.Execute())

    // Check stdout
    assert.Check(t, is.Contains(cli.OutBuffer().String(), "container1"))

    // Check stderr
    assert.Check(t, is.Equal("", cli.ErrBuffer().String()))
}
```

### Input Simulation

```go
func TestInteractiveInput(t *testing.T) {
    cli := test.NewFakeCli(&fakeClient{...})

    // Simulate user input
    cli.SetIn(streams.NewIn(io.NopCloser(strings.NewReader("y\n"))))

    cmd := newRemoveCommand(cli)
    cmd.SetArgs([]string{"--force", "container1"})
    assert.NilError(t, cmd.Execute())
}
```

## Pattern 6: E2E Testing with icmd

`gotest.tools/v3/icmd` wrapper for executing real Docker CLI binary.

### E2E Test Structure

```go
// e2e/container/run_test.go
func TestRunBasic(t *testing.T) {
    skip.If(t, environment.RemoteDaemon())

    result := icmd.RunCommand("docker", "run", "--rm", "alpine", "echo", "hello")
    result.Assert(t, icmd.Expected{
        ExitCode: 0,
        Out:      "hello",
    })
}
```

### Environment Setup (TestMain)

```go
func TestMain(m *testing.M) {
    setup := environment.Setup()  // Detect daemon, configure TLS
    os.Exit(m.Run())
}
```

### Skip Helpers

```go
skip.If(t, environment.RemoteDaemon())
skip.If(t, !environment.DaemonIsLinux())
skip.If(t, versions.LessThan(testEnv.DaemonAPIVersion(), "1.40"))
skip.If(t, !testEnv.IsExperimentalDaemon())
```

### Polling for Async Operations

```go
func TestContainerRestart(t *testing.T) {
    // Start container
    icmd.RunCommand("docker", "run", "-d", "--name", "test", "alpine", "sleep", "100")

    // Restart and poll for state
    icmd.RunCommand("docker", "restart", "test")

    poll.WaitOn(t, containerIsRunning("test"), poll.WithTimeout(30*time.Second))
}

func containerIsRunning(name string) poll.Check {
    return func(t poll.LogT) poll.Result {
        result := icmd.RunCommand("docker", "inspect", "-f", "{{.State.Running}}", name)
        if result.Stdout() == "true\n" {
            return poll.Success()
        }
        return poll.Continue("container not yet running")
    }
}
```

## Pattern 7: Error Simulation via Mock Functions

Tests inject errors by returning them from function field mocks.

```go
func TestContainerStartError(t *testing.T) {
    cli := test.NewFakeCli(&fakeClient{
        containerStartFunc: func(string, container.StartOptions) error {
            return errors.New("cannot start container: permission denied")
        },
    })

    cmd := newStartCommand(cli)
    cmd.SetArgs([]string{"container1"})
    err := cmd.Execute()
    assert.ErrorContains(t, err, "permission denied")
}
```

### Error Type Simulation

```go
// Simulate specific Docker API errors
containerInspectFunc: func(id string) (container.InspectResponse, error) {
    return container.InspectResponse{}, errdefs.NotFound(
        fmt.Errorf("No such container: %s", id),
    )
}
```

## Pattern 8: Async Testing with Channels

For concurrent behavior like container attach, signal handling, and process termination.

```go
func TestRunAttach(t *testing.T) {
    done := make(chan error, 1)
    cli := test.NewFakeCli(&fakeClient{
        containerAttachFunc: func(ctx context.Context, id string, opts container.AttachOptions) (types.HijackedResponse, error) {
            // Simulate attach stream
            return types.HijackedResponse{
                Reader: io.NopCloser(strings.NewReader("output")),
                Conn:   &fakeConn{},
            }, nil
        },
    })

    go func() {
        cmd := newRunCommand(cli)
        cmd.SetArgs([]string{"alpine"})
        done <- cmd.Execute()
    }()

    select {
    case err := <-done:
        assert.NilError(t, err)
    case <-time.After(10 * time.Second):
        t.Fatal("timeout waiting for command")
    }
}
```

## Assertion Library Usage

All tests use `gotest.tools/v3/assert` exclusively:

```go
import (
    "gotest.tools/v3/assert"
    is "gotest.tools/v3/assert/cmp"
)

// Equality
assert.Equal(t, actual, expected)
assert.Check(t, is.Equal(actual, expected))

// Error checking
assert.NilError(t, err)
assert.ErrorContains(t, err, "expected message")
assert.Check(t, is.ErrorContains(err, "partial"))

// Collection checks
assert.Check(t, is.Len(items, 3))
assert.Check(t, is.Contains(output, "expected"))

// Deep equality (with go-cmp)
assert.Check(t, is.DeepEqual(actual, expected))
assert.DeepEqual(t, actual, expected, cmpopts.EquateEmpty())

// Boolean
assert.Assert(t, condition)
assert.Check(t, is.Equal(true, result))

// Golden files
golden.Assert(t, output, "expected-output.golden")
```

## Naming Conventions

| Element | Convention | Example |
|---------|-----------|---------|
| Test functions | `Test<Subject>[<Case>]` | `TestRunLabel`, `TestNewImagesCommandErrors` |
| Fake types | `fake<Type>` | `fakeClient`, `fakeStore` |
| Builder options | `With<Property>` | `WithLabel`, `WithPort`, `WithStatus` |
| Golden files | `<command>-<variant>.golden` | `container-list-ids.golden` |
| Test helpers | `new<Type>` or descriptive | `newRunCommand`, `containerIsRunning` |
| Test data | `testdata/` directory | `testdata/*.golden` |
| Subtests | Descriptive lowercase | `"no image specified"`, `"conflicting options"` |

## Pattern Consistency Assessment

| Pattern | Consistency | Notes |
|---------|------------|-------|
| FakeCli usage | **HIGH** | Used in every command test |
| Function-field mocking | **HIGH** | 19 packages, same pattern |
| Builder pattern | **HIGH** | All builders follow same structure |
| Golden files | **HIGH** | Consistent testdata/ structure |
| Table-driven tests | **HIGH** | Standard Go idiom, widely used |
| Assert library | **HIGH** | gotest.tools/v3 only, no mixing |
| Naming conventions | **HIGH** | Uniform across all packages |
| E2E patterns | **MEDIUM** | Less standardized than unit tests |

## Key Reference Files

| File | Pattern |
|------|---------|
| `internal/test/cli.go` | FakeCli implementation |
| `cli/command/container/client_test.go` | fakeClient pattern |
| `internal/test/builders/container.go` | Builder pattern |
| `cli/command/container/list_test.go` | Golden files + table tests |
| `e2e/container/run_test.go` | E2E patterns |
| `internal/test/environment/testenv.go` | Environment detection |
