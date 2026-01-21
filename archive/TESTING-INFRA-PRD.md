# Clawker Testing Infrastructure PRD

## Overview

Implement a comprehensive testing infrastructure for the Clawker CLI that enables fast feedback during development while ensuring correctness of Docker image builds, project binding, container lifecycle management, Claude Code initialization, and host-container connectivity.

---

## Goals

1. **Fast local iteration**: Unit tests run in under 10 seconds with no external dependencies
2. **Confidence in templates**: Verify Dockerfile generation produces valid, buildable output
3. **Project isolation**: Tests do not pollute user config or leave Docker resources behind
4. **Schema safety**: Config changes break tests at compile time, not runtime
5. **Cacheable CI**: Template-aware caching avoids 10-minute rebuilds when templates haven't changed
6. **Claude Code verification**: Confirm containers reach healthy state with Claude Code running
7. **Command flexibility**: Verify both entrypoint flow and bypass modes work correctly
8. **Connectivity assurance**: Verify host proxy, SSH forwarding, and network policies function correctly

---

## Component 1: Test Utilities Package

**Location**: `internal/testutil/`

**Purpose**: Single source of truth for all test infrastructure. No other test utility packages should exist.

### Config Builder

A fluent, type-safe API for constructing `config.Config` objects in tests. When the config schema changes, the compiler identifies all affected tests.

Must support:
- All config sections (Build, Security, Agent, Workspace)
- Preset configurations (minimal valid config, full-featured config)
- Adaptation for test base images (swap base image, clear packages)

### Project Harness

Creates fully isolated project environments for tests including:
- Temporary project directory with `clawker.yaml`
- Isolated user config directory (settings, registered projects)
- Environment variable overrides that restore after test
- Working directory management with automatic restoration
- Expected resource name generation (images, containers, volumes)

### Docker Utilities

Helpers for tests that interact with Docker:
- Client acquisition with automatic cleanup
- Project resource cleanup (containers, volumes, networks, images by label)
- Test base image management with content-addressed tagging
- Template hash computation for cache invalidation

### Golden File Support

Compare test output against expected files stored alongside the test. Support update mode via environment variable.

---

## Component 2: Container Ready Signal

**Purpose**: Provide a reliable, deterministic signal that tests can wait for to know the container has completed initialization and Claude Code is running.

### Signal Design

**Marker File**

Entrypoint creates a file at a known path as the final step before exec-ing into Claude Code:

```
/var/run/clawker/ready
```

File contains timestamp and optional metadata (PID, versions). Tests poll for file existence.

**Stdout Marker**

Entrypoint prints a structured log line that tests can grep for:

```
[clawker] ready ts=1706123456 pid=42 agent=default
```

Use a prefix (`[clawker]`) that won't collide with Claude Code output.

**Both Approaches Together**

Create the file AND emit the log line. File is simpler to check, log line is visible in `docker logs` for debugging.

### Entrypoint Modifications

Add to end of `entrypoint.sh`, immediately before exec-ing Claude Code:

```bash
# Signal readiness
mkdir -p /var/run/clawker
echo "ts=$(date +%s) pid=$$" > /var/run/clawker/ready
echo "[clawker] ready ts=$(date +%s) agent=${CLAWKER_AGENT:-default}"

# Hand off to Claude Code
exec claude "$@"
```

### Failure Signals

Entrypoint should emit failure markers if setup fails:

```bash
if ! setup_firewall; then
    echo "[clawker] error component=firewall msg=setup failed"
    exit 1
fi
```

Tests fail fast on error patterns rather than waiting for timeout.

### Signal Timing

```
Container Start
    │
    ├── Entrypoint begins
    │   ├── Environment setup
    │   ├── Firewall init (if enabled)
    │   ├── SSH agent setup (if enabled)
    │   ├── Host proxy verification
    │   └── [clawker] ready        ← Tests wait for this
    │
    └── exec claude
        └── Claude Code starts     ← Tests verify process exists
```

### Timeout Values

| Environment | Ready Timeout | Rationale |
|-------------|---------------|-----------|
| Integration tests | 60 seconds | Cached image, minimal setup |
| E2E tests | 120 seconds | May include more setup steps |
| CI | 180 seconds | Slower runners, cold caches |
| Local development | 60 seconds | Fast feedback preferred |

Configurable via environment variable: `CLAWKER_READY_TIMEOUT`

### Healthcheck Integration

Add to generated Dockerfile so `docker ps` shows health status:

```dockerfile
HEALTHCHECK --interval=5s --timeout=3s --start-period=30s --retries=3 \
    CMD test -f /var/run/clawker/ready || exit 1
```

Tests can use `docker inspect` to check health state instead of custom polling.

---

## Component 3: Claude Code Verification

**Purpose**: Confirm the container reaches a healthy state with Claude Code running, without attempting to snapshot or interact with the REPL.

### Success Criteria

The container is considered healthy when:

1. Entrypoint script completes without error
2. Claude Code process is running
3. Expected environment is configured correctly
4. Network connectivity to required services works

### Verification Methods

**Process Check**

Exec into container and verify Claude Code process exists. Use `pgrep` or `/proc` inspection. Do not attach to the process.

**Log Pattern Matching**

Monitor container logs for expected startup sequence:
- Entrypoint completion marker
- Claude Code initialization output
- Fail on error patterns or timeout

**Environment Validation**

Exec into container and verify:
- Required environment variables are set
- Firewall rules applied (when enabled)
- SSH agent socket exists (when enabled)
- Host proxy is reachable from container

### What NOT to Test

- Claude Code REPL output or behavior (third-party, non-deterministic)
- AI response content
- Interactive PTY rendering
- Anything requiring Claude Code authentication

### Test Matrix

| Test | Tier | What It Proves |
|------|------|----------------|
| Process exists | Integration | Claude Code binary launched |
| Entrypoint completes | Integration | Setup scripts succeeded |
| Environment correct | Integration | Config translated to runtime correctly |
| Full startup | E2E | Everything works together |

---

## Component 4: Command Mode Testing

**Purpose**: Verify that run/exec commands work correctly in all modes, mimicking Docker CLI behavior where entrypoint can be bypassed for arbitrary commands.

### Command Modes

Clawker supports multiple execution modes that mirror Docker CLI patterns:

**Standard Mode (Entrypoint Flow)**

clawker detects if --agent is passed or not, if agent is passed it runs the container matching the agent, if --agent is not passed it expects a container name or id to run
The agent flag should prevent use of container name or id

```bash
clawker container run --agent default # runs container "clawker.projectName.default"
clawker container run clawker.projectName.containerName # runs container "clawker.projectName.containerName"
clawker container run --agent default clawker.projectName.containerName # runs container "clawker.projectName.default" and passes "clawker.projectName.containerName" as shell command to the container which will return a shell error which is expected. clawker will not verify an expected error here, tests should expect the container to immediately exit (command -v is will be ran against it)
```

Entrypoint runs, detects if the first container argument is a flags (starts with -) or a system command (command -v) if the arg is a flag or isn't a system command everything is passed to claude code, otherwise it is treated as an arbitrary command

Runs entrypoint → setup → exec Claude Code. Full initialization.

**Entrypoint Detection Snippet**
```shell
# If first argument starts with "-" or isn't a command, prepend "claude"
if [ "${1#-}" != "${1}" ] || [ -z "$(command -v "${1}" 2>/dev/null)" ]; then
    set -- claude "$@"
fi
```

**Claude Code Flags Mode**

If --agent is passed without a container name or id, everything after -- is passed to the entrypoint script

```bash
clawker container run --agent default -- --print "hello"
clawker container run --agent default -- --resume
clawker container run --agent default -- --model sonnet
```

if --agent is not passed, and a name or id is used instead, -- is not required

```bash
clawker container run clawker.projectName.containerName --print "hello"
clawker container run clawker.projectName.containerName --resume
```

**Arbitrary Command Mode**

```bash
clawker container run --agent default -- bash
clawker container run --agent default -- /bin/sh -c "echo hello"
clawker container run clawker.projectName.containerName bash
clawker container run clawker.projectName.containerName /bin/sh -c "echo hello"
```

Exec works in the same way as run
**Exec Mode**

```bash
# claude code args
clawker container exec --agent default -- --print "hello"
clawker container exec clawker.projectName.containerName --print "hello"

# arbitrary commands
clawker container exec --agent default -- bash
clawker container exec --agent default -- /bin/sh -c "echo hello"
clawker container exec clawker.projectName.containerName bash
clawker container exec clawker.projectName.containerName /bin/sh -c "echo hello"
```

Run commands in already-running container. Environment inherited from container.

### Test Scenarios

**Entrypoint Flow**

| Scenario | Command | Verify |
|----------|---------|--------|
| Default startup | `run` | Ready signal emitted, Claude process running |
| With Claude flags and agent flag | `run --agent -- --print "hi"` | Flags passed to Claude Code |
| With Claude flags and container name | `run <name> --print "hi"` | Flags passed to Claude Code |
| Detached | `run -d` | Container running in background, ready signal present |

**Arbitrary Command (via Entrypoint)**

| Scenario | Command | Verify |
|----------|---------|--------|
| Shell via entrypoint using agent flag  | `run --agent -- bash` | Gets shell, entrypoint ran first |
| Command via entrypoint | `run --agent -- ls /` | Lists root, entrypoint setup applied |
| Shell via entrypoint using container name | `run <name> bash` | Gets shell, entrypoint ran first |
| Command via entrypoint | `run <name> ls /` | Lists root, entry point setup applied |

**Exec Into Running**

| Scenario | Command | Verify |
|----------|---------|--------|
| Basic exec using agent flag | `exec --agent <name> -- whoami` | Returns expected claude:claude user |
| Environment using agent flag | `exec --agent <name> -- env` | Clawker env vars present |
| Verify ready using agent flag | `exec --agent <name> -- cat /var/run/clawker/ready` | Ready file exists |
| Run script using agent flag | `exec --agent <name> -- /scripts/test.sh` | Script executes |
| Basic exec using container name | `exec <name> whoami` | Returns expected claude:claude user |
| Environment using container name | `exec <name> env` | Clawker env vars present |
| Verify ready using container name | `exec <name> cat /var/run/clawker/ready` | Ready file exists |
| Run script using container name | `exec <name> /scripts/test.sh` | Script executes |

**Error Cases**

| Scenario | Command | Verify |
|----------|---------|--------|
| Invalid entrypoint | `run --entrypoint /nonexistent` | Clear error message |
| Command not found | `exec <id> -- notacommand` | Non-zero exit, error in stderr |
| Exec on stopped | `exec <stopped-id> -- ls` | Clear error about container state |
| Bad Claude flag | `run -- --invalid-flag` | Claude Code error propagates |

### Test Implementation

**Arbitrary Command Tests (Entrypoint Runs)**

Verify entrypoint detects system commands:
Run both with --agent and container name/id variants:
```
1. Run with -- bash or -- ls
2. Verify entrypoint setup completed (env vars set)
3. Verify command ran (not Claude Code)
4. Ready signal may or may not be emitted depending on implementation
```

**Flag Passthrough Tests**

Verify flags reach Claude Code without full REPL interaction:
Run both with --agent and container name/id variants:
```
1. Run with -- --version or -- --help (if supported)
2. Capture stdout
3. Verify Claude Code received the flag (version output, help text)
4. Don't test REPL behavior
```

**Environment Inheritance Tests**

Verify exec inherits container environment:
Run both with --agent and container name/id variants:
```
1. Start container normally, wait for ready
2. Exec: env | grep CLAWKER
3. Verify expected variables present
4. Exec: cat /var/run/clawker/ready
5. Verify ready file contents
```

### Scope Boundaries

**Test**:
- Entrypoint runs or is correctly bypassed
- Entrypoint correctly detects system commands vs Claude flags
- Arguments pass through correctly
- Exit codes propagate
- Stdout/stderr captured correctly
- Environment variables set appropriately per mode

**Don't Test**:
- Claude Code flag behavior (not our code)
- What Claude Code does with flags
- REPL interaction

---

## Component 5: Network & Connectivity Verification

**Purpose**: Verify that network features work correctly both inside and outside the container, including loopback, host proxy communication, and SSH agent forwarding.

### Loopback Verification

**What to Test**

Container can make requests to localhost/127.0.0.1 for internal services.

**Test Scenarios**

| Scenario | Method | Verify |
|----------|--------|--------|
| Localhost resolution | `exec -- getent hosts localhost` | Returns 127.0.0.1 |
| Loopback binding | `exec -- nc -l 127.0.0.1 8888 &` then connect | Connection succeeds |
| Loopback curl | `exec -- curl http://127.0.0.1:<port>` | Response received |

### Host Proxy Verification

**What to Test**

Container can reach host proxy server for browser open requests and OAuth callbacks.

**Architecture**

```
┌─────────────────────┐     ┌─────────────────────────┐
│     Container       │     │          Host           │
│                     │     │                         │
│  Claude Code        │     │                         │
│      │              │     │                         │
│      ▼              │     │                         │
│  host-open.sh ──────┼────►│  Host Proxy Server      │
│                     │     │      │                  │
│  callback-          │     │      ▼                  │
│  forwarder.sh ◄─────┼─────│  Browser / OAuth        │
│                     │     │                         │
└─────────────────────┘     └─────────────────────────┘
```

**Test Scenarios**

| Scenario | Method | Verify |
|----------|--------|--------|
| Proxy reachable | `exec -- curl $CLAWKER_HOST_PROXY/health` | 200 OK |
| Open URL request | `exec -- /scripts/host-open.sh "https://example.com"` | Host proxy receives request |
| Callback registration | Start callback listener, trigger OAuth flow | Callback received in container |
| Proxy env var set | `exec -- echo $CLAWKER_HOST_PROXY` | Non-empty, valid URL |

**Host Proxy Health Endpoint**

Host proxy must expose a health endpoint for container verification:

```
GET /health -> 200 OK {"status": "healthy", "ts": 1234567890}
```

**Test Implementation**

```
1. Start host proxy (or ensure test starts it)
2. Start container with host proxy configured
3. Wait for ready signal
4. Exec curl to proxy health endpoint
5. Verify 200 response
6. Test open URL endpoint (mock browser or verify request logged)
7. Test callback flow (register callback, simulate OAuth redirect, verify receipt)
```

### SSH Agent Forwarding Verification

**What to Test**

SSH agent socket is:
1. Forwarded into container correctly
2. Accessible inside container
3. Listening and functional on host

**Architecture**

```
┌─────────────────────┐     ┌─────────────────────────┐
│     Container       │     │          Host           │
│                     │     │                         │
│  $SSH_AUTH_SOCK ────┼────►│  SSH Agent Proxy        │
│      │              │     │      │                  │
│      ▼              │     │      ▼                  │
│  ssh-agent-proxy    │     │  Host SSH Agent         │
│                     │     │  ($SSH_AUTH_SOCK)       │
│  ssh-add -l         │     │                         │
│  git clone (ssh)    │     │                         │
│                     │     │                         │
└─────────────────────┘     └─────────────────────────┘
```

**Test Scenarios - Inside Container**

| Scenario | Method | Verify |
|----------|--------|--------|
| Socket exists | `exec -- test -S $SSH_AUTH_SOCK` | Exit 0 |
| Agent responds | `exec -- ssh-add -l` | Lists keys or "no identities" (not error) |
| SSH connection | `exec -- ssh -T git@github.com` | Auth succeeds (if host has GitHub key) |
| Env var set | `exec -- echo $SSH_AUTH_SOCK` | Non-empty path |

**Test Scenarios - Outside Container (Host Side)**

| Scenario | Method | Verify |
|----------|--------|--------|
| Proxy process running | `pgrep ssh-agent-proxy` | Process exists |
| Proxy socket listening | `test -S <proxy-socket-path>` | Socket exists |
| Proxy accepts connections | Connect to proxy socket | No connection refused |
| Host agent accessible | `ssh-add -l` on host | Agent responsive |

**Test Implementation**

```
1. Ensure host SSH agent running with at least one key (or skip)
2. Start container with SSH forwarding enabled
3. Wait for ready signal
4. Verify inside container:
   a. SSH_AUTH_SOCK env var set
   b. Socket file exists
   c. ssh-add -l returns 0 (agent responds)
5. Verify outside container:
   a. ssh-agent-proxy process running
   b. Proxy socket exists
6. Optional: Test actual SSH operation (git clone)
```

**Skip Conditions**

SSH tests should skip gracefully when:
- Host has no SSH agent running
- Host SSH agent has no keys loaded
- SSH forwarding disabled in config

### Firewall Verification

**What to Test**

When firewall is enabled, only allowed hosts are reachable.

**Test Scenarios**

| Scenario | Method | Verify |
|----------|--------|--------|
| Allowed host reachable | `exec -- curl https://api.anthropic.com` | Connection succeeds |
| Blocked host unreachable | `exec -- curl https://blocked.example.com` | Connection refused/timeout |
| Rules applied | `exec -- iptables -L` or `ufw status` | Expected rules present |
| DNS works for allowed | `exec -- nslookup api.anthropic.com` | Resolves |

### Git Credential Forwarding Verification

**What to Test**

Git credential helper forwards to host proxy for HTTPS authentication.

**Test Scenarios**

| Scenario | Method | Verify |
|----------|--------|--------|
| Credential helper configured | `exec -- git config credential.helper` | Returns clawker helper |
| Helper callable | `exec -- git credential-clawker get` | Returns or prompts via proxy |
| HTTPS clone works | `exec -- git clone https://github.com/...` | Clone succeeds (if host has creds) |

### Test Matrix Summary

| Feature | Enabled By | Inside Container | Outside Container |
|---------|------------|------------------|-------------------|
| Loopback | Always | `curl 127.0.0.1` | N/A |
| Host Proxy | Always | `curl $CLAWKER_HOST_PROXY/health` | Proxy process running |
| SSH Forward | `security.ssh_forward: true` | `ssh-add -l` succeeds | Proxy socket listening |
| Firewall | `security.firewall.enabled: true` | Blocked hosts fail | N/A |
| Git Creds | `security.http_proxy: true` | Helper configured | Proxy handles /git/* |

### Connectivity Test Utilities

Add helpers to `internal/testutil/`:

**WaitForHostProxy**
```
Poll proxy health endpoint until responsive or timeout
```

**VerifySSHAgent**
```
Check SSH_AUTH_SOCK set, socket exists, ssh-add responds
Skip if host agent unavailable
```

**VerifyFirewall**
```
Attempt connection to known-blocked host, verify failure
Attempt connection to known-allowed host, verify success
```

**VerifyLoopback**
```
Start listener on loopback, connect from same container, verify
```

---

## Component 6: Test Tiers

### Tier 1: Unit Tests

**Scope**: Pure logic with no external dependencies

**Runtime**: < 10 seconds total

**Trigger**: Every commit, pre-push hook

What to test:
- Config parsing and validation
- Dockerfile template context generation
- Resource naming conventions
- Label generation
- Semver parsing
- Command flag parsing
- Argument parsing for bypass/passthrough modes
- Host proxy URL generation
- SSH socket path generation

What NOT to test:
- Actual Docker operations
- Actual network connections
- File system side effects outside temp directories

### Tier 2: Integration Tests

**Scope**: Tests requiring Docker daemon

**Runtime**: < 5 minutes total

**Trigger**: PR builds, local on-demand

**Build tag**: `integration`

What to test:
- Image builds complete successfully
- Container lifecycle operations work
- Label-based resource isolation functions correctly
- Project binding applies correct labels and names
- Build context tar archives are complete and valid
- Entrypoint bypass modes function
- Arbitrary command detection in entrypoint
- Exec command works on running containers
- Ready signal emitted correctly
- Claude Code process starts
- Loopback connectivity works
- Host proxy reachable from container
- SSH agent forwarding functional (when host agent available)
- Firewall rules applied (when enabled)

Caching strategy:
- Use template-generated test base image
- Content-address the image tag using template file hashes
- Cache the heavy OS dependency layer separately from template layer

### Tier 3: E2E Tests

**Scope**: Full workflows from CLI invocation to verified container state

**Runtime**: < 15 minutes

**Trigger**: Main branch merges, release candidates

**Build tag**: `e2e`

What to test:
- `clawker init` → `clawker project init` → `clawker container run` workflow
- Container starts and Claude Code process initializes
- Host proxy communication works end-to-end
- Browser open requests reach host
- OAuth callback flow completes
- SSH forwarding works for actual git operations (when keys available)
- Firewall blocks unauthorized hosts
- Full lifecycle: run → exec → stop → start → remove
- Detached mode with reattach
- Multiple agents for same project
- All command modes (standard, flags, arbitrary, bypass, exec)

---

## Component 7: Test Organization

### File Placement

- Unit tests: `*_test.go` alongside source files
- Integration tests: `*_integration_test.go` alongside source files with build tag
- Golden files: `testdata/` directory within each package that needs them
- No global `testdata/` or `e2e/` directories at repository root

### Packages to Delete

- `pkg/cmd/testutil/` — consolidate into `internal/testutil/`

---

## Component 8: CI Pipeline

### Jobs

1. **Unit**: Runs on all pushes and PRs. No Docker required.
2. **Integration**: Runs on PRs. Requires Docker. Uses cached test base image.
3. **E2E**: Runs on main branch and release tags. Full workflow validation.
4. **Nightly Full Build**: Weekly or nightly. Builds from scratch without cache to catch environment drift.

### Caching Strategy

Cache key for test base image must include:
- Hash of all template files (`pkg/build/templates/*`)
- Hash of `DockerfileContext` struct definition
- Hash of build context generation logic

When cache misses, build new test base and update cache.

### CI Environment Requirements

For full test coverage, CI runners need:
- Docker daemon
- SSH agent with at least one key (for SSH tests, or tests skip gracefully)
- Network access to test allowed/blocked hosts

---

## Component 9: Makefile Targets

| Target | Description |
|--------|-------------|
| `test` | Unit tests only (default, fast) |
| `test-integration` | Unit + integration tests |
| `test-e2e` | All tests including E2E |
| `test-coverage` | Unit tests with coverage report |
| `test-base` | Build/rebuild test base image |
| `golden-update` | Regenerate all golden files |
| `test-clean` | Remove test Docker resources |

---

## Constraints

1. **No YAML fixture files for configs**: All test configs constructed programmatically via ConfigBuilder
2. **No global test directories**: Each package owns its own `testdata/` if needed
3. **No test pollution**: Every test cleans up its Docker resources and restores environment state
4. **No network in unit tests**: Unit tests must pass offline
5. **Parallel safe**: Tests must support `go test -parallel` without resource conflicts
6. **No Claude Code interaction**: Tests verify process runs, never interact with REPL
7. **Bypass tests are fast**: Tests using entrypoint bypass should complete in seconds
8. **Graceful skips**: Tests requiring host resources (SSH agent, keys) skip cleanly when unavailable
9. **Host proxy lifecycle**: Tests must manage host proxy lifecycle or assume it's running

---

## Success Criteria

1. `go test -short ./...` completes in under 10 seconds
2. Adding a required field to config schema produces compile errors in tests, not runtime failures
3. CI caches test base image and skips rebuild when templates unchanged
4. No orphaned Docker resources after test suite completion
5. New developers can run full test suite with only Docker installed
6. Integration tests verify all connectivity features work
7. E2E tests complete full workflow including network verification
8. SSH tests skip gracefully on machines without SSH agent
9. All command modes (standard, flags, arbitrary, bypass, exec) have test coverage
10. Host proxy health is verified before container tests that depend on it

---

## Out of Scope

- Performance/benchmark testing
- Fuzz testing
- Multi-platform testing (Linux/macOS/Windows matrix)
- Claude Code REPL interaction testing
- OAuth provider integration testing (mock only)
- Actual git clone from real repositories (use local test repos)
