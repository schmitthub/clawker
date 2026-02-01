# CLI Workflow Tests

This directory contains CLI workflow tests using Go's [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript) framework. Tests validate clawker CLI workflows against a real Docker daemon.

## Running Tests

```bash
# All CLI workflow tests
go test ./test/cli/... -v -timeout 15m

# Specific category
go test -run ^TestContainer$ ./test/cli/... -v
go test -run ^TestVolume$ ./test/cli/... -v
go test -run ^TestNetwork$ ./test/cli/... -v
go test -run ^TestImage$ ./test/cli/... -v
go test -run ^TestRalph$ ./test/cli/... -v
go test -run ^TestProject$ ./test/cli/... -v
go test -run ^TestRoot$ ./test/cli/... -v

# Single test script
CLAWKER_ACCEPTANCE_SCRIPT=run-basic.txtar go test -run ^TestContainer$ ./test/cli/... -v

# Via Makefile
make test-cli
```

## Directory Structure

```
test/cli/
├── acceptance_test.go           # Test harness and custom commands
├── README.md                    # This file
└── testdata/
    ├── container/               # Container management tests
    │   ├── run-basic.txtar
    │   ├── start-stop.txtar
    │   └── ...
    ├── volume/                  # Volume management tests
    ├── network/                 # Network management tests
    ├── image/                   # Image management tests
    ├── ralph/                   # Ralph autonomous loop tests
    ├── project/                 # Project init tests
    └── root/                    # Root-level command tests
```

## Test File Format (.txtar)

Each `.txtar` file contains:
1. **Script section**: Commands and assertions
2. **File sections**: Test fixtures (config files, etc.)

```txtar
# Comment describing the test
# Verifies: specific behavior

# Script commands
exec clawker container ls
stdout 'some-expected-output'

-- clawker.yaml --
version: "1"
project: "test-project"
```

### Script Syntax

| Syntax | Description |
|--------|-------------|
| `exec cmd args` | Run command, fail on non-zero exit |
| `! exec cmd args` | Run command, fail on zero exit |
| `stdout 'pattern'` | Assert stdout matches pattern |
| `! stdout 'pattern'` | Assert stdout does NOT match pattern |
| `stderr 'pattern'` | Assert stderr matches pattern |
| `! stderr 'pattern'` | Assert stderr does NOT match pattern |
| `exists file` | Assert file exists |
| `! exists file` | Assert file does NOT exist |
| `grep 'pattern' file` | Assert file contains pattern |
| `cp src dst` | Copy file |
| `mkdir dir` | Create directory |

## Environment Variables

### Injected by Test Harness

| Variable | Description | Example |
|----------|-------------|---------|
| `$PROJECT` | Unique project name for test isolation | `acceptance-run_basic-a1b2c3d4e5` |
| `$RANDOM_STRING` | Random 10-char alphanumeric | `a1b2c3d4e5` |
| `$SCRIPT_NAME` | Test script name (underscored) | `run_basic` |
| `$HOME` | Set to work directory for isolation | `/tmp/testscript123/work` |
| `$CLAWKER_SPINNER_DISABLED` | Disables spinners | `1` |

### For Running Tests

| Variable | Description |
|----------|-------------|
| `CLAWKER_ACCEPTANCE_PROJECT` | Override project prefix (default: `acceptance`) |
| `CLAWKER_ACCEPTANCE_SCRIPT` | Run single script (e.g., `run-basic.txtar`) |
| `CLAWKER_ACCEPTANCE_PRESERVE_WORK_DIR` | Keep work directory after test (`true`/`1`) |
| `CLAWKER_ACCEPTANCE_SKIP_DEFER` | Skip deferred cleanups (`true`/`1`) |
| `UPDATE_GOLDEN` | Update golden files (`1`) |

## Custom Commands

### `defer`

Registers a cleanup command to run after the test (LIFO order).

```txtar
exec clawker container run -d --agent myagent alpine sleep 3600
defer clawker container rm --force --agent myagent
```

### `replace`

Performs variable substitution in a file.

```txtar
replace clawker.yaml PROJECT=$PROJECT
```

### `wait_container_running`

Waits for a container to reach running state.

```txtar
exec clawker container run -d --agent myagent alpine sleep 3600
wait_container_running clawker.$PROJECT.myagent 30
```

Arguments:
- `CONTAINER_NAME`: Container name or ID
- `TIMEOUT_SECONDS`: Optional timeout (default: 30)

### `wait_container_exit`

Waits for a container to exit.

```txtar
exec clawker container run -d --agent myagent alpine echo hello
wait_container_exit clawker.$PROJECT.myagent 60 0
```

Arguments:
- `CONTAINER_NAME`: Container name or ID
- `TIMEOUT_SECONDS`: Optional timeout (default: 60)
- `EXPECTED_CODE`: Optional expected exit code (default: any)

### `wait_ready_file`

Waits for clawker ready file (`/tmp/.clawker-ready`) to exist in container.

```txtar
wait_ready_file clawker.$PROJECT.myagent 120
```

### `container_id`

Gets container ID and stores in environment variable.

```txtar
container_id clawker.$PROJECT.myagent CONTAINER_ID
# Now $CONTAINER_ID contains the full container ID
```

### `container_state`

Gets container state and stores in environment variable.

```txtar
container_state clawker.$PROJECT.myagent STATE
# $STATE is one of: running, exited, paused, etc.
```

### `cleanup_project`

Removes all resources for a project (containers, volumes, networks, images).

```txtar
cleanup_project $PROJECT
```

### `stdout2env`

Captures stdout from previous command into environment variable.

```txtar
exec clawker image list --format '{{.ID}}'
stdout2env IMAGE_ID
# Now $IMAGE_ID contains the image ID
```

### `sleep`

Pauses execution for specified seconds.

```txtar
sleep 2
```

### `env2upper`

Sets environment variable to uppercase value.

```txtar
env2upper UPPER_PROJECT=$PROJECT
# $UPPER_PROJECT is now uppercase
```

## Writing Tests

### Basic Pattern

```txtar
# Test description
# Verifies: what this test validates

# Substitute project name in config
replace clawker.yaml PROJECT=$PROJECT

# Create settings directory (simulates user has run clawker init)
mkdir $HOME/.local/clawker

# Run commands and assert
exec clawker container run --rm alpine echo hello
stdout 'hello'

-- clawker.yaml --
version: "1"
project: "$PROJECT"

build:
  image: "alpine:latest"

security:
  firewall:
    enable: false
```

### Testing with Containers

```txtar
# Start container, defer cleanup
exec clawker container run -d --agent myagent alpine sleep 3600
defer clawker container rm --force --agent myagent

# Wait for running state
wait_container_running clawker.$PROJECT.myagent

# Perform operations
exec clawker container exec --agent myagent -- echo hello
stdout 'hello'

# Stop container
exec clawker container stop --agent myagent
```

### Output Conventions

Know where commands output:

| Command Type | stdout | stderr |
|--------------|--------|--------|
| List commands | Data | - |
| Create/Delete | Resource name/ID | Status messages |
| Build | - | Progress/status |
| Status commands | JSON (with `--json`) | Human-readable |

## Debugging

### Preserve Work Directory

```bash
CLAWKER_ACCEPTANCE_PRESERVE_WORK_DIR=true go test -run ^TestContainer$ ./test/cli/... -v
```

The work directory path is logged at the start of each test.

### Skip Cleanup

```bash
CLAWKER_ACCEPTANCE_SKIP_DEFER=true go test -run ^TestContainer$ ./test/cli/... -v
```

Useful for inspecting container/resource state after test failure.

### Run Single Script

```bash
CLAWKER_ACCEPTANCE_SCRIPT=run-basic.txtar go test -run ^TestContainer$ ./test/cli/... -v
```

### View Detailed Output

Use `-v` flag for verbose output including command stdout/stderr.

## CI Integration

Acceptance tests require Docker. In CI:

```yaml
# GitHub Actions example
- name: Run CLI workflow tests
  run: go test ./test/cli/... -v -timeout 15m

  env:
    CLAWKER_SPINNER_DISABLED: "1"
```

Key considerations:
- Timeout: 15 minutes minimum (image builds can be slow)
- Docker service must be available
- Tests are parallelized by category, not within categories
- Each test gets unique project name for isolation

## Test Categories

| Category | Tests | Description |
|----------|-------|-------------|
| Container | 14+ | Container lifecycle, exec, logs, cp |
| Volume | 3 | Create, list, inspect, remove |
| Network | 3 | Create, list, inspect, remove |
| Image | 4 | Build, list, inspect, prune |
| Ralph | 3 | Status, reset, JSON output |
| Project | 2 | Project init, force overwrite |
| Root | 2 | User init, build |

## Troubleshooting

### "Docker not available"

Tests skip if Docker daemon is unreachable. Ensure Docker is running.

### "timeout waiting for container"

Container failed to start or exited unexpectedly. Check:
- Container logs: `docker logs <container_name>`
- Exit diagnostics are logged on failure
- Firewall issues (NET_ADMIN capability)

### Test pollution

Each test gets a unique `$PROJECT` name. If seeing pollution:
1. Check defer commands are registered
2. Verify cleanup_project is called if manual cleanup needed
3. Use `CLAWKER_ACCEPTANCE_SKIP_DEFER=true` to inspect state
