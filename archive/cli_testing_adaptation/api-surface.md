# Testing API Surface - Docker CLI Repository

## Overview

The Docker CLI testing infrastructure provides a comprehensive internal library for writing unit and end-to-end tests. The API surface is internal-only with no backward compatibility guarantees, optimized for Docker CLI command testing with extensive mocking and assertion capabilities.

## Core Components

### 1. FakeCli API (`internal/test`)

Mock implementation of the `command.Cli` interface.

#### Constructor

```go
func NewFakeCli(client client.APIClient, opts ...func(*FakeCli)) *FakeCli
```

Creates a new FakeCli with:
- Automatic test-friendly defaults (empty buffers, no-op stdin)
- Optional functional options for customization
- Wraps any `client.APIClient` implementation (typically a fakeClient)

#### Buffer Accessors

```go
func (c *FakeCli) OutBuffer() *bytes.Buffer  // Captured stdout
func (c *FakeCli) ErrBuffer() *bytes.Buffer  // Captured stderr
```

#### Stream Setters

```go
func (c *FakeCli) SetIn(in *streams.In)     // Set stdin stream
func (c *FakeCli) SetOut(out *streams.Out)   // Set stdout stream
func (c *FakeCli) SetErr(err io.Writer)      // Set stderr writer
```

#### Config/Context Management

```go
func (c *FakeCli) ConfigFile() *configfile.ConfigFile
func (c *FakeCli) CurrentContext() string
func (c *FakeCli) DockerEndpoint() docker.Endpoint
func (c *FakeCli) ServerInfo() command.ServerInfo
func (c *FakeCli) ContentTrustEnabled() bool
func (c *FakeCli) NotaryClient(imgRefAndAuth trust.ImageRefAndAuth, actions []string) (notaryclient.Repository, error)
```

#### Functional Options

```go
// Set content trust notary client
func EnableContentTrust(c *FakeCli)

// Set custom config file
func WithConfigFile(cfg *configfile.ConfigFile) func(*FakeCli)

// Set custom server info
func WithServerInfo(info command.ServerInfo) func(*FakeCli)
```

#### Typical Usage

```go
fakeCLI := test.NewFakeCli(&fakeClient{
    createContainerFunc: func(config *container.Config, hostConfig *container.HostConfig, ...) (container.CreateResponse, error) {
        return container.CreateResponse{ID: "abc123"}, nil
    },
})

cmd := newRunCommand(fakeCLI)
cmd.SetArgs([]string{"--detach", "busybox"})
assert.NilError(t, cmd.Execute())
assert.Check(t, is.Contains(fakeCLI.OutBuffer().String(), "abc123"))
```

### 2. fakeClient Pattern

Function-field-based API stubbing defined per-package in `client_test.go` files.

#### Structure

```go
type fakeClient struct {
    client.Client  // Embed interface for default implementations

    // Container operations
    containerListFunc      func(context.Context, container.ListOptions) ([]container.Summary, error)
    containerCreateFunc    func(*container.Config, *container.HostConfig, *network.NetworkingConfig, *specs.Platform, string) (container.CreateResponse, error)
    containerStartFunc     func(string, container.StartOptions) error
    containerStopFunc      func(string, container.StopOptions) error
    containerRemoveFunc    func(string, container.RemoveOptions) error
    containerInspectFunc   func(string) (container.InspectResponse, error)
    containerExecCreateFunc func(string, container.ExecOptions) (container.ExecCreateResponse, error)
    containerAttachFunc    func(context.Context, string, container.AttachOptions) (types.HijackedResponse, error)
    containerLogsFunc      func(string, container.LogsOptions) (io.ReadCloser, error)
    containerWaitFunc      func(context.Context, string, container.WaitCondition) (<-chan container.WaitResponse, <-chan error)

    // Image operations
    imageListFunc    func(context.Context, image.ListOptions) ([]image.Summary, error)
    imagePullFunc    func(context.Context, string, image.PullOptions) (io.ReadCloser, error)
    imagePushFunc    func(context.Context, string, image.PushOptions) (io.ReadCloser, error)
    imageInspectFunc func(string) (image.InspectResponse, error)
    imageRemoveFunc  func(string, image.RemoveOptions) ([]image.DeleteResponse, error)
    imageBuildFunc   func(context.Context, io.Reader, types.ImageBuildOptions) (types.ImageBuildResponse, error)

    // Network operations
    networkListFunc    func(context.Context, network.ListOptions) ([]network.Summary, error)
    networkCreateFunc  func(context.Context, string, network.CreateOptions) (network.CreateResponse, error)
    networkInspectFunc func(context.Context, string, network.InspectOptions) (network.Inspect, error)
    networkRemoveFunc  func(context.Context, string) error

    // Volume operations
    volumeListFunc   func(context.Context, volume.ListOptions) (volume.ListResponse, error)
    volumeCreateFunc func(context.Context, volume.CreateOptions) (volume.Volume, error)
    volumeRemoveFunc func(context.Context, string, bool) error

    // System operations
    infoFunc       func(context.Context) (system.Info, error)
    serverVersion  func(context.Context) (types.Version, error)
    diskUsageFunc  func(context.Context, types.DiskUsageOptions) (types.DiskUsage, error)
}
```

#### Method Implementation Pattern

```go
func (f *fakeClient) ContainerList(ctx context.Context, opts container.ListOptions) ([]container.Summary, error) {
    if f.containerListFunc != nil {
        return f.containerListFunc(ctx, opts)
    }
    return nil, nil  // Safe default when not stubbed
}
```

#### Key Properties
- Tests assign only the functions they need
- Unstubbed methods return zero values / nil errors
- Enables partial mocking with input verification
- No code generation required

### 3. Test Builders (`internal/test/builders`)

Fluent API for constructing test data using functional options pattern.

#### Available Builders

| Function | Returns | Options Prefix |
|----------|---------|---------------|
| `Container(id, ...ContainerOpt)` | `*types.Container` | `With*` |
| `Service(opts ...ServiceOpt)` | `*swarm.Service` | `Service*`, `With*` |
| `Volume(opts ...VolumeOpt)` | `*volume.Volume` | `Volume*` |
| `NetworkResource(opts ...NetworkResourceOpt)` | `*network.Summary` | `NetworkResource*` |
| `Node(opts ...NodeOpt)` | `*swarm.Node` | `Node*` |
| `Task(opts ...TaskOpt)` | `swarm.Task` | `Task*`, `With*` |
| `Secret(opts ...SecretOpt)` | `*swarm.Secret` | `Secret*` |
| `Config(opts ...ConfigOpt)` | `*swarm.Config` | `Config*` |
| `Swarm(opts ...SwarmOpt)` | `*swarm.Swarm` | `Swarm*` |

#### Container Builder Options

```go
// Identity
WithID(id string) ContainerOpt
WithName(name string) ContainerOpt
WithNames(names ...string) ContainerOpt

// Status
WithStatus(status string) ContainerOpt
WithState(state string) ContainerOpt

// Metadata
WithLabel(key, value string) ContainerOpt
WithSize(size int64) ContainerOpt
WithImage(image string) ContainerOpt

// Networking
WithPort(private, public uint16, builders.TCP|builders.UDP) ContainerOpt

// Mounts
WithMount(mount types.MountPoint) ContainerOpt
```

#### Service Builder Options

```go
ReplicatedService(replicas uint64) ServiceOpt
GlobalService() ServiceOpt
ServiceImage(image string) ServiceOpt
ServicePort(opts ...PortOpt) ServiceOpt
ServiceLabels(labels map[string]string) ServiceOpt
ServiceName(name string) ServiceOpt
ServiceID(id string) ServiceOpt
```

#### Usage Examples

```go
// Simple container
c := builders.Container("web")

// Complex container
c := builders.Container("web",
    builders.WithLabel("app", "nginx"),
    builders.WithPort(80, 8080, builders.TCP),
    builders.WithStatus("running"),
    builders.WithSize(1024),
)

// Replicated service
svc := builders.Service(
    builders.ReplicatedService(3),
    builders.ServiceImage("nginx:latest"),
    builders.ServiceName("web"),
    builders.ServicePort(
        builders.PortConfig(80, 8080, builders.PortTCP),
    ),
)
```

### 4. Test Helpers (`internal/test`)

#### ID Generation

```go
func RandomID() string  // Generates random hex ID (64 chars)
```

#### Value Comparison

```go
func CompareMultipleValues(t *testing.T, actual, expected string)
// Order-agnostic comparison of key=value pairs
```

#### Prompt Testing

```go
func TerminatePrompt(ctx context.Context, t *testing.T, cli *FakeCli, cmd *cobra.Command, args []string)
// Executes command and terminates interactive prompts
```

#### Write Monitoring

```go
func NewWriterWithHook(w io.Writer, hook func([]byte)) io.Writer
// Wraps a writer to call hook on each write (for synchronization)
```

### 5. Environment API (`internal/test/environment`)

#### Setup

```go
func Setup() *Execution
// Configures test environment from environment variables:
//   TEST_DOCKER_HOST - Docker daemon address
//   TEST_DOCKER_CERT_PATH - TLS certificate path
//   DOCKER_API_VERSION - API version override
```

#### Execution Type

```go
type Execution struct {
    // Methods for daemon capability detection
}

func (e *Execution) DaemonAPIVersion() string
func (e *Execution) IsExperimentalDaemon() bool
func (e *Execution) DaemonIsLinux() bool
func (e *Execution) RemoteDaemon() bool
```

#### Skip Helpers

```go
func SkipIfDaemonNotLinux(t testing.TB)
func SkipIfNotExperimentalDaemon(t testing.TB)
func SkipIfNotPlatform(t testing.TB, platform string)
func SkipIfCgroupNamespacesNotSupported(t testing.TB)
```

### 6. Output Assertion (`internal/test/output`)

#### Line-by-Line Comparison

```go
func Assert(t testing.TB, actual string, checks map[int]func(string) error)
```

#### Comparison Functions

```go
func Prefix(expected string) func(string) error   // Line starts with
func Suffix(expected string) func(string) error   // Line ends with
func Contains(expected string) func(string) error // Line contains
func Equals(expected string) func(string) error   // Exact match
```

#### Usage

```go
output.Assert(t, cli.OutBuffer().String(), map[int]func(string) error{
    0: output.Prefix("CONTAINER ID"),
    1: output.Contains("web"),
    2: output.Equals(""),
})
```

### 7. gotest.tools/v3 Integration

#### assert Package

```go
assert.NilError(t, err)                        // err must be nil
assert.ErrorContains(t, err, "message")         // err contains substring
assert.Equal(t, actual, expected)               // ==
assert.Assert(t, condition)                     // truthy
assert.Check(t, comparison)                     // comparison (continues on failure)
assert.DeepEqual(t, actual, expected, opts...)  // deep equality via go-cmp
```

#### is Package (Comparisons)

```go
is.Equal(actual, expected)          // ==
is.DeepEqual(actual, expected)      // deep equality
is.Len(collection, n)              // length check
is.Contains(haystack, needle)       // string/slice contains
is.ErrorContains(err, msg)         // error message contains
is.Nil(value)                      // nil check
```

#### golden Package

```go
golden.Assert(t, actual, "filename.golden")  // Compare against golden file
golden.String(actual, "filename.golden")     // Returns comparison result
golden.Get(t, "filename.golden")             // Read golden file content
// Update: run tests with UPDATE_GOLDEN=1
```

#### icmd Package (E2E)

```go
result := icmd.RunCommand("docker", "run", "--rm", "alpine", "echo", "hello")
result.Assert(t, icmd.Expected{
    ExitCode: 0,
    Out:      "hello",
    Err:      "",
})

// With environment
result := icmd.RunCmd(icmd.Command("docker", "info"),
    icmd.WithEnv("DOCKER_HOST=tcp://localhost:2375"),
)
```

#### fs Package (Filesystem)

```go
dir := fs.NewDir(t, "test-dir",
    fs.WithFile("config.json", `{"key": "value"}`),
    fs.WithDir("subdir",
        fs.WithFile("data.txt", "content"),
    ),
    fs.WithMode(0o755),
)
defer dir.Remove()
// dir.Path() returns the temporary directory path
```

#### poll Package (Polling)

```go
poll.WaitOn(t, checkCondition,
    poll.WithTimeout(30*time.Second),
    poll.WithDelay(500*time.Millisecond),
)

func checkCondition(t poll.LogT) poll.Result {
    if ready {
        return poll.Success()
    }
    return poll.Continue("waiting for condition")
}
```

#### skip Package

```go
skip.If(t, environment.RemoteDaemon())
skip.If(t, !environment.DaemonIsLinux())
skip.If(t, os.Getenv("SKIP_FEATURE") != "")
```

#### env Package

```go
env.Patch(t, "DOCKER_HOST", "tcp://localhost:2375")
// Automatically restored when test completes
```

## API Characteristics Summary

| Characteristic | Approach |
|---------------|----------|
| Mocking | Function-field structs (no external libraries) |
| Assertions | gotest.tools/v3/assert exclusively |
| Test data | Fluent builders with functional options |
| Output capture | bytes.Buffer via FakeCli |
| Golden files | gotest.tools/v3/golden with UPDATE_GOLDEN=1 |
| E2E execution | gotest.tools/v3/icmd subprocess runner |
| Polling | gotest.tools/v3/poll for async conditions |
| Skip conditions | gotest.tools/v3/skip + custom environment checks |
| Cleanup | t.Cleanup() and defer patterns |
