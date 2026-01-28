# Infrastructure Requirements: Docker CLI Container Options Pattern

> Generated infrastructure analysis for Docker CLI's shared container options pattern, including build system, dependency management, testing, and linting infrastructure.

## Infrastructure Summary

```
Build System: docker buildx bake with Dockerfile multi-stage builds
Language Runtime: Go 1.25.6 (CalVer-based, uses vendor.mod instead of go.mod)
Containerized: Yes - Development and build via Docker with BuildKit
Cloud Services: None (local build system)
External Dependencies: 65+ direct dependencies (see vendor.mod)
Testing Framework: gotest.tools/v3 with gotestsum
Linting: golangci-lint v2.6.1
Code Formatting: gofumpt v0.7.0 preferred, gofmt fallback
Platform Targets: 11 platforms (darwin/amd64, darwin/arm64, linux/*, windows/*)
```

---

## Build System

### Primary Build Tool: docker buildx bake

Docker CLI uses **docker buildx bake** as the primary build system, configured via HCL language in `docker-bake.hcl`.

**Build System Requirements:**
- Docker 20.10+ with BuildKit enabled
- docker-buildx plugin v0.29.1+
- Multi-platform build support

**Key Build Targets:**

```hcl
# Build local binary
docker buildx bake

# Build for specific platform
docker buildx bake --set binary.platform=linux/arm64

# Build for all platforms (11 architectures)
docker buildx bake cross

# Build with dynamic linking (glibc)
USE_GLIBC=1 docker buildx bake dynbinary

# Build example CLI plugins
docker buildx bake plugins

# Run unit tests in container
docker buildx bake test

# Run linting checks
docker buildx bake lint

# Validate vendor directory
docker buildx bake validate-vendor
```

### Build Configuration: docker-bake.hcl

**File Location:** `/docker-bake.hcl`

**Key Variables:**
```hcl
GO_VERSION = "1.25.6"           # Go language version
VERSION = ""                     # Semantic version (set via env)
USE_GLIBC = ""                   # Empty=Alpine, "1"=Debian/glibc
STRIP_TARGET = ""                # Strip debugging symbols
IMAGE_NAME = "docker-cli"        # Base Docker image name
PACKAGER_NAME = ""               # Windows binary package name
```

**Platform Targets (11 total):**
```hcl
platforms = [
    "darwin/amd64",      # macOS Intel
    "darwin/arm64",      # macOS Apple Silicon
    "linux/amd64",       # Linux x86-64
    "linux/arm/v6",      # Linux ARMv6 (Raspberry Pi)
    "linux/arm/v7",      # Linux ARMv7 (32-bit ARM)
    "linux/arm64",       # Linux ARM64
    "linux/ppc64le",     # Linux PowerPC
    "linux/riscv64",     # Linux RISC-V
    "linux/s390x",       # Linux IBM s390x
    "windows/amd64",     # Windows x86-64
    "windows/arm64"      # Windows ARM64
]
```

**Build Targets:**
- `binary` - Default, builds for local platform
- `dynbinary` - Dynamically linked binary with glibc
- `plugins` - Example CLI plugins
- `cross` - All 11 platforms
- `lint` - Code linting
- `shellcheck` - Shell script validation
- `validate-vendor` - Verify vendor directory
- `test` - Unit tests with coverage

### Dockerfile: Multi-Stage Build Pipeline

**File Location:** `/Dockerfile`

**Build Stages:**

1. **Base Stages** (Platform-specific):
   ```dockerfile
   build-base-alpine    # Alpine-based build environment
   build-base-debian    # Debian-based build environment
   build-alpine         # Alpine with build tools (musl-dev, gcc)
   build-debian         # Debian with build tools (libc6-dev, gcc)
   ```

2. **Tools Installation Stages** (Cached for reuse):
   ```dockerfile
   goversioninfo        # Windows version resource tool (v1.5.0)
   gotestsum            # Test runner with custom output (v1.13.0)
   xx                   # Cross-compilation utilities (v1.7.0)
   buildx, compose      # Docker plugins for e2e tests
   ```

3. **Build Stage** (Primary compilation):
   ```dockerfile
   FROM build-${BASE_VARIANT} AS build

   ARG GO_LINKMODE=static      # static or dynamic linking
   ARG GO_BUILDTAGS            # Additional build tags
   ARG GO_STRIP                # Strip symbols if set
   ARG CGO_ENABLED             # Enable/disable CGO
   ARG VERSION                 # Version string
   ARG PACKAGER_NAME           # Windows package name

   # Cross-compile via xx-go wrapper
   # Run: ./scripts/build/binary
   # Output: /out/docker binary
   ```

4. **Test Stage**:
   ```dockerfile
   FROM build-${BASE_VARIANT} AS test

   # Install gotestsum
   # Run: gotestsum -- -coverprofile=/tmp/coverage.txt
   # Excludes: /vendor/, /e2e/, /cmd/docker-trust
   ```

5. **Output Stages**:
   ```dockerfile
   binary          # Final binary artifact
   plugins         # CLI plugins
   test-coverage   # Coverage report (coverage.txt)
   e2e             # E2E test environment with dependencies
   dev             # Development shell
   ```

**Base Image Selection:**
- **Alpine** (default): `golang:1.25.6-alpine3.22`
  - Smaller image size
  - Static linking (musl-libc)
  - Faster builds

- **Debian** (with USE_GLIBC=1): `golang:1.25.6-bookworm`
  - glibc-based (system libc compatibility)
  - Dynamic linking enabled
  - Larger image size

**Build Arguments and Defaults:**

| Argument | Default | Purpose |
|----------|---------|---------|
| GO_VERSION | 1.25.6 | Go language version |
| ALPINE_VERSION | 3.22 | Alpine base image version |
| BASE_DEBIAN_DISTRO | bookworm | Debian release |
| XX_VERSION | 1.7.0 | Cross-compilation toolkit |
| GOVERSIONINFO_VERSION | v1.5.0 | Windows version tool |
| GOTESTSUM_VERSION | v1.13.0 | Test runner version |
| BUILDX_VERSION | 0.29.1 | Docker buildx plugin |
| COMPOSE_VERSION | v2.40.0 | Docker compose plugin |
| BASE_VARIANT | alpine | Base image variant |
| GO_LINKMODE | static | Linking mode |
| CGO_ENABLED | (varies) | C interop enabled |

### Build Scripts

**Script Location:** `/scripts/build/`

**binary** - Main compilation script:
```bash
#!/usr/bin/env sh
set -eu
. ./scripts/build/.variables

# Go environment setup
export GO111MODULE=auto

# Windows: Generate version information via goversioninfo
if [ "$(go env GOOS)" = "windows" ]; then
    ./scripts/build/mkversioninfo
    go generate -v "${SOURCE}"
fi

# Compile binary with linkage and tags
go build -o "${TARGET}" \
    -tags "${GO_BUILDTAGS}" \
    -ldflags "${GO_LDFLAGS}" \
    ${GO_BUILDMODE} \
    "${SOURCE}"

# Create symlink to 'docker' without platform suffix
ln -sf "$(basename "${TARGET}")" "$(dirname "${TARGET}")/docker"
```

**Build Variables** (set in `.variables`):
- `SOURCE` - Entry point (`cmd/docker/docker.go`)
- `TARGET` - Output path (`build/docker` or `build/docker.exe`)
- `GO_BUILDTAGS` - Feature flags
- `GO_LDFLAGS` - Link-time flags (version, commit, etc.)
- `GO_BUILDMODE` - Mode flags (CGO, PIE, etc.)

---

## Dependency Management

### vendor.mod: CalVer-Based Module System

**Why vendor.mod Instead of go.mod:**

Docker CLI uses CalVer (Calendar Versioning) for releases, not SemVer. Using `go.mod` would require SemVer semantics, which is incompatible.

**File Structure:**
```
vendor.mod      # Module declaration and requirements (like go.mod)
vendor.sum      # Checksums for dependencies (like go.sum)
vendor/         # All dependencies vendored locally
scripts/        # Helper scripts for vendor management
  with-go-mod.sh    # Creates temporary go.mod for tooling
  vendor/update     # Updates vendor directory
  vendor/validate   # Validates vendor directory
  vendor/outdated   # Checks for outdated dependencies
```

**Module Declaration:**
```
module github.com/docker/cli
go 1.24.0

require (
    dario.cat/mergo v1.0.2
    github.com/containerd/errdefs v1.0.0
    # ... 63 more direct dependencies
)

require (
    # ... 37 indirect dependencies
)
```

### Key Direct Dependencies

**CLI Framework & Flags:**
- `github.com/spf13/cobra` v1.10.2 - Command framework
- `github.com/spf13/pflag` v1.0.10 - Flag parsing

**Docker API Client:**
- `github.com/moby/moby/client` v0.2.2 - Docker API client
- `github.com/moby/moby/api` v1.53.0 - API types

**Configuration & Data:**
- `go.yaml.in/yaml/v3` v3.0.4 - YAML parsing
- `github.com/go-viper/mapstructure/v2` v2.5.0 - Struct mapping
- `github.com/distribution/reference` v0.6.0 - Container references

**Container & System Types:**
- `github.com/containerd/platforms` v1.0.0-rc.2 - Platform specs
- `github.com/moby/sys/signal` v0.7.1 - Signal handling
- `github.com/moby/sys/capability` v0.4.0 - Linux capabilities
- `github.com/opencontainers/image-spec` v1.1.1 - OCI image spec

**Logging & Monitoring:**
- `github.com/sirupsen/logrus` v1.9.4 - Logging
- `go.opentelemetry.io/*` - Distributed tracing (multiple v1.38.0)

**Testing & Utilities:**
- `gotest.tools/v3` v3.5.2 - Testing utilities
- `github.com/google/go-cmp` v0.7.0 - Deep comparison
- `github.com/google/shlex` v0.0.0-20191202100458-e7afc7fbc510 - Shell parsing

### Dependency Management Commands

**Local (via Makefile):**
```bash
# Update vendor directory
make vendor
# Runs: scripts/with-go-mod.sh scripts/vendor update

# Validate vendor
make validate-vendor
# Runs: scripts/with-go-mod.sh scripts/vendor validate

# Check outdated dependencies
make mod-outdated
# Runs: scripts/with-go-mod.sh scripts/vendor outdated
```

**Docker Build Commands:**
```bash
# Update vendor via Docker
docker buildx bake update-vendor

# Validate vendor via Docker
docker buildx bake validate-vendor

# Check outdated deps via Docker
docker buildx bake mod-outdated
```

**Key Script: with-go-mod.sh**

This helper script creates a temporary `go.mod` from `vendor.mod` for tools that require it:

```bash
#!/bin/bash
# Temporarily symlink vendor.mod as go.mod
rm -f go.mod go.sum
ln -s vendor.mod go.mod
ln -s vendor.sum go.sum

# Run the command
"$@"

# Cleanup
rm -f go.mod go.sum
```

Why needed: Some tools (gotestsum, golangci-lint, go-mod-outdated) expect `go.mod` even though it's not committed.

---

## Development Environment

### Local Development

**Prerequisites:**

| Tool | Version | Purpose | Installation |
|------|---------|---------|--------------|
| Go | 1.25.6 | Language runtime | Installed locally or via `make dev` |
| docker | 20.10+ | Container runtime | Docker Desktop or Docker Engine |
| docker-buildx | v0.29.1+ | BuildKit wrapper | `docker buildx` command |
| gotestsum | v1.13.0 | Test runner with formatting | `go install gotest.tools/gotestsum@v1.13.0` |
| golangci-lint | v2.6.1 | Multi-linter | `go install github.com/golangci/golangci-lint/cmd/golangci-lint@v2.6.1` |
| gofumpt | v0.7.0 | Code formatter (preferred) | `go install mvdan.cc/gofumpt@v0.7.0` |
| gofmt | 1.25.6 | Code formatter (fallback) | Built-in to Go |
| shellcheck | (latest) | Shell script linter | `apt-get install shellcheck` or `brew install shellcheck` |

### Container-Based Development

**Recommended Approach**: Use Docker for consistent environment

```bash
# Start interactive development container
make -f docker.Makefile dev
# or
make -f docker.Makefile shell

# This:
# 1. Builds docker-cli-dev image from dockerfiles/Dockerfile.dev
# 2. Runs container with source mounted at /go/src/github.com/docker/cli
# 3. Provides docker.sock for container operations
# 4. Sets up build cache volume: docker-cli-dev-cache
```

**Environment Setup:**

The development container includes:
- Go 1.25.6 with BuildKit enabled
- BuildKit v0.29.1
- gofumpt v0.7.0
- gotestsum v1.13.0
- goversioninfo v1.5.0 (Windows version tool)
- git, bash, build-base
- Docker socket mounting for local daemon access

**Volume Management:**

| Mount | Purpose |
|-------|---------|
| `-v $(CURDIR):/go/src/github.com/docker/cli` | Source code |
| `-v docker-cli-dev-cache:/root/.cache/go-build` | Go build cache (persists) |
| `--mount type=bind,src=/var/run/docker.sock` | Docker daemon access |

### Local Development Commands

```bash
# Run tests locally (requires gotestsum)
gotestsum -- ./...

# Run single test
gotestsum -- -run TestRunValidateFlags ./cli/command/container

# Run with coverage
docker buildx bake test-coverage

# Format code (prefers gofumpt, falls back to gofmt)
make fmt

# Lint code
golangci-lint run
# Or via Docker:
docker buildx bake lint

# Shell validation
make shellcheck

# Update vendor
make vendor

# Validate vendor
make validate-vendor

# Clean build artifacts
make clean
```

---

## Testing Infrastructure

### Test Framework: gotest.tools/v3

**Testing Stack:**
- **Test Runner**: gotestsum (v1.13.0)
- **Assertions**: gotest.tools/v3/assert
- **Comparison**: gotest.tools/v3/assert/cmp (aliased as `is` in tests)
- **Coverage Tool**: `-coverprofile` flag

### Test Patterns in Container Options

**File: `/cli/command/container/opts_test.go`** (41,569 bytes)

Test cases cover:
- Flag parsing and validation
- Volume mounting options
- Environment variable handling
- Port exposure
- Network configuration
- Resource limits (memory, CPU, devices)
- Health check configuration
- Logging options
- Security options (capabilities, seccomp)

**File: `/cli/command/container/client_test.go`** (10,367 bytes)

Mock client pattern:
```go
type fakeClient struct {
    client.Client
    // Mock implementation functions
    inspectFunc func(string) (client.ContainerInspectResult, error)
    createFunc  func(opts client.ContainerCreateOptions) (client.ContainerCreateResult, error)
    // ... more mock functions
}

// Test setup
func TestSomething(t *testing.T) {
    fakeCLI := test.NewFakeCli(&fakeClient{
        createFunc: func(opts client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
            return client.ContainerCreateResult{ID: "id"}, nil
        },
    })

    cmd := newRunCommand(fakeCLI)
    cmd.SetArgs([]string{"--detach=true", "busybox"})
    assert.NilError(t, cmd.Execute())
}
```

### Running Tests

**Via Docker (Recommended):**
```bash
# All tests with coverage
docker buildx bake test-coverage

# Quick unit tests
docker buildx bake test
```

**Locally (Requires Tools):**
```bash
# All tests
gotestsum -- ./...

# Single package
gotestsum -- ./cli/command/container

# Single test
gotestsum -- -run TestValidateAttach ./cli/command/container

# With coverage
gotestsum -- -coverprofile=coverage.txt ./...

# Custom output format
GOTESTSUM_FORMAT=short-verbose gotestsum -- ./...
```

**Output Formats:**
- `dots` - Single dots for progress
- `short` - Minimal output
- `standard-quiet` - Few details
- `short-verbose` - Detailed per-test
- `standard-verbose` - Full output

### Coverage Configuration

**Coverage Report Generation:**

```bash
# In Dockerfile test stage
gotestsum -- -coverprofile=/tmp/coverage.txt $(go list ./... | grep -vE '/vendor/|/e2e/|/cmd/docker-trust')

# Output: build/coverage/coverage.txt

# Local execution
mkdir -p build/coverage
gotestsum -- -coverprofile=build/coverage/coverage.txt ./...
```

**Excluded from Coverage:**
- `/vendor/` - Third-party dependencies
- `/e2e/` - End-to-end tests (separate suite)
- `/cmd/docker-trust` - Legacy trust command

### Test Assertions Pattern

```go
import (
    "gotest.tools/v3/assert"
    is "gotest.tools/v3/assert/cmp"
)

func TestExample(t *testing.T) {
    // Simple assertions
    assert.NilError(t, err)
    assert.Equal(t, actual, expected)
    assert.Assert(t, condition)

    // Comparison assertions
    assert.Check(t, is.Equal(actual, expected))
    assert.Check(t, is.DeepEqual(actual, expected))
    assert.Check(t, is.Error(err, expectedMsg))
}
```

---

## Linting & Code Quality

### golangci-lint Configuration

**File: `/.golangci.yml`**

**Go Version Target:**
```yaml
run:
  go: "1.25.6"
  timeout: 5m
```

**Issue Handling:**
```yaml
issues:
  max-issues-per-linter: 0    # Report all issues
  max-same-issues: 0          # All duplicates
```

**Format Checkers:**
```yaml
formatters:
  enable:
    - gofumpt       # Enforces gofumpt formatting
    - goimports     # Enforces import order
  exclusions:
    generated: strict  # Skip generated code
```

**Enabled Linters:**
```yaml
linters:
  enable:
    # Code quality
    - asasalint      # Detect []any in variadic functions
    - bodyclose      # Close HTTP bodies
    - copyloopvar    # Loop variable copies
    - depguard       # Dependency restrictions
    - dogsled        # Too many blank identifiers
    - dupword        # Duplicate words
    - errcheck       # Unchecked errors
    - errchkjson     # JSON marshal errors
    - exhaustive     # Missing enum cases
    - exptostd       # Use stdlib over exp

    # Security
    - gosec          # Security issues
    - forbidigo      # Forbidden patterns

    # Performance
    - gocritic       # Code critique
    - gocyclo        # Cyclomatic complexity
    - ineffassign    # Ineffective assignments

    # Correctness
    - govet          # Go vet checks
    - iface          # Interface issues
    - makezero       # Slice with non-zero length
```

### Code Formatting

**Primary Tool: gofumpt**

```bash
# Format with gofumpt (preferred)
gofumpt -w -d -lang=1.24 .

# Via make
make fmt
```

**Fallback: gofmt**

```bash
# If gofumpt not available
gofmt -w -s -d .
```

**Format Features:**
- Removes unnecessary blank lines
- Aligns parentheses
- Sorts imports (with goimports)
- Enforces spacing rules

### Code Style Requirements

**Maximum Complexity:**
- Cyclomatic complexity: max 16

**Line Length:**
- Maximum: 200 characters (evaluated via golangci-lint)

**Import Organization:**
- Standard library
- Third-party imports
- Local imports

### Shell Script Validation

```bash
# Run shellcheck on scripts
make shellcheck

# This checks:
# - scripts/
# - contrib/completion/bash

# Excluded from checking:
# - scripts/winresources
# - *.ps1 (PowerShell scripts)
```

---

## Build Artifacts and Output

### Binary Output

**Default Output Location:** `/build/`

**Artifacts:**

| Artifact | Platform | Linking | Size |
|----------|----------|---------|------|
| `docker` | Native | Static (Alpine) | ~50-70MB |
| `docker.exe` | Windows | Static | ~60-80MB |
| `docker` (glibc) | Native | Dynamic | ~30-40MB |

**Cross-Platform Builds:**

```bash
# All platforms in separate build/ directories
build/
├── docker                    # Native binary
├── docker-linux-amd64        # Linux AMD64
├── docker-linux-arm64        # Linux ARM64
├── docker-darwin-amd64       # macOS Intel
├── docker-darwin-arm64       # macOS Apple Silicon
├── docker-windows-amd64.exe  # Windows AMD64
├── docker-windows-arm64.exe  # Windows ARM64
└── ... (other platforms)
```

### Version Information

**Version Source:** `/VERSION` file
```
29.0.0-dev
```

**Build-Time Variables (via ldflags):**

```go
-ldflags "
    -X github.com/docker/cli/internal/version.Version=29.0.0
    -X github.com/docker/cli/internal/version.BuildTime=2025-01-27T21:00:00Z
    -X github.com/docker/cli/internal/version.GitCommit=abc1234
"
```

**Windows Specific:**
- Version resource embedded via goversioninfo
- Company/Product name set via PACKAGER_NAME

### Plugin Artifacts

**Plugin Builds:** `/build/` with naming convention `docker-<plugin-name>`

```bash
# Build plugins
docker buildx bake plugins

# Example plugins built:
# build/docker-example
# build/docker-example-command
```

---

## Replicating the Pattern: Minimal Requirements

To replicate the container options pattern in a new Go project, you need:

### 1. Build System Minimum Viable Setup

```hcl
# docker-bake.hcl
variable "GO_VERSION" {
  default = "1.25.6"
}

target "binary" {
  target = "binary"
  platforms = ["local"]
  output = ["build"]
}

target "test" {
  target = "test"
  output = ["type=cacheonly"]
}
```

```dockerfile
# Dockerfile
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
RUN --mount=type=bind,target=.,ro \
    --mount=type=cache,target=/root/.cache \
    go build -o /out/myapp ./cmd/myapp

FROM golang:${GO_VERSION}-alpine AS test
RUN --mount=type=bind,target=.,rw \
    --mount=type=cache,target=/root/.cache \
    go test ./...

FROM scratch AS binary
COPY --from=build /out/myapp /myapp
```

### 2. Dependency Management

```bash
# Initialize module (use go.mod, not vendor.mod unless CalVer)
go mod init github.com/yourorg/yourproject

# Vendor dependencies
go mod vendor

# Makefile target for vendor management
.PHONY: vendor
vendor:
	go mod vendor

.PHONY: test
test:
	gotestsum -- ./...
```

### 3. Linting Configuration

```yaml
# .golangci.yml - Minimal viable
run:
  go: "1.25"
  timeout: 5m

linters:
  enable:
    - gofumpt
    - govet
    - errcheck
    - gosec

formatters:
  enable:
    - gofumpt
    - goimports
```

### 4. Test Infrastructure

```go
// Minimal test setup
import (
    "testing"
    "gotest.tools/v3/assert"
    is "gotest.tools/v3/assert/cmp"
)

func TestExample(t *testing.T) {
    assert.Check(t, is.Equal(1+1, 2))
}
```

### 5. Container Options Implementation Checklist

```go
// 1. Define intermediate options struct
type containerOptions struct {
    // fields for each flag
}

// 2. Implement custom option types
type ListOpts struct {
    values    *[]string
    validator func(string) (string, error)
}

// 3. Create addFlags function
func addFlags(flags *pflag.FlagSet) *containerOptions {
    copts := &containerOptions{}
    // Register all flags
    return copts
}

// 4. Create parse function
func parse(flags *pflag.FlagSet, copts *containerOptions) (*apiConfig, error) {
    // Validation and conversion
    return &apiConfig{}, nil
}

// 5. Use in commands
func newRunCommand(cli *DockerCli) *cobra.Command {
    copts := addFlags(flags)
    config, err := parse(flags, copts)
}
```

---

## Special Considerations for this Pattern

### Why Container Options Matter

1. **Scale**: Docker CLI manages 98+ flags across 5+ commands
2. **Consistency**: Flags must behave identically across commands
3. **Validation**: Complex inter-field validation rules
4. **Versioning**: Different API versions have different capabilities
5. **Platforms**: Some flags are OS-specific

### Infrastructure Decisions That Enable the Pattern

1. **containerOptions struct** - Single source of truth for all flag values
2. **addFlags() function** - Centralized flag registration and validator initialization
3. **parse() function** - Clear separation between parsing and API conversion
4. **custom option types** - Validation happens during parsing via pflag.Value interface
5. **version annotations** - Runtime API version compatibility checks
6. **Mock client pattern** - Easy testing without real daemon

### Critical Infrastructure Points

| Aspect | Decision | Reason |
|--------|----------|--------|
| Module System | vendor.mod not go.mod | CalVer incompatible with SemVer |
| Build Tool | docker buildx bake | Multi-platform builds, reproducibility |
| Base Image | Alpine (default) + Debian (option) | Size/compatibility tradeoffs |
| Test Framework | gotest.tools/v3 + gotestsum | Better assertion messages, custom formatting |
| Linting | golangci-lint v2.6.1 | Unified multi-linter configuration |
| Flag Library | spf13/pflag + cobra | Industry standard, composable |
| API Client | moby/moby/client | Official Docker API client |

---

## Environment Variables for Build Customization

**At Build Time (docker-bake.hcl):**

```bash
# Set version
VERSION=29.0.0 docker buildx bake binary

# Use glibc instead of musl
USE_GLIBC=1 docker buildx bake dynbinary

# Strip debugging symbols
STRIP_TARGET=1 docker buildx bake binary

# Set Windows package name
PACKAGER_NAME="My Company" docker buildx bake binary

# Set Go version
GO_VERSION=1.26.0 docker buildx bake binary
```

**At Test Time (Makefile):**

```bash
# Custom test format
GOTESTSUM_FORMAT=short-verbose make test-unit

# Test specific directories
TESTDIRS="./cli/command/container" make test-unit

# Test with additional flags
TESTFLAGS="-race -count=3" make test-unit

# Disable Docker build cache
DOCKER_CLI_GO_BUILD_CACHE=n make -f docker.Makefile dev
```

**Development Container:**

```bash
# Set custom container name
DOCKER_CLI_CONTAINER_NAME=my-dev-session make -f docker.Makefile dev

# Set custom mounts
DOCKER_CLI_MOUNTS="-v /my/path:/src" make -f docker.Makefile dev

# Set ENGINE_VERSION for compatibility testing
ENGINE_VERSION=25.0.0 make -f docker.Makefile e2e
```

---

## Summary Table: What's Needed to Replicate

| Component | Purpose | Minimal | Full |
|-----------|---------|---------|------|
| docker-bake.hcl | Build orchestration | Yes | Yes |
| Dockerfile | Multi-stage builds | Yes | Yes |
| vendor.mod | Dependencies | If CalVer | Yes |
| golangci.yml | Code quality | Maybe | Yes |
| Makefile | Local shortcuts | Maybe | Yes |
| docker.Makefile | Container dev | Maybe | Yes |
| dockerfiles/ | Dev/test images | Maybe | Yes |
| scripts/ | Build helpers | Maybe | Yes |
| opts/ package | Reusable flag types | Yes | Yes |
| containerOptions | Shared flags | Yes | Yes |
| Test infrastructure | gotest.tools/v3 | Yes | Yes |

---

## File Reference

| File | Purpose | Lines | Complexity |
|------|---------|-------|------------|
| `/Dockerfile` | Multi-stage build pipeline | 150+ | High |
| `/docker-bake.hcl` | Build orchestration | 160+ | Medium |
| `/docker.Makefile` | Container development | 100+ | Medium |
| `/Makefile` | Local development | 100+ | Low |
| `/vendor.mod` | Go module declaration | 65+ deps | Medium |
| `/.golangci.yml` | Linting rules | 80+ | Medium |
| `/cli/command/container/opts.go` | Container options | 740 | High |
| `/cli/command/container/opts_test.go` | Option tests | 1100+ | Medium |
| `/opts/opts.go` | Custom types (ListOpts, MapOpts) | 400+ | High |
| `/scripts/build/binary` | Compilation script | 30 | Low |
| `/dockerfiles/Dockerfile.dev` | Dev container | 100+ | Medium |
| `/dockerfiles/Dockerfile.lint` | Lint container | 27 | Low |

---

## Key Takeaways

1. **Reproducibility**: Everything is containerized - builds work the same everywhere
2. **Scale**: Multi-stage Docker builds enable efficient use of build cache
3. **Platform Support**: BuildKit and cross-compilation tooling (xx) enable 11-platform builds
4. **Testing**: Infrastructure supports unit, integration, and e2e testing
5. **Quality**: Comprehensive linting and code formatting enforced at build time
6. **Dependency Management**: vendor.mod pattern works for CalVer projects
7. **Pattern Enables**: The container options pattern depends on pflag.Value interface for validation during parsing

To successfully adapt this pattern to another project:
- Start with the containerOptions struct design
- Implement custom option types (ListOpts, MapOpts, MemBytes, etc.)
- Create the addFlags() and parse() functions
- Add comprehensive tests using gotest.tools/v3
- Set up docker buildx bake for reproducible builds
