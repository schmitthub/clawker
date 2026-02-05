# Testing Architecture - Docker CLI Repository

## Overview

The Docker CLI testing architecture is a mature, three-tier system designed around interface-based dependency injection with zero external mocking libraries.

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                     Test Architecture                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Unit Tests (~150+ files)               │   │
│  │                                                           │   │
│  │  ┌─────────────┐    ┌──────────────┐   ┌─────────────┐  │   │
│  │  │  FakeCli    │    │ fakeClient   │   │  Builders   │  │   │
│  │  │  (1 shared) │    │ (19 per-pkg) │   │ (shared)    │  │   │
│  │  └──────┬──────┘    └──────┬───────┘   └──────┬──────┘  │   │
│  │         │                  │                   │          │   │
│  │         ▼                  ▼                   ▼          │   │
│  │  ┌─────────────────────────────────────────────────────┐ │   │
│  │  │         Command Handler Tests (*_test.go)           │ │   │
│  │  │    cli/command/{container,image,network,...}/        │ │   │
│  │  └─────────────────────────────────────────────────────┘ │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                   E2E Tests (34 files)                    │   │
│  │                                                           │   │
│  │  ┌──────────┐  ┌──────────┐  ┌───────────────────────┐  │   │
│  │  │  docker   │  │ registry │  │  Docker-in-Docker     │  │   │
│  │  │  binary   │  │  :5000   │  │  Engine (DinD)        │  │   │
│  │  └────┬─────┘  └────┬─────┘  └───────────┬───────────┘  │   │
│  │       │              │                     │              │   │
│  │       ▼              ▼                     ▼              │   │
│  │  ┌─────────────────────────────────────────────────────┐ │   │
│  │  │     E2E Test Files (e2e/{container,image,...}/)      │ │   │
│  │  │     Execute docker binary via icmd.RunCommand        │ │   │
│  │  └─────────────────────────────────────────────────────┘ │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                  Shared Infrastructure                     │   │
│  │                                                           │   │
│  │  internal/test/cli.go ........... FakeCli implementation  │   │
│  │  internal/test/builders/ ........ Test data builders      │   │
│  │  internal/test/environment/ ..... Environment detection   │   │
│  │  internal/test/output/ .......... Output comparison       │   │
│  │  testdata/ (per package) ........ Golden files/fixtures   │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                   Build System                            │   │
│  │                                                           │   │
│  │  docker-bake.hcl ─── test, test-coverage, e2e-image      │   │
│  │  Dockerfile ──────── Multi-stage (test, e2e targets)      │   │
│  │  Makefile ────────── Local dev targets                    │   │
│  │  docker.Makefile ─── Docker-based execution               │   │
│  │  scripts/test/e2e/ ─ E2E orchestration scripts            │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

## Tier 1: Unit Tests

### FakeCli Abstraction Layer

The `FakeCli` type in `internal/test/cli.go` implements the full `command.Cli` interface with in-memory I/O buffers:

```go
// FakeCli provides a fully functional CLI mock
type FakeCli struct {
    command.DockerCli
    client     client.APIClient  // The fake Docker client
    outBuffer  *bytes.Buffer     // Captured stdout
    errBuffer  *bytes.Buffer     // Captured stderr
    inBuffer   *bytes.Buffer     // Simulated stdin
    // ... config store, notary, etc.
}
```

Key capabilities:
- Full `command.Cli` interface implementation
- In-memory stdout/stderr/stdin buffers
- Configurable Docker API client (typically a fakeClient)
- Config store for testing credential operations
- Content trust / notary support

### fakeClient Pattern

Each command package that needs Docker API mocking defines its own `fakeClient` struct in `client_test.go`:

```go
// Per-package fakeClient with function-field overrides
type fakeClient struct {
    client.Client  // Embed the interface for default nil implementations

    // Override specific methods as needed
    containerListFunc    func(context.Context, container.ListOptions) ([]container.Summary, error)
    containerInspectFunc func(string) (container.InspectResponse, error)
    containerCreateFunc  func(*container.Config, *container.HostConfig, ...) (container.CreateResponse, error)
    // ...
}

func (f *fakeClient) ContainerList(ctx context.Context, opts container.ListOptions) ([]container.Summary, error) {
    if f.containerListFunc != nil {
        return f.containerListFunc(ctx, opts)
    }
    return nil, nil  // Safe default
}
```

There are **19 per-package fakeClient implementations** across:
- `cli/command/container/` - Container operations
- `cli/command/image/` - Image operations
- `cli/command/network/` - Network operations
- `cli/command/volume/` - Volume operations
- `cli/command/system/` - System operations
- `cli/command/service/` - Swarm service operations
- And more...

### Unit Test Flow

```
Test Function
    │
    ├─ Create fakeClient with needed function overrides
    │     fakeClient{containerListFunc: func(...) { return testData, nil }}
    │
    ├─ Create FakeCli wrapping the fakeClient
    │     cli := test.NewFakeCli(&fakeClient{...})
    │
    ├─ Create command instance
    │     cmd := newListCommand(cli)
    │
    ├─ Set arguments
    │     cmd.SetArgs([]string{"--format", "table"})
    │
    ├─ Execute command
    │     err := cmd.Execute()
    │
    └─ Assert results
          assert.NilError(t, err)
          golden.Assert(t, cli.OutBuffer().String(), "list-output.golden")
```

## Tier 2: E2E Tests

### Environment Architecture

E2E tests run against real Docker infrastructure orchestrated by Docker Compose:

```yaml
# e2e/compose-env.yaml
services:
  registry:
    image: registry:3
    ports: ["5000:5000"]

  engine:
    image: docker:${ENGINE_VERSION}-dind
    privileged: true
    command: --insecure-registry=registry:5000
    environment:
      DOCKER_TLS_CERTDIR: ""
```

### E2E Test Flow

```
Build Phase
    │
    ├─ docker buildx bake e2e-image
    │     Compiles docker binary
    │     Installs gotestsum, buildx, compose
    │     Packages test fixtures
    │
    └─ Produces e2e test container image

Execution Phase
    │
    ├─ scripts/test/e2e/entry
    │     Detects remote vs DinD mode
    │
    ├─ scripts/test/e2e/wrapper
    │     setup → test → cleanup
    │
    └─ scripts/test/e2e/run
          ├─ Setup: docker-compose up, swarm init
          ├─ Test: gotestsum via env -i isolation
          └─ Cleanup: compose down --volumes
```

### E2E Test Pattern

```go
func TestPushWithContentTrust(t *testing.T) {
    skip.If(t, environment.RemoteDaemon())

    // Execute real docker binary
    result := icmd.RunCommand("docker", "push", imageName)
    result.Assert(t, icmd.Expected{
        ExitCode: 0,
        Out:      "Pushed",
    })
}
```

### CI Matrix

```
E2E Matrix (16 combinations):
    ├─ Connection: {local, ssh-connhelper}
    ├─ Base: {alpine, debian}
    └─ Engine: {rc, 29, 28, 25}
```

## Relationship Between Test Tiers

### What Each Tier Validates

| Aspect | Unit Tests | E2E Tests |
|--------|-----------|-----------|
| Flag parsing | ✅ Direct | ✅ Via binary |
| API calls | ✅ Via fakeClient | ✅ Real daemon |
| Output formatting | ✅ Golden files | ✅ Golden files |
| Error handling | ✅ Error injection | ✅ Real errors |
| Multi-command flows | ❌ | ✅ Real workflows |
| Plugin interaction | ❌ | ✅ Real plugins |
| Network operations | ❌ Mocked | ✅ Real network |
| Auth flows | ✅ Mocked | ✅ Real registry |

### Directory Mirroring

E2E tests mirror the source structure:
```
cli/command/container/  →  e2e/container/
cli/command/image/      →  e2e/image/
cli/command/network/    →  e2e/network/
cli/command/volume/     →  e2e/volume/
cli/command/system/     →  e2e/system/
```

## Shared Test Infrastructure

### internal/test/

```
internal/test/
├── cli.go              # FakeCli implementation
├── notary/             # Notary test helpers
├── builders/           # Test data builders
│   ├── container.go    # Container builder
│   ├── network.go      # Network builder
│   ├── volume.go       # Volume builder
│   ├── service.go      # Service builder
│   ├── task.go         # Task builder
│   └── config.go       # Config builder
├── environment/        # Test environment detection
│   └── testenv.go      # Daemon checks, platform detection
└── output/             # Output comparison helpers
    └── output.go       # String matching utilities
```

### Builder Pattern

```go
// Functional options pattern for building test data
container := builders.Container("web",
    builders.WithLabel("app", "nginx"),
    builders.WithPort(80, 8080, builders.TCP),
    builders.WithStatus("running"),
)
```

### Golden File Workflow

```
testdata/
├── container-list-default.golden    # Expected table output
├── container-list-json.golden       # Expected JSON output
├── container-inspect.golden         # Expected inspect output
└── ...

# Update golden files when output changes intentionally:
UPDATE_GOLDEN=1 go test ./cli/command/container/...
```

## Build System Integration

```
docker-bake.hcl
    │
    ├── target "test" {
    │       dockerfile = "Dockerfile"
    │       target = "test"          # Multi-stage target
    │       output = ["type=cacheonly"]
    │   }
    │
    ├── target "test-coverage" {
    │       inherits = ["test"]
    │       target = "test-coverage"
    │       output = ["./build/coverage"]
    │   }
    │
    └── target "e2e-image" {
            dockerfile = "Dockerfile"
            target = "e2e"
            output = ["type=docker"]
        }
```

## Architectural Strengths

1. **Clean separation** - Unit and E2E tiers share only `gotest.tools` framework and `internal/test/environment`
2. **No external mock dependencies** - Function-field pattern eliminates code generation and framework coupling
3. **Reproducible CI matrix** - 16 E2E combinations ensure compatibility across engine versions
4. **Interface-driven** - `command.Cli` interface enables seamless test substitution
5. **Golden files** - Reliable output regression testing with easy update workflow

## Architectural Concerns

1. **Per-package fakeClient boilerplate** - 19 files of similar nil-check dispatch code
2. **No integration tier** - Gap between pure unit tests and full E2E
3. **DinD fragility** - Docker-in-Docker adds complexity and potential flakiness
4. **Platform coverage** - E2E runs only on Linux; Windows testing is commented out in CI

## Key Adaptation Considerations

When adapting this architecture for a new CLI:

1. **Start with FakeCli equivalent** - Interface-based CLI mock with buffer capture
2. **Use function-field fakes** - Per-resource fakeClient with overridable methods
3. **Invest in builders early** - Functional options for complex test data
4. **Golden files from day one** - Snapshot testing for output formatting
5. **Container-based E2E** - Docker Compose for real service dependencies
6. **CI matrix testing** - Multiple backend versions for compatibility assurance
