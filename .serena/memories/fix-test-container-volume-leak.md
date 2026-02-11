# Fix: Excessive Container/Volume Creation in Command Integration Tests

## Problem Statement

Running `go test ./test/commands/... -run "TestContainer(Create|Start|Run)" -v -timeout 10m` creates **312+ containers and 1885+ volumes** from only ~20 test functions. The theoretical maximum should be ~52 containers and ~28 volumes. Something is causing massive resource amplification.

### Symptoms
- 312 containers created (with active cleanup running, so true total is higher)
- 1885 volumes created
- 283+ orphaned `commands.test host-proxy serve` daemon processes
- 10-minute test timeout exceeded → panic → defers never ran → cleanup didn't happen
- ALL busybox containers had `com.clawker.test=true` labels (0 unlabeled) — proving the CopyToVolume label fix works
- Volume:container ratio ~6:1 (expected ~0.5:1)

### Two Separate Issues
1. **Unnecessary resource creation** (PARTIALLY FIXED): `NewTestFactory` defaults triggered CopyToVolume + host proxy unnecessarily
2. **Unexplained amplification** (NOT FOUND): ~20 tests creating 6x-67x more resources than the code paths account for

## What Was Done

### Fix 1: CopyToVolume Label Injection (COMPLETE - previous session)
- `internal/docker/volume.go`: Switched all 6 `c.APIClient.*` calls to whail Engine methods
- Temp busybox containers now inherit managed labels + test labels (`com.clawker.test=true`)
- Cleanup defer uses `context.WithTimeout(context.Background(), 10*time.Second)` 
- `internal/docker/CLAUDE.md`: Updated documentation

### Fix 2: NewTestFactory Config Defaults (COMPLETE - this session)
- `test/harness/factory.go`: Changed `config.NewConfigForTest(nil, nil)` to `config.NewConfigForTest(testProject(), nil)`
- New `testProject()` function returns config with:
  - `ClaudeCode.Config.Strategy: "fresh"` (skips CopyToVolume — no temp busybox containers)
  - `ClaudeCode.UseHostAuth: false` (skips second CopyToVolume)
  - `Security.Firewall.Enable: false` (no firewall)
  - `Security.EnableHostProxy: false` (no host-proxy daemon processes)
- Unit tests pass (all `internal/...`, `pkg/...`, `cmd/...`)
- **Note**: `test/internals/containerfs_test.go` tests explicitly need copy mode — they set their own ClaudeCodeConfig directly and don't use NewTestFactory, so they're unaffected

### Fix 2 Reduces But Doesn't Explain Amplification
With copy mode disabled, each test should create:
- 1 main container (for run/create tests)
- 0 temp busybox containers (no CopyToVolume)
- 2 volumes (config + history)
- 0 host-proxy daemons

Total for ~14 init-triggering tests: ~14 containers + 28 volumes. NOT thousands.

## What Explore Agents Found

### Agent 1: Production Code Path Trace
- **No loops, no retries, no amplification** in any production code path
- `ContainerInitializer.Run()` → `runSteps()` is linear: 5 sequential steps, single goroutine
- `EnsureConfigVolumes` creates exactly 2 volumes per init (config + history), idempotent
- `CopyToVolume` creates exactly 1 temp container per call (max 2 per init)
- `EnsureNetwork` is idempotent (creates 1 network if not exists)
- `docker.NewClient()` creates 0 resources
- Host proxy spawns OS daemon process, NOT Docker containers
- Cobra PreRunE/PersistentPreRunE create 0 resources
- Image resolution creates 0 resources

### Agent 2: Test Infrastructure Trace
- `RunTestMain` creates 0 resources (cleanup coordinator only)
- `CleanupTestResources` is purely destructive (removes, never creates)
- `NewTestClient` / `NewRawDockerClient` create 0 resources
- Start command does NOT trigger container init (only starts existing containers)
- Wait functions (WaitForReadyFile, etc.) create 0 resources
- `TestMain` in test/commands/ just calls `harness.RunTestMain(m)`
- `CleanupProjectResources` is purely destructive
- Container removal does not trigger creation

### Agent 3: Call Site Enumeration
- Found ALL `VolumeCreate`, `ContainerCreate`, `EnsureVolume`, `CopyToVolume`, `EnsureConfigVolumes` call sites
- NONE are inside loops in production code
- All call counts match theoretical analysis (1 container create per init, 2 volumes per init)
- No goroutine pools, no retry mechanisms, no reconnection logic

### Summary: Root Cause NOT Found
All three agents confirmed the production code is clean — linear execution, no amplification mechanisms. The theoretical max from code analysis is ~52 containers and ~28 volumes. The actual 312/1885 remains unexplained.

## Test Functions in test/commands/

Files: `container_create_test.go`, `container_run_test.go`, `container_start_test.go`, `container_exec_test.go`, `worktree_test.go`, `main_test.go`

**Tests matching `-run "TestContainer(Create|Start|Run)"`:**
- 3 create tests (all trigger init)
- 9 run tests (8 top-level + 3 subtests in ArbitraryCommand, all trigger init)  
- 7 start tests (+ subtests, do NOT trigger init — use rawClient.ContainerCreate)
- Total: ~14 inits, ~10 raw containers from start tests

## Remaining TODO

- [x] **1. Verify resource counts**: Integration test leak audit COMPLETE — 27/28 test/commands pass with 0 leaks, 0 unlabeled resources, 0 dangling images. See `integration-test-leak-audit` memory for full results.
- [x] **2. Add test-name labels to all Docker resources** (DONE — previous session)
- [x] **3. Update documentation** (DONE — previous session)
- [x] **4. Root cause fixes** (DONE — this session):
  - `NewTestFactory` now wires `h.Config` with `applyTestDefaults()` (replaces hardcoded `testProject()`)
  - `whail.VolumeExists` delegates to `IsVolumeManaged` (unmanaged volumes treated as "not found")
  - `whail.NetworkExists` delegates to `IsNetworkManaged` (same pattern)
  - `CopyToVolume` uses configurable `ChownImage` field + `imageExistsRaw()` (no managed label check for external images)
  - `test/harness.BuildTestChownImage` builds labeled busybox via `docker.Client` (through whail)
  - `test/harness.BuildSimpleTestImage` builds via `docker.Client` (through whail) — NOT raw moby SDK
  - Removed `f.Client` overrides from `container_create_test.go` (factory now provides correct config)
- [x] **5. Update documentation** (DONE — this session): test/CLAUDE.md, internal/docker/CLAUDE.md, this memory

### Fix 5: BuildLightImage — Raw SDK → Whail (COMPLETE - leak audit session)
- `test/harness/client.go:BuildLightImage`: Changed from `rawClient.ImageBuild` (raw Docker SDK) to `dc.BuildImage` (whail-wrapped)
- Root cause: Multi-stage Dockerfile creates Go builder intermediates as dangling unlabeled images
- Fix: whail auto-injects `com.clawker.managed=true` + `com.clawker.test=true` + `com.clawker.test.name` labels
- Added `rawClient.ImagePrune` with `dangling=true` filter to clean up multi-stage intermediates
- Verified: Mid-test inspect confirms all labels, 0 dangling images post-build

## Key Files

| File | Status | Purpose |
|------|--------|---------|
| `internal/docker/volume.go` | DONE | CopyToVolume uses whail methods + configurable ChownImage + imageExistsRaw |
| `internal/docker/client.go` | DONE | ChownImage field, chownImage() accessor, imageExistsRaw() |
| `internal/docker/CLAUDE.md` | DONE | Documents ChownImage, imageExistsRaw, CopyToVolume config |
| `pkg/whail/volume.go` | DONE | VolumeExists delegates to IsVolumeManaged |
| `pkg/whail/network.go` | DONE | NetworkExists delegates to IsNetworkManaged |
| `pkg/whail/CLAUDE.md` | DONE | Documents VolumeExists/NetworkExists managed enforcement |
| `test/harness/factory.go` | DONE | Wires h.Config + applyTestDefaults(), sets ChownImage |
| `test/harness/docker.go` | DONE | BuildTestChownImage + BuildSimpleTestImage via docker.Client (whail) |
| `test/commands/container_create_test.go` | DONE | Removed f.Client overrides |
| `test/harness/client.go` | DONE | BuildLightImage: raw SDK → whail + dangling prune |

### Fix 6: Remove NewRawDockerClient from Command Tests (COMPLETE - rawClient removal session)
- All command tests (`container_exec_test.go`, `container_start_test.go`, `container_run_test.go`) now use `NewTestClient(t)` (`*docker.Client`) exclusively
- Agent tests (`ralph_test.go`, `run_test.go`) converted to `*docker.Client`
- Internal tests (`workspace_test.go`) converted
- Harness functions (`docker.go`, `ready.go`) now take `*docker.Client` instead of `client.APIClient`
- `AddClawkerLabels` signature updated to accept `testName string` parameter
- `NewRawDockerClient` kept but marked deprecated (still used by `BuildLightImage` for dangling prune and `RunTestMain` for cleanup)
- All 3549 unit tests pass, `go build ./test/...` compiles
- Plan file: `/Users/andrew/.claude/plans/frolicking-tinkering-pearl.md`

## Plan File Reference
Claude Code plan file: `/Users/andrew/.claude/plans/dreamy-tinkering-hamster.md`

## Important Reminder
**ALWAYS check with the user before proceeding with the next TODO item.** The user is frustrated with agents that investigate endlessly without fixing things — prioritize concrete action. If all work is done, ask the user if they want to delete this memory.
