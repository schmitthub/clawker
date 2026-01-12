# Manual CLI Test Patterns

> **LLM Memory Document**: Reference this document ONLY when writing CLI command tests. Contains manual test patterns for verifying claucker CLI behavior.

## Test Structure

Each CLI command test follows this pattern:

1. **Setup** - Note initial state (containers, volumes, files)
2. **Execute** - Run the command with specific flags
3. **Verify** - Check expected outcomes
4. **Cleanup** - Remove test artifacts

Use unique agent names (e.g., `test-<feature>-<variant>`) to isolate tests.

---

## `claucker run` Tests

### Test: Default Cleanup (Container + Volumes Removed)

**Purpose**: Verify that `claucker run` removes both container AND volumes on exit by default.

```bash
# Setup: Note existing volumes
docker volume ls | grep claucker

# Execute: Run with unique agent name
./bin/claucker run --agent test-cleanup -- ls /workspace

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
./bin/claucker run --keep --agent test-keep -- ls /workspace

# Verify: Container preserved (exited state)
docker ps -a | grep test-keep
# Expected: Shows container with "Exited (0)" status

# Verify: Volumes preserved
docker volume ls | grep test-keep
# Expected: Shows claucker.project.test-keep-config and claucker.project.test-keep-history

# Cleanup
docker rm -f claucker.claucker.test-keep
docker volume rm claucker.claucker.test-keep-config claucker.claucker.test-keep-history
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
./bin/claucker run --mode=bind --agent test-bind -- ls /workspace
docker volume ls | grep test-bind
# Expected: Only config/history volumes during run, none after exit

# Snapshot mode - workspace volume created
./bin/claucker run --mode=snapshot --agent test-snap -- ls /workspace
docker volume ls | grep test-snap
# Expected: workspace/config/history volumes during run, none after exit
```

---

## Common Verification Commands

### Check Container State
```bash
# List all claucker containers
docker ps -a --filter "label=com.claucker.managed=true"

# Check specific agent
docker ps -a | grep "claucker.project.agent"

# Inspect container labels
docker inspect <container> --format '{{json .Config.Labels}}' | jq .
```

### Check Volume State
```bash
# List all claucker volumes
docker volume ls | grep claucker

# Check specific agent's volumes
docker volume ls | grep "claucker.project.agent"

# Inspect volume labels (may be null for auto-created volumes)
docker volume inspect <volume> --format '{{json .Labels}}' | jq .
```

### Cleanup Commands
```bash
# Remove specific container
docker rm -f claucker.project.agent

# Remove specific volumes
docker volume rm claucker.project.agent-workspace
docker volume rm claucker.project.agent-config
docker volume rm claucker.project.agent-history

# Remove all test artifacts for an agent
docker rm -f claucker.project.agent 2>/dev/null
docker volume rm claucker.project.agent-{workspace,config,history} 2>/dev/null
```

---

## Volume Naming Convention

Volumes follow the pattern: `claucker.{project}.{agent}-{purpose}`

| Purpose | Path in Container | Created When |
|---------|-------------------|--------------|
| `workspace` | `/workspace` | Snapshot mode only |
| `config` | `/home/claude/.claude` | Always |
| `history` | `/commandhistory` | Always |

**Important**: Config and history volumes are created by Docker implicitly (no labels). The workspace volume is created explicitly with labels in snapshot mode.

---

## Test Isolation Tips

1. **Use unique agent names** - Prevents collision with existing containers/volumes
2. **Check state before AND after** - Confirms the test caused the change
3. **Clean up regardless of pass/fail** - Use cleanup commands even if test fails
4. **Use timeout for hanging commands** - `timeout 60 ./bin/claucker run ...`
5. **Capture exit codes** - `echo "Exit: $?"` after commands

---

## Adding New Test Patterns

When documenting new manual tests, include:

1. **Test name** - Short descriptive name
2. **Purpose** - What behavior is being verified
3. **Setup commands** - Initial state checks
4. **Execute commands** - The actual claucker command(s)
5. **Verify commands** - How to check the outcome
6. **Expected output** - What success looks like
7. **Pass criteria** - Explicit conditions for pass/fail
8. **Cleanup commands** - How to reset state
