# Testing Guide

## Overview

Clawker uses a multi-tier testing strategy with no build tags — test categories are separated by directory.

| Category | Directory | Docker Required | Purpose |
|----------|-----------|:---:|---------|
| Unit | `*_test.go` (co-located) | No | Pure logic, fakes, mocks |
| CLI | `test/cli/` | Yes | Testscript-based CLI workflow validation |
| Commands | `test/commands/` | Yes | Command integration (create/exec/run/start) |
| Internals | `test/internals/` | Yes | Container scripts/services (firewall, SSH) |
| Whail | `test/whail/` | Yes + BuildKit | BuildKit integration, engine-level builds |
| Agents | `test/agents/` | Yes | Full agent lifecycle, ralph tests |

## Running Tests

```bash
# Unit tests (no Docker) — always run these first
make test

# Individual integration suites (Docker required)
go test ./test/whail/... -v -timeout 5m
go test ./test/cli/... -v -timeout 15m
go test ./test/commands/... -v -timeout 10m
go test ./test/internals/... -v -timeout 10m
go test ./test/agents/... -v -timeout 15m

# All test suites
make test-all
```

### Running Specific CLI Tests

```bash
# All CLI tests
go test ./test/cli/... -v -timeout 15m

# Single category
go test -run ^TestContainer$ ./test/cli/... -v

# Single script
CLAWKER_ACCEPTANCE_SCRIPT=run-basic.txtar go test -run ^TestContainer$ ./test/cli/... -v
```

## Golden File Testing

Some tests compare output against golden files. To update after intentional changes:

```bash
GOLDEN_UPDATE=1 go test ./path/to/package/... -run TestName -v
```

Common golden file tests:

```bash
GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeed -v
GOLDEN_UPDATE=1 go test ./internal/tui/... -run TestProgressPlain_Golden -v
GOLDEN_UPDATE=1 go test ./internal/cmd/image/build/... -run TestBuildProgress_Golden -v
```

## Fawker Demo CLI

Fawker is a demo CLI with faked dependencies and recorded scenarios — no Docker required. Use it for visual UAT:

```bash
make fawker
./bin/fawker image build                            # Default build scenario
./bin/fawker image build --scenario error           # Error scenario
./bin/fawker image build --progress plain           # Plain mode
./bin/fawker container run -it --agent test @       # Interactive run
./bin/fawker container run --detach --agent test @  # Detached run
```

## Writing Tests

### Test Infrastructure

Each package in the dependency DAG provides test utilities so dependents can mock the entire chain:

| Package | Test Utils | Provides |
|---------|------------|----------|
| `internal/docker` | `dockertest/` | `FakeClient`, fixtures, assertions |
| `internal/config` | `configtest/` | `InMemoryRegistryBuilder`, `InMemoryProjectBuilder` |
| `internal/git` | `gittest/` | `InMemoryGitManager` |
| `pkg/whail` | `whailtest/` | `FakeAPIClient` |
| `internal/iostreams` | (built-in) | `NewTestIOStreams()` |

**Rule**: If a dependency node lacks test infrastructure, add it before writing tests that depend on it.

### Command Test Pattern

Commands are tested using the Cobra+Factory pattern with `dockertest.FakeClient`:

```go
func TestMyCommand(t *testing.T) {
    fake := dockertest.NewFakeClient()
    fake.SetupContainerCreate()
    fake.SetupContainerStart()

    f, tio := testFactory(t, fake)
    cmd := NewCmdRun(f, nil)  // nil runF = real run function

    cmd.SetArgs([]string{"--detach", "alpine"})
    cmd.SetIn(&bytes.Buffer{})
    cmd.SetOut(tio.OutBuf)
    cmd.SetErr(tio.ErrBuf)

    err := cmd.Execute()
    require.NoError(t, err)

    fake.AssertCalled(t, "ContainerCreate")
    fake.AssertCalled(t, "ContainerStart")
}
```

### Three Test Tiers for Commands

| Tier | Method | What It Tests |
|------|--------|---------------|
| **1. Flag Parsing** | `runF` trapdoor | Flags map correctly to Options fields |
| **2. Integration** | `nil` runF + fake Docker | Full pipeline (flags + Docker calls + output) |
| **3. Unit** | Direct function call | Domain logic without Cobra or Factory |

### Test Harness (`test/harness/`)

For integration tests with real Docker:

```go
h := harness.NewHarness(t, harness.WithProject("test"),
    harness.WithConfigBuilder(builders.MinimalValidConfig()))

client := harness.NewTestClient(t)
image := harness.BuildLightImage(t, client)
ctr := harness.RunContainer(t, client, image,
    harness.WithCapAdd("NET_ADMIN"),
    harness.WithUser("root"),
)

result, err := ctr.Exec(ctx, client, "echo", "hello")
require.Equal(t, 0, result.ExitCode)
```

### Config Builder Presets

```go
builders.MinimalValidConfig()         // Bare minimum
builders.FullFeaturedConfig()         // All features enabled
builders.DefaultBuild()               // buildpack-deps with git/curl
builders.SecurityFirewallDisabled()   // For tests that don't need firewall
```

## Key Conventions

1. **All tests must pass before any change is complete** — `make test` at minimum
2. **No build tags** — test categories separated by directory
3. **Always use `t.Cleanup()`** for resource cleanup
4. **Use `context.Background()` in cleanup functions** — parent context may be cancelled
5. **Unique agent names** — include timestamp + random suffix for parallel safety
6. **Never import `test/harness` in co-located unit tests** — too heavy (pulls Docker SDK)
7. **Never call `factory.New()` in tests** — construct `&cmdutil.Factory{}` struct literals directly
