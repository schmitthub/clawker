# Testing Infrastructure - Docker CLI Repository

## Overview

```
Build System: docker buildx bake (multi-stage Dockerfile)
Language Runtime: Go 1.25.6
Containerized: Yes (Full Docker-based test execution)
CI/CD Platform: GitHub Actions (ubuntu-24.04 primary, macOS secondary)
Test Frameworks: gotestsum (v1.13.0), golangci-lint (v2.6.1)
E2E Environment: Docker Compose with docker-in-docker
External Dependencies: 15+ services/tools
Coverage Analysis: Codecov integration
```

## Build System for Tests

### Primary Bake Targets (docker-bake.hcl)

- `test`: Unit tests with gotestsum (cache-only output)
- `test-coverage`: Unit tests with coverage profiling (outputs to build/coverage/)
- `e2e-image`: E2E test environment image (docker-in-docker + tools)
- `lint`: golangci-lint validation (v2.6.1)
- `shellcheck`: Shell script validation

### Dockerfile Stages

- **Unit test stage (test):** Mounts Go module/build caches, runs gotestsum
- **Test-coverage stage:** Extracts coverage artifacts
- **E2E stage:** Includes gotestsum, buildx (0.29.1), compose (v2.40.0), SSH client
- **Development container (dev):** Pre-installed tools for interactive development

## CI/CD Pipeline (GitHub Actions)

### Three Main Workflows

#### 1. test.yml - Unit and Platform Tests
- Container job: Runs `docker buildx bake test-coverage` on ubuntu-24.04
- Host jobs: macOS matrix (macos-14, macos-15-intel, macos-15) using native Go
- Coverage uploaded to Codecov v5

#### 2. e2e.yml - End-to-End Testing Matrix
- 16 test combinations: 2 targets (local, connhelper-ssh) x 2 bases (alpine, debian) x 4 engine versions (rc, 29, 28, 25)
- Sets up daemon.json with experimental features
- Runs via `make -f docker.Makefile test-e2e-$target`
- Coverage profiling with `-coverprofile=/tmp/coverage.txt` flag

#### 3. validate.yml - Code Quality Checks
- Lint, shellcheck, validate-vendor targets via bake
- Markdown validation: Regenerates docs and checks git diff
- Make targets validation: Tests yamldocs and manpages generation

#### 4. build.yml - Binary Builds and Artifacts
- Matrix: 2 targets (binary, dynbinary) x 11 platforms x 2 variants (musl, glibc)
- Artifacts uploaded with platform-specific naming
- Cache strategy: GitHub Actions cache backend with scope/mode=max

## Test Environment Setup

### Docker-in-Docker Composition (e2e/compose-env.yaml)

- **registry:3 service:** For push/pull tests on port 5000
- **docker:${ENGINE_VERSION}-dind engine:** Configurable versions (25, 28, 29, rc)
  - Privileged mode enabled
  - Insecure registry flag: `--insecure-registry=registry:5000`
  - Experimental features enabled
  - No TLS (`DOCKER_TLS_CERTDIR=`)

### SSH Connection Helper (e2e/compose-env.connhelper-ssh.yaml)

- Custom `Dockerfile.connhelper-ssh` based on `docker:${ENGINE_VERSION}-dind`
- Adds openssh-server/client, creates 'penguin' user with docker group access
- Generates SSH host keys
- Connection via `ssh://penguin@${engine_ip}`

## Test Orchestration Scripts

### Entry Script (scripts/test/e2e/entry)
- Conditional: Remote daemon vs docker-compose DinD mode
- Delegates to wrapper script for DinD setup

### Wrapper (scripts/test/e2e/wrapper)
- Three-phase execution: setup -> test -> cleanup
- Exits with test result code

### Run Script (scripts/test/e2e/run)
- **Setup:** Generates SSH keys, starts docker-compose, connects to network, initializes swarm
- **Test execution:** Environment isolation via `env -i`, passes `DOCKER_CLI_E2E_PLUGINS_EXTRA_DIRS`
- **Cleanup:** Network disconnect, compose down with volume removal

## Development Environment

### Dev Container (Dockerfile.dev)
- Based on `golang:1.25.6-alpine3.22`
- Pre-installed: bash, build-base, curl, git, jq, nano
- Tools: gofumpt (v0.7.0), gotestsum (v1.13.0), goversioninfo (v1.5.0), docker-buildx (0.29.1)
- Mounts: `/var/run/docker.sock` for Docker access
- Build cache: Named volume `docker-cli-dev-cache` (persistent)

### Local Test Execution

```bash
# Unit tests
make test-unit                    # Direct Go
make -f docker.Makefile test-unit # Docker-based

# With coverage
make test-coverage
make -f docker.Makefile test-coverage

# E2E tests
make -f docker.Makefile test-e2e-local
make -f docker.Makefile test-e2e-connhelper-ssh

# Single test
gotestsum -- ./cli/command/container/... -run TestRunLabel
```

## Caching Strategies

### Docker Layer Cache
- GitHub Actions cache backend with scope (test, lint, bin-image, etc.)
- Mode: max (caches all intermediate layers)
- Shared across identical bake targets

### Go Build Cache (Persisted)
- `/root/.cache`: Go build cache
- `/go/pkg/mod`: Module cache
- Mounted in Dockerfile via `--mount=type=cache`
- Docker volume (`docker-cli-dev-cache`) for interactive development

## Coverage Handling

### Collection
- Unit tests: `gotestsum -- ... -coverprofile=/tmp/coverage.txt`
- E2E tests: Same via TESTFLAGS
- Output: `./build/coverage/coverage.txt`

### Upload
- `codecov/codecov-action@v5`
- Authenticated with `CODECOV_TOKEN` secret
- Uploads from container and host jobs

## Required Infrastructure

### Must Have
- Docker 20.10+ with BuildKit
- docker-buildx plugin
- Ability to run privileged containers
- Docker Hub access (for base images)
- GitHub Actions with ubuntu-24.04 runners
- Codecov.io account (free for public repos)

### Optional
- macOS runners (for native build validation)
- Docker Hub account (for pushing cli-bin images)
- Additional storage for build cache (~2GB)

## Key Metrics

| Metric | Value |
|--------|-------|
| Unit test matrix | 1 platform (containerized) + 3 macOS variants |
| E2E test matrix | 16 combinations (2 x 2 x 4) |
| Build platforms | 11 (darwin/amd64, darwin/arm64, linux/*, windows/*) |
| Tools installed in e2e | 5 (gotestsum, buildx, compose, binary, plugins) |
| Base image variants | 2 (Alpine, Debian) |
| Engine versions tested | 4 (25, 28, 29, rc) |
