# I/O & Error Handling in Tests - Docker CLI Repository

## Overview

The Docker CLI testing infrastructure employs a two-tier I/O strategy: **unit tests** capture all output through the FakeCli pattern (in-memory `bytes.Buffer` backing stdout/stderr streams), while **E2E tests** execute the real CLI binary as a subprocess via `gotest.tools/v3/icmd` and capture process-level stdout/stderr/exit codes.

## Unit Test I/O: FakeCli Stream Capture

### Buffer-Backed Streams

```go
// internal/test/cli.go
type FakeCli struct {
    outBuffer *bytes.Buffer  // Captures stdout
    errBuffer *bytes.Buffer  // Captures stderr
    inBuffer  *bytes.Buffer  // Simulates stdin
}

// Output inspection after command execution
output := fakeCLI.OutBuffer().String()
errOutput := fakeCLI.ErrBuffer().String()
```

### Stdin Simulation

```go
// Simulate user typing "y\n" for confirmation prompts
cli.SetIn(streams.NewIn(io.NopCloser(strings.NewReader("y\n"))))

cmd := newPruneCommand(cli)
cmd.SetArgs([]string{})
assert.NilError(t, cmd.Execute())
```

### Output Assertion Patterns

```go
// Exact match via golden file
golden.Assert(t, cli.OutBuffer().String(), "expected-output.golden")

// Contains check
assert.Check(t, is.Contains(cli.OutBuffer().String(), "container1"))

// Line-by-line assertion (internal/test/output)
output.Assert(t, cli.OutBuffer().String(), map[int]func(string) error{
    0: output.Prefix("CONTAINER ID"),
    1: output.Contains("web"),
})

// Empty error output
assert.Check(t, is.Equal("", cli.ErrBuffer().String()))
```

## E2E Test I/O: Subprocess Capture

### icmd Command Execution

```go
// Execute real docker binary
result := icmd.RunCommand("docker", "run", "--rm", "alpine", "echo", "hello")

// Assert stdout, stderr, and exit code
result.Assert(t, icmd.Expected{
    ExitCode: 0,
    Out:      "hello",
    Err:      "",
})

// Access raw output
stdout := result.Stdout()
stderr := result.Stderr()
combined := result.Combined()
```

### PTY-Based I/O (Terminal Tests)

```go
// e2e/container tests using creack/pty
p, tty, err := pty.Open()
require.NoError(t, err)
defer p.Close()

cmd := exec.Command("docker", "run", "-it", "alpine", "sh")
cmd.Stdin = tty
cmd.Stdout = tty
cmd.Stderr = tty

err = cmd.Start()
require.NoError(t, err)

// Read from PTY
buf := make([]byte, 1024)
n, _ := p.Read(buf)
```

## Error Simulation via fakeClient

### Function-Field Error Injection

```go
// Simple error return
fakeClient{
    containerStartFunc: func(string, container.StartOptions) error {
        return errors.New("cannot start: permission denied")
    },
}

// Conditional errors (per-ID behavior)
fakeClient{
    containerInspectFunc: func(id string) (container.InspectResponse, error) {
        switch id {
        case "exists":
            return container.InspectResponse{...}, nil
        default:
            return container.InspectResponse{}, errdefs.NotFound(
                fmt.Errorf("No such container: %s", id),
            )
        }
    },
}
```

### Typed Error Simulation

```go
// Docker API error types (github.com/containerd/errdefs)
errdefs.NotFound(fmt.Errorf("no such container: %s", id))
errdefs.InvalidParameter(fmt.Errorf("invalid config"))
errdefs.Conflict(fmt.Errorf("container already exists"))
errdefs.Forbidden(fmt.Errorf("operation not permitted"))

// StatusError for HTTP-level errors
cli.StatusError{StatusCode: 404, Status: "Not Found"}
```

### Guard Errors (Unexpected Call Detection)

```go
// Fail if an API method is called unexpectedly
fakeClient{
    containerRemoveFunc: func(id string, opts container.RemoveOptions) error {
        t.Fatalf("unexpected call to ContainerRemove(%s)", id)
        return nil
    },
}
```

### Default Behavior (Nil Function Fields)

```go
// When function field is nil, returns zero values
func (f *fakeClient) ContainerList(ctx context.Context, opts container.ListOptions) ([]container.Summary, error) {
    if f.containerListFunc != nil {
        return f.containerListFunc(ctx, opts)
    }
    return nil, nil  // Safe default: empty list, no error
}
```

## Error Assertion Patterns

### Basic Error Checking

```go
// Error must be nil
assert.NilError(t, err)

// Error must contain substring
assert.ErrorContains(t, err, "permission denied")

// Error must match type
assert.Check(t, is.ErrorContains(err, "not found"))
```

### Structured Error Checking

```go
// Check StatusError type and code
var statusErr cli.StatusError
assert.Assert(t, errors.As(err, &statusErr))
assert.Equal(t, statusErr.StatusCode, 404)
```

### Table-Driven Error Tests

```go
tests := []struct {
    name        string
    args        []string
    setupFunc   func(*fakeClient)
    expectedErr string
}{
    {
        name: "container not found",
        args: []string{"nonexistent"},
        setupFunc: func(fc *fakeClient) {
            fc.containerInspectFunc = func(id string) (container.InspectResponse, error) {
                return container.InspectResponse{}, errdefs.NotFound(fmt.Errorf("not found"))
            }
        },
        expectedErr: "not found",
    },
    {
        name: "permission denied",
        args: []string{"restricted"},
        setupFunc: func(fc *fakeClient) {
            fc.containerStartFunc = func(string, container.StartOptions) error {
                return errors.New("permission denied")
            }
        },
        expectedErr: "permission denied",
    },
}

for _, tc := range tests {
    t.Run(tc.name, func(t *testing.T) {
        fc := &fakeClient{}
        tc.setupFunc(fc)
        cli := test.NewFakeCli(fc)
        cmd := newStartCommand(cli)
        cmd.SetArgs(tc.args)
        err := cmd.Execute()
        assert.ErrorContains(t, err, tc.expectedErr)
    })
}
```

## Resilience Patterns in Tests

### Channel-Based Synchronization with Timeout

```go
func TestRunAttach(t *testing.T) {
    done := make(chan error, 1)

    go func() {
        cmd := newRunCommand(cli)
        cmd.SetArgs([]string{"alpine"})
        done <- cmd.Execute()
    }()

    select {
    case err := <-done:
        assert.NilError(t, err)
    case <-time.After(10 * time.Second):
        t.Fatal("timeout waiting for command to complete")
    }
}
```

### Polling for Async State (E2E)

```go
// Wait for container to reach "running" state
poll.WaitOn(t, containerIsRunning("test-container"),
    poll.WithTimeout(30 * time.Second),
    poll.WithDelay(500 * time.Millisecond),
)

func containerIsRunning(name string) poll.Check {
    return func(t poll.LogT) poll.Result {
        result := icmd.RunCommand("docker", "inspect", "-f", "{{.State.Running}}", name)
        if result.Stdout() == "true\n" {
            return poll.Success()
        }
        return poll.Continue("container %s not yet running", name)
    }
}
```

### Context Cancellation for Cleanup

```go
func TestCommandCancellation(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())

    go func() {
        time.Sleep(100 * time.Millisecond)
        cancel() // Simulate cancellation
    }()

    err := runCommand(ctx, cli, args)
    assert.ErrorContains(t, err, "context canceled")
}
```

### Resource Cleanup Patterns

```go
// t.Cleanup for deferred cleanup
func TestWithTempDir(t *testing.T) {
    dir := t.TempDir() // Automatically cleaned up
    // ... use dir
}

// fs.NewDir for structured temp directories
dir := fs.NewDir(t, "test",
    fs.WithFile("config.json", `{"key": "value"}`),
)
defer dir.Remove()

// E2E cleanup via Docker commands
t.Cleanup(func() {
    icmd.RunCommand("docker", "rm", "-f", containerName)
})
```

## Signal Handling Tests

### Unit Level: Signal Forwarding

```go
func TestSignalForwarding(t *testing.T) {
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGTERM)
    defer signal.Stop(sigCh)

    // Start command that listens for signals
    go func() {
        cmd.Execute()
    }()

    // Send signal to test process
    proc, _ := os.FindProcess(os.Getpid())
    proc.Signal(syscall.SIGTERM)

    // Verify signal was received
    select {
    case sig := <-sigCh:
        assert.Equal(t, sig, syscall.SIGTERM)
    case <-time.After(5 * time.Second):
        t.Fatal("signal not received")
    }
}
```

### E2E Level: TTY Signal Proxy

```go
// e2e/container/proxy_signal_test.go
func TestProxySignal(t *testing.T) {
    skip.If(t, environment.RemoteDaemon())

    // Start container with TTY
    p, tty, _ := pty.Open()
    defer p.Close()

    cmd := exec.Command("docker", "run", "-it", "--name", "signal-test", "alpine", "sh")
    cmd.Stdin = tty
    cmd.Stdout = tty

    cmd.Start()
    t.Cleanup(func() { cmd.Process.Kill() })

    // Send SIGWINCH (window resize)
    cmd.Process.Signal(syscall.SIGWINCH)

    // Verify behavior...
}
```

## Golden File I/O

### Read/Compare Pattern

```go
// Compare output against golden file
golden.Assert(t, cli.OutBuffer().String(), "container-list.golden")

// Read golden file content
expected := golden.Get(t, "expected-output.golden")
```

### Update Workflow

```bash
# Update all golden files in a package
UPDATE_GOLDEN=1 go test ./cli/command/container/...

# Update specific test's golden files
UPDATE_GOLDEN=1 go test ./cli/command/container/... -run TestContainerList
```

### Golden File Location

```
cli/command/container/
├── list.go
├── list_test.go
└── testdata/
    ├── container-list-default.golden
    ├── container-list-json.golden
    ├── container-list-quiet.golden
    └── container-list-with-size.golden
```

## Writer Hook Pattern (Synchronization)

```go
// internal/test/writer.go
func NewWriterWithHook(w io.Writer, hook func([]byte)) io.Writer {
    return &writerWithHook{Writer: w, hook: hook}
}

type writerWithHook struct {
    io.Writer
    hook func([]byte)
}

func (w *writerWithHook) Write(p []byte) (int, error) {
    w.hook(p) // Notify on write
    return w.Writer.Write(p)
}
```

Usage for synchronizing test assertions with async output:

```go
written := make(chan struct{})
cli.SetOut(streams.NewOut(test.NewWriterWithHook(cli.OutBuffer(), func(p []byte) {
    close(written) // Signal when first write occurs
})))

go cmd.Execute()

select {
case <-written:
    // Output has been written, safe to assert
case <-time.After(5 * time.Second):
    t.Fatal("timeout waiting for output")
}
```

## Risk Areas and Considerations

### Platform Specificity
- Signal tests (SIGTERM, SIGWINCH, SIGKILL) are Unix-only
- PTY tests require `creack/pty` (not available on Windows)
- Some golden files may differ across platforms

### Timeout Sensitivity
- Hardcoded timeout values in channel selects
- E2E poll timeouts may need adjustment for slow CI
- No centralized timeout configuration

### Environment Coupling
- E2E tests depend on `TEST_DOCKER_HOST` environment variable
- Registry tests require `registry:5000` to be running
- SSH tests require specific container setup

### Flakiness Risks
- Docker-in-Docker can be slow to start
- Network operations may timeout under load
- Signal delivery timing is non-deterministic

## Key Reference Files

| File | Purpose |
|------|---------|
| `internal/test/cli.go` | FakeCli with buffer-backed streams |
| `internal/test/cmd.go` | TerminatePrompt with writer hooks |
| `internal/test/writer.go` | WriterWithHook for synchronization |
| `internal/test/output/output.go` | Line-level assertion helpers |
| `internal/test/environment/testenv.go` | E2E environment setup |
| `cli/command/container/client_test.go` | fakeClient error injection |
| `cli/command/container/run_test.go` | Async/signal/attach testing |
| `cli/command/volume/prune_test.go` | Stdin simulation, golden files |
| `e2e/container/run_test.go` | E2E polling, timeouts |
| `e2e/container/proxy_signal_test.go` | Signal proxy testing |
