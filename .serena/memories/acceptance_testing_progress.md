# Acceptance Testing Implementation Progress

## Current Status: Phase 7 Complete (Container Options Adversarial Tests)

The acceptance testing infrastructure is being implemented following a plan created off of `.claude/prds/acceptance-testing/`. The work is divided into phases, with each phase focusing on specific areas of functionality.
Initial planning was performed on a different computer and separate file system. so if you need to create your own plan file, refer to the documents in `.claude/prds/acceptance-testing/`.

## Completed Work

### Phase 0-2: Infrastructure (Complete)
- `acceptance/acceptance_test.go` - Main test harness (~700 lines)
- Custom testscript commands: `defer`, `wait_container_running`, `wait_container_exit`, `replace`, `sleep`, `stdout2env`, `env2upper`, `container_id`, `container_state`, `cleanup_project`
- Environment injection: `PROJECT`, `RANDOM_STRING`, `SCRIPT_NAME`, `CLAWKER_SPINNER_DISABLED`
- Deferred cleanup system with LIFO execution

### Phase 3: Container Tests (Complete - 14 tests)
Located in `acceptance/testdata/container/`:
- `run-basic.txtar` - Basic container run with echo
- `run-detach.txtar` - Detached container with logs
- `run-autoremove.txtar` - Auto-remove flag behavior
- `start-stop.txtar` - Container lifecycle
- `exec-basic.txtar` - Exec into container
- `logs-follow.txtar` - Log streaming
- `list-format.txtar` - List with format flag
- `inspect.txtar` - Container inspection
- `cp.txtar` - File copy (fixed: destination must be directory for tar-based copy)
- `rename.txtar` - Container rename (note: Docker doesn't allow label updates)
- `pause-unpause.txtar` - Pause/unpause
- `kill.txtar` - Kill command
- `wait.txtar` - Wait command
- `create.txtar` - Container create

**Removed:** `prune.txtar` - `clawker container prune` command doesn't exist

### Phase 4: Resource Management Tests (Complete - 10 tests)

**Volume tests** (`acceptance/testdata/volume/`):
- `create-remove.txtar` - Volume lifecycle
- `list.txtar` - Volume listing
- `inspect.txtar` - Volume inspection

**Removed:** `prune.txtar` - Interferes with parallel tests (prunes all managed volumes)

**Network tests** (`acceptance/testdata/network/`):
- `create-remove.txtar` - Network lifecycle (note: create outputs ID not name)
- `list.txtar` - Network listing
- `inspect.txtar` - Network inspection

**Removed:** `prune.txtar` - Same issue as volume prune

**Image tests** (`acceptance/testdata/image/`):
- `list.txtar` - Image listing (builds image first)
- `inspect.txtar` - Image inspection
- `build-basic.txtar` - Basic image build with tags
- `prune.txtar` - Image prune with --all flag

## Key Fixes Applied

1. **cp.txtar**: Docker SDK's `CopyToContainer` requires directory path as destination when using tar archives. Changed `/tmp/testfile.txt` to `/tmp/`.

2. **rename.txtar**: Docker doesn't allow label updates after container creation. Removed impossible assertion that the old agent name wouldn't appear in listing (the AGENT label retains original value).

3. **Volume/Network prune tests removed**: These prune ALL managed resources, interfering with parallel tests. Prune is inherently destructive and doesn't work well in parallel test environments.

4. **Output assertions**: Many clawker commands output to stderr not stdout:
   - `volume rm` → stderr
   - `volume prune` → stderr
   - `network rm` → stderr
   - Use `stderr` assertions instead of `stdout`

5. **Network create**: Returns network ID (hash), not network name. Don't assert on stdout after create.

6. **build.txtar stderr assertion**: The `clawker build` command outputs `building container image` (lowercase) not `Building image`. Always check actual command output when writing assertions.

7. **Ralph status output**: `clawker ralph status --json` outputs `{"exists": false}` (with space after colon) when no session exists. The `--quiet` flag is NOT supported by ralph status - it outputs human-readable text to stderr by default.

8. **Project init with --yes**: Requires `mkdir $HOME/.local/clawker` first because --yes mode expects settings directory to exist.

9. **Firewall disabled for tests**: All acceptance tests use `security.firewall.enable: false` to avoid NET_ADMIN capability requirements and simplify test setup.

## Running Tests

```bash
# All acceptance tests
go test -tags=acceptance ./acceptance -v -timeout 15m

# Specific category
go test -tags=acceptance -run ^TestContainer$ ./acceptance -v
go test -tags=acceptance -run ^TestVolume$ ./acceptance -v
go test -tags=acceptance -run ^TestNetwork$ ./acceptance -v
go test -tags=acceptance -run ^TestImage$ ./acceptance -v -timeout 10m

# Single test script
CLAWKER_ACCEPTANCE_SCRIPT=run-basic.txtar go test -tags=acceptance -run ^TestContainer$ ./acceptance -v
```

### Phase 5: Ralph, Project, and Root Tests (Complete - 7 tests)

**Ralph tests** (`acceptance/testdata/ralph/`):
- `status-no-session.txtar` - Status when no session exists
- `status-json.txtar` - JSON output format (`{"exists": false}`)
- `reset.txtar` - Reset command with --quiet and --all flags

**Project tests** (`acceptance/testdata/project/`):
- `init-basic.txtar` - Non-interactive project init with --yes
- `init-force.txtar` - Force overwrite with --force flag

**Root tests** (`acceptance/testdata/root/`):
- `init.txtar` - User-level init creating settings.yaml
- `build.txtar` - Image build command (note: output is `building container image` lowercase)

### Phase 6: Documentation and CI (Complete)

**Created:**
- `acceptance/README.md` - Comprehensive test authoring guide (~300 lines)
  - Directory structure, test file format (.txtar)
  - All custom commands documented (defer, replace, wait_*, etc.)
  - Environment variables table
  - Running tests (all, by category, single script)
  - Debugging tips (preserve work dir, skip defer)
  - CI configuration examples

**Modified:**
- `.claude/rules/TESTING.md` - Added acceptance tests section with running commands
- `CLAUDE.md` - Added acceptance test commands to Build Commands and Testing Requirements
- `Makefile` - Added `acceptance` target with `$(TEST_CMD_VERBOSE) -tags=acceptance -timeout 15m ./acceptance`

## Test Counts Summary

| Category | Tests | Status |
|----------|-------|--------|
| Container | 55 (opts) + 14 (lifecycle) = 69 | Run tests to verify |
| Volume | 3 | ✅ All Pass |
| Network | 3 | ✅ All Pass |
| Image | 4 | ✅ All Pass |
| Ralph | 3 | ✅ All Pass |
| Project | 2 | ✅ All Pass |
| Root | 2 | ✅ All Pass |
| **Total** | **72** | **48 Pass, 4 Fail (adversarial), 20 new (untested)** |

### Phase 7: Container Options Tests (35 existing + 20 new = 55 total opts-* tests)

New tests in `acceptance/testdata/container/opts-*.txtar` covering all container option flags from the PRD.

**Phase 7b: Additional Flag Coverage (20 new test files)**

| Test File | Flags Tested |
|-----------|-------------|
| `opts-attach.txtar` | `--attach` / `-a` |
| `opts-stop-timeout.txtar` | `--stop-timeout` |
| `opts-tty-stdin.txtar` | `--tty` / `-t`, `--interactive` / `-i` |
| `opts-security-opt.txtar` | `--security-opt` |
| `opts-userns-cgroupns.txtar` | `--cgroupns` |
| `opts-dns-search.txtar` | `--dns-search` |
| `opts-network-config.txtar` | `--network`, `--mac-address` |
| `opts-network-ip-alias.txtar` | `--ip`, `--network-alias` |
| `opts-volumes.txtar` | `--volume` / `-v` |
| `opts-volumes-from.txtar` | `--volumes-from` |
| `opts-cidfile.txtar` | `--cidfile` |
| `opts-cpu-scheduling.txtar` | `--cpu-shares`, `--cpu-period`, `--cpu-quota`, `--cpuset-mems` |
| `opts-blkio.txtar` | `--blkio-weight` |
| `opts-blkio-validation.txtar` | `--blkio-weight` range validation |
| `opts-memory-swappiness.txtar` | `--memory-swappiness` |
| `opts-memory-swappiness-validation.txtar` | `--memory-swappiness` range validation |
| `opts-uts.txtar` | `--uts` |
| `opts-isolation.txtar` | `--isolation` |
| `opts-storage-opt.txtar` | `--storage-opt` |
| `opts-link.txtar` | `--link` |

**Deliberately Skipped (not testable in CI)**:
- Windows-only: `--cpu-count`, `--cpu-percent`, `--io-maxbandwidth`, `--io-maxiops`
- Hardware-dependent: `--gpus`, `--cpu-rt-period`, `--cpu-rt-runtime`
- Device-path-dependent: `--blkio-weight-device`, `--device-read-bps`, `--device-write-bps`, `--device-read-iops`, `--device-write-iops`
- IPv6-dependent: `--ip6`
- Complex setup: `--link-local-ip`

## Files Modified/Created

### New Files
- `acceptance/acceptance_test.go`
- `acceptance/README.md` - Test authoring guide
- `acceptance/testdata/container/*.txtar` (14 files)
- `acceptance/testdata/volume/*.txtar` (3 files)
- `acceptance/testdata/network/*.txtar` (3 files)
- `acceptance/testdata/image/*.txtar` (4 files)
- `acceptance/testdata/ralph/*.txtar` (3 files)
- `acceptance/testdata/project/*.txtar` (2 files)
- `acceptance/testdata/root/*.txtar` (2 files)

### Modified Files
- `internal/cmd/container/cp/cp.go` - Added HandleError for better error messages
- `.claude/rules/TESTING.md` - Added acceptance tests section
- `CLAUDE.md` - Added acceptance test commands
- `Makefile` - Added `acceptance` target

### Staged Documentation (from plan mode)
- `.claude/prds/acceptance-testing/*.md` - Various PRD and analysis docs

## Tips for Writing New Acceptance Tests

### Before Writing a Test

1. **Check command implementation** - Read the actual command code to understand:
   - What outputs to stdout vs stderr
   - Exact message wording (case-sensitive!)
   - Required flags and their defaults

2. **Look at existing similar tests** - Follow established patterns in `acceptance/testdata/`

3. **Test interactively first** - Run the command manually to see actual output

### Common Patterns

```txtar
# Standard setup for most tests
replace clawker.yaml PROJECT=$PROJECT
mkdir $HOME/.local/clawker

# Standard config with firewall disabled
-- clawker.yaml --
version: "1"
project: "$PROJECT"

build:
  image: "alpine:latest"

security:
  firewall:
    enable: false
```

### Output Channel Reference

| Command | stdout | stderr |
|---------|--------|--------|
| `container ls` | Data | - |
| `container run/create` | Container ID | Status |
| `volume/network rm` | - | Status |
| `build` | - | Progress |
| `ralph status` | JSON (with --json) | Human-readable |
| `ralph status --json` | JSON object | - |
| `ralph reset` | - | Status |
| `project init` | - | Status |
| `init` | - | Status |

### Debugging Failed Tests

```bash
# Preserve work directory for inspection
CLAWKER_ACCEPTANCE_PRESERVE_WORK_DIR=true go test -tags=acceptance -run ^TestRalph$ ./acceptance -v

# Skip cleanup to inspect state
CLAWKER_ACCEPTANCE_SKIP_DEFER=true go test -tags=acceptance -run ^TestContainer$ ./acceptance -v

# Run single script with verbose output
CLAWKER_ACCEPTANCE_SCRIPT=build.txtar go test -tags=acceptance -run ^TestRoot$ ./acceptance -v
```
