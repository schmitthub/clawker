---
paths:
  - "pkg/cmd/**/*.go"
---

# CLI Testing Guide

> **LLM Memory Document**: Reference this document when writing CLI command tests. Contains both automated integration tests and manual test patterns.

## Automated Integration Tests

Integration tests execute actual `clawker` commands and verify Docker state (containers, volumes, labels).
Integration tests require a `clawker.yaml` file in the current working directory

### Location

```
pkg/cmd/run/run_integration_test.go       # run command integration tests
pkg/cmd/run/testdata/clawker.yaml        # test config

pkg/cmd/start/start_integration_test.go   # start command integration tests
pkg/cmd/start/testdata/clawker.yaml      # test config

pkg/cmd/stop/stop_integration_test.go     # stop command integration tests
pkg/cmd/stop/testdata/clawker.yaml       # test config

pkg/cmd/remove/remove_integration_test.go # remove command integration tests
pkg/cmd/remove/testdata/clawker.yaml     # test config

pkg/cmd/prune/prune_integration_test.go   # prune command integration tests
pkg/cmd/prune/testdata/clawker.yaml      # test config
```

### Running Tests

```bash
# Unit tests only (fast, no Docker required)
go test ./pkg/cmd/...

# All integration tests (requires Docker)
go test ./pkg/cmd/... -tags=integration -v -timeout 10m

# Run specific command tests
go test ./pkg/cmd/run/... -tags=integration -v -timeout 5m
go test ./pkg/cmd/start/... -tags=integration -v -timeout 5m
go test ./pkg/cmd/stop/... -tags=integration -v -timeout 5m
go test ./pkg/cmd/remove/... -tags=integration -v -timeout 5m
go test ./pkg/cmd/prune/... -tags=integration -v -timeout 5m

# Run specific test
go test ./pkg/cmd/run/... -tags=integration -v -run TestRun_DefaultCleanup
```

### Test Cases

#### Run Command

| Test | Purpose |
|------|---------|
| `TestRun_DefaultCleanup` | Verify container + volumes removed on exit by default |
| `TestRun_KeepFlag` | Verify `--keep` preserves container + volumes |
| `TestRun_BindMode` | Verify bind mode creates NO workspace volume |
| `TestRun_SnapshotMode` | Verify snapshot mode creates workspace volume |
| `TestRun_ContainerLabels` | Verify correct `com.clawker.*` labels |
| `TestRun_ExitCode` | Verify exit code propagation |

#### Start Command

| Test | Purpose |
|------|---------|
| `TestStart_CreatesContainer` | Verify first start creates container + volumes |
| `TestStart_Idempotent` | Verify second start reuses existing container |
| `TestStart_CleanFlag` | Verify `--clean` removes existing before start |
| `TestStart_BindMode` | Verify bind mode creates no workspace volume |
| `TestStart_SnapshotMode` | Verify snapshot mode creates workspace volume |
| `TestStart_ContainerLabels` | Verify correct `com.clawker.*` labels |
| `TestStart_DetachFlag` | Verify container runs in background |
| `TestStart_PortPublish` | Verify port binding created |

#### Stop Command

| Test | Purpose |
|------|---------|
| `TestStop_StopsContainer` | Verify container stopped and removed |
| `TestStop_PreservesVolumes` | Verify volumes preserved by default |
| `TestStop_CleanFlag` | Verify `--clean` removes volumes + image |
| `TestStop_SpecificAgent` | Verify only stops named agent |
| `TestStop_ForceFlag` | Verify force kills container |
| `TestStop_AlreadyStopped` | Verify handles stopped container gracefully |

#### Remove Command

| Test | Purpose |
|------|---------|
| `TestRm_ByName` | Verify removes specific container by name |
| `TestRm_ByProject` | Verify removes all project containers |
| `TestRm_RemovesVolumes` | Verify associated volumes deleted |
| `TestRm_ForceRunning` | Verify force removes running container |
| `TestRm_RunningWithoutForce` | Verify gracefully stops then removes running container |
| `TestRm_NonExistent` | Verify graceful error for missing container |

#### Prune Command

| Test | Purpose |
|------|---------|
| `TestPrune_RemovesStoppedContainers` | Verify stopped containers are removed |
| `TestPrune_SkipsRunningContainers` | Verify running containers are NOT removed |
| `TestPrune_DefaultNoVolumes` | Verify volumes are NOT removed without `--all` |
| `TestPrune_AllRemovesVolumes` | Verify `--all` removes volumes |
| `TestPrune_AllRemovesContainers` | Verify `--all` removes stopped containers |
| `TestPrune_NoResources` | Verify graceful handling when nothing to prune |
| `TestPrune_ForceSkipsPrompt` | Verify `--force` skips confirmation |

### Test Infrastructure

Tests use build tag `//go:build integration` to separate from unit tests:

- **TestMain**: Skips if Docker unavailable, builds binary and test image once
- **uniqueAgent()**: Generates unique agent names to prevent collision
- **containerExists()/volumeExists()**: Docker state verification helpers
- **cleanup()**: Removes test containers and volumes
- **runClawker()**: Executes clawker with `--workdir` pointing to testdata

### Adding New Integration Tests

1. Add test function with `TestRun_` prefix
2. Generate unique agent name with `uniqueAgent(t)`
3. Defer `cleanup(containerName)` for cleanup on pass/fail
4. Run command with `runClawker(args...)`
5. Verify Docker state with `containerExists()`/`volumeExists()`
6. Use `docker inspect` for label verification

---

## Manual CLI Test Patterns

For exploratory testing or debugging, use these manual patterns.

### Test Structure

Each manual CLI test follows this pattern:

1. **Setup** - Note initial state (containers, volumes, files)
2. **Execute** - Run the command with specific flags
3. **Verify** - Check expected outcomes
4. **Cleanup** - Remove test artifacts

Use unique agent names (e.g., `test-<feature>-<variant>`) to isolate tests.

---

## `clawker run` Tests

### Test: Default Cleanup (Container + Volumes Removed)

**Purpose**: Verify that `clawker run` removes both container AND volumes on exit by default.

```bash
# Setup: Note existing volumes
docker volume ls | grep clawker

# Execute: Run with unique agent name
./bin/clawker run --agent test-cleanup -- ls /workspace

# Verify: Container removed
docker ps -a | grep test-cleanup
# Expected: No output (container removed)

# Verify: Volumes removed
docker volume ls | grep test-cleanup
# Expected: No output (volumes removed)
```

**Pass criteria**:

- Container does not exist after exit
- No volumes with agent name exist after exit

---

### Test: --keep Flag (Container + Volumes Preserved)

**Purpose**: Verify that `--keep` preserves both container AND volumes.

```bash
# Execute: Run with --keep flag
./bin/clawker run --keep --agent test-keep -- ls /workspace

# Verify: Container preserved (exited state)
docker ps -a | grep test-keep
# Expected: Shows container with "Exited (0)" status

# Verify: Volumes preserved
docker volume ls | grep test-keep
# Expected: Shows clawker.project.test-keep-config and clawker.project.test-keep-history

# Cleanup
docker rm -f clawker.clawker.test-keep
docker volume rm clawker.clawker.test-keep-config clawker.clawker.test-keep-history
```

**Pass criteria**:

- Container exists with Exited status
- Config and history volumes exist
- (Workspace volume exists only in snapshot mode)

---

### Test: Workspace Modes

**Purpose**: Verify volume behavior differs between bind and snapshot modes.

```bash
# Bind mode (default) - no workspace volume created
./bin/clawker run --mode=bind --agent test-bind -- ls /workspace
docker volume ls | grep test-bind
# Expected: Only config/history volumes during run, none after exit

# Snapshot mode - workspace volume created
./bin/clawker run --mode=snapshot --agent test-snap -- ls /workspace
docker volume ls | grep test-snap
# Expected: workspace/config/history volumes during run, none after exit
```

---

## Common Verification Commands

### Check Container State

```bash
# List all clawker containers
docker ps -a --filter "label=com.clawker.managed=true"

# Check specific agent
docker ps -a | grep "clawker.project.agent"

# Inspect container labels
docker inspect <container> --format '{{json .Config.Labels}}' | jq .
```

### Check Volume State

```bash
# List all clawker volumes
docker volume ls | grep clawker

# Check specific agent's volumes
docker volume ls | grep "clawker.project.agent"

# Inspect volume labels (may be null for auto-created volumes)
docker volume inspect <volume> --format '{{json .Labels}}' | jq .
```

### Cleanup Commands

```bash
# Remove specific container
docker rm -f clawker.project.agent

# Remove specific volumes
docker volume rm clawker.project.agent-workspace
docker volume rm clawker.project.agent-config
docker volume rm clawker.project.agent-history

# Remove all test artifacts for an agent
docker rm -f clawker.project.agent 2>/dev/null
docker volume rm clawker.project.agent-{workspace,config,history} 2>/dev/null
```

---

## Volume Naming Convention

Volumes follow the pattern: `clawker.{project}.{agent}-{purpose}`

| Purpose | Path in Container | Created When |
|---------|-------------------|--------------|
| `workspace` | `/workspace` | Snapshot mode only |
| `config` | `/home/claude/.claude` | Always |
| `history` | `/commandhistory` | Always |

All volumes (config, history, workspace) are created explicitly with `com.clawker.*` labels, enabling proper cleanup via label-based filtering in `RemoveContainerWithVolumes()`.

---

## Test Isolation Tips

1. **Use unique agent names** - Prevents collision with existing containers/volumes
2. **Check state before AND after** - Confirms the test caused the change
3. **Clean up regardless of pass/fail** - Use cleanup commands even if test fails
4. **Use timeout for hanging commands** - `timeout 60 ./bin/clawker run ...`
5. **Capture exit codes** - `echo "Exit: $?"` after commands

---

## Adding New Test Patterns

When documenting new manual tests, include:

1. **Test name** - Short descriptive name
2. **Purpose** - What behavior is being verified
3. **Setup commands** - Initial state checks
4. **Execute commands** - The actual clawker command(s)
5. **Verify commands** - How to check the outcome
6. **Expected output** - What success looks like
7. **Pass criteria** - Explicit conditions for pass/fail
8. **Cleanup commands** - How to reset state
