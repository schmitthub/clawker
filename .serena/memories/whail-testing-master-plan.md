> **Artifact of [testing-initiative-master-plan.md](testing-initiative-master-plan.md)** — Phases complete. Retained as reference for Phase 4+ migration.

# Master Plan: Whail Jail Testing Infrastructure

Adapted from Docker CLI's function-field mock pattern. Whail-only scope — clawker consumer tests deferred to future PR.

**Branch:** `a/testing`
**Plan location:** `/Users/andrew/.claude/plans/cryptic-weaving-plum.md`

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: FakeAPIClient + whailtest package | `complete` | — |
| Task 2: Label helper tests | `complete` | — |
| Task 3: Unit jail tests — REJECT | `complete` | — |
| Task 4: Unit jail tests — INJECT_LABELS + INJECT_FILTER + Override Prevention | `complete` | — |
| Task 5: Integration test gaps | `complete` | — |

**Key Learnings** (agents append here as they complete tasks):

**Task 2:**
- `labels_test.go` already existed with 8 test functions covering MergeLabels, LabelConfig resource methods, LabelFilter, LabelFilterMultiple, and AddLabelFilter.
- Added 3 missing tests: `TestMergeLabelFilters`, `TestLabelConfig_Precedence`, `TestLabels_Merge`.
- `client.Filters` is `map[string]map[string]bool` — label entries are stored as `"key=value": true` under the `"label"` key.
- `Filters.Add()` returns a new Filters (immutable pattern) — original is not modified, must capture return value.

**Task 3:**
- `jail_test.go` must use `package whail_test` (external test package), NOT `package whail` — the plan said `package whail` but that causes an import cycle because `whailtest` imports `whail`.
- 29 REJECT methods tested (plan said 28 — miscount): 21 container + 2 volume + 4 network + 2 image.
- For "inspectSelf" methods (ContainerInspect, VolumeInspect, NetworkInspect, ImageInspect), the managed check and the operation both call the same moby method. Assert `CalledN(1)` instead of `NotCalled`.
- ContainerWait returns error via channel (no error return value). Read from `result.Error` channel — it's buffered so it returns immediately when unmanaged.
- ContainerStatsOneShot delegates to `APIClient.ContainerStats`, so the dangerous method is "ContainerStats" not "ContainerStatsOneShot".
- ImageInspect on the fake has variadic options: `func(...client.ImageInspectOption)`.

**Task 1:**
- `NewFromExisting` was updated to accept variadic `EngineOptions` (`NewFromExisting(c client.APIClient, opts ...EngineOptions) *Engine`). Existing callers (single arg) still work. It now applies defaults and computes managedLabelKey/Value like `NewWithOptions` does.
- The moby `client.APIClient` interface has unexported methods, so `FakeAPIClient` embeds a nil `*client.Client` to satisfy them. Unoverridden methods panic on nil deref (fail-loud).
- The moby v0.2.1 API uses a uniform `Method(ctx, id, MethodOptions) (MethodResult, error)` pattern. `ContainerWait` is the only exception: returns `ContainerWaitResult` (no error) containing Result/Error channels.
- `ImageInspect` uses variadic options: `ImageInspect(ctx, ref string, opts ...ImageInspectOption)`.
- Module is `github.com/schmitthub/clawker` (not anthropics).
- Container labels are at `InspectResponse.Config.Labels`, volume labels at `Volume.Labels`, network labels at `Network.Labels`, image labels at `InspectResponse.Config.Labels` (OCI ImageConfig).
- `whailtest` constants: `TestLabelPrefix = "com.whailtest"`, `TestManagedLabel = "managed"`. Full key: `com.whailtest.managed=true`.

**Task 5:**
- `ContainerInspectResult` wraps the response — access labels via `inspect.Container.Config.Labels`, not `inspect.Config.Labels`.
- `ContainerInspect` requires 3 args: `(ctx, containerID, client.ContainerInspectOptions{})`.
- `VolumeListAll` delegates to `VolumeList` — both return `client.VolumeListResult` with `.Items` field.
- `Labels` type is `[]map[string]string` — pass override as `Labels{{key: "false"}}`.
- Integration tests use `package whail` (internal test package) so `testEngine`, `testLabelPrefix`, etc. are accessible directly.

**Task 4:**
- `client.Filters` is indeed `map[string]map[string]bool` — direct map access works: `filters["label"]["com.whailtest.managed=true"]` returns `true` if present, `false` otherwise (nil-safe).
- Label override prevention was NOT implemented in production code — `containerLabels()`, `volumeLabels()`, etc. merge extras AFTER the managed base label, so extras can override `managed=false`. Fixed by adding `labels[e.managedLabelKey] = e.managedLabelValue` after the final label merge in ContainerCreate, VolumeCreate, NetworkCreate, and ImageBuild.
- For ContainerCreate and ImageBuild, caller-provided labels (`Config.Labels` / `options.Labels`) are merged LAST (highest precedence), so the fix must go after that final merge, not just in the `*Labels()` helpers.
- `ImageBuild` has no ExtraLabels parameter — the test passes `managed=false` via `options.Labels` instead.
- `FindContainerByName` spy needs to return a matching container (`Names: []string{"/test"}`) to avoid `ErrContainerNotFound`, though filter capture happens regardless.
- `VolumesPrune` spy is `VolumePruneFn` (not `VolumesPruneFn`) — the fake method is `VolumePrune`, matching the moby API method name.

---

## CRITICAL: Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Present the handoff prompt from the task's Wrap Up section to the user
5. Wait for the user to start a new conversation with the handoff prompt

This ensures each task gets a fresh context window. Each task is designed to be self-contained — the handoff prompt provides all the context the next agent needs.

---

## Context for All Agents

### What is whail?
`pkg/whail/` is a reusable Docker engine wrapper with label-based resource isolation ("the jail"). It wraps `moby/moby/client.APIClient` and enforces that all operations only affect resources bearing a managed label.

### Jail behavior categories
- **REJECT (28 methods):** Check `IsContainerManaged`/`IsVolumeManaged`/`IsNetworkManaged`/`isManagedImage` before forwarding to moby. Reject with typed error if unmanaged.
- **INJECT_LABELS (4 methods):** Add managed labels to create requests (ContainerCreate, VolumeCreate, NetworkCreate, ImageBuild).
- **INJECT_FILTER (12 methods):** Add managed label filter to list/prune queries.
- **PASSTHROUGH (9 methods):** No jail logic (accessors, existence checks, `IsManaged` itself).

### Key files
- `pkg/whail/engine.go` — Engine struct, `NewFromExisting(client.APIClient, EngineOptions)`
- `pkg/whail/container.go` — 25 container methods
- `pkg/whail/volume.go` — 8 volume methods
- `pkg/whail/network.go` — 10 network methods
- `pkg/whail/image.go` — 5 image methods
- `pkg/whail/copy.go` — 3 copy methods
- `pkg/whail/labels.go` — Label helpers, LabelConfig, MergeLabels
- `pkg/whail/errors.go` — DockerError type, 47 error constructors
- `pkg/whail/types.go` — 28 type aliases re-exporting moby types

### Design pattern: FakeAPIClient
Function-field struct (Docker CLI pattern). Each moby APIClient method has a corresponding `XxxFn` field. If the field is nil, the method panics (fail-loud). Default inspect methods return managed resources so whail's internal `IsManaged` checks pass transparently. Each method call appends to `Calls []string` for assertion.

### Design pattern: Input-spy
Test closures assigned to function fields inspect the arguments whail sends to moby — proving labels were injected, filters were applied, etc.

### Package layout
```
pkg/whail/whailtest/     ← NEW: test helpers (standard library pattern like net/http/httptest)
    fake_client.go       ← FakeAPIClient struct + method implementations
    helpers.go           ← ManagedContainerJSON, wait helpers, assertion helpers
    doc.go               ← package doc
pkg/whail/
    labels_test.go       ← NEW: label helper unit tests (no build tag)
    jail_test.go         ← NEW: unit jail tests (no build tag)
    container_test.go    ← MODIFY: add integration test gaps
    volume_test.go       ← MODIFY: add integration test gaps
```

### Rules
- Read `CLAUDE.md`, `.claude/rules/testing.md`, `pkg/whail/CLAUDE.md` for project conventions
- Use Serena tools for code exploration (never read full files unnecessarily)
- `jail_test.go` uses `package whail` (not `whail_test`) — allows importing `whailtest` without circular deps
- `labels_test.go` can use either `package whail` or `package whail_test`
- Integration tests use `//go:build integration` build tag
- Never import `moby/moby/client` outside `pkg/whail/` — but `whailtest/` is inside `pkg/whail/` so it can
- Use `whail.NewFromExisting(fake, opts)` to create engines for unit tests — check the actual signature in `engine.go`
- All new code must compile and tests must pass: `go test ./pkg/whail/... -count=1`

---

## Task 1: FakeAPIClient + whailtest Package

**Creates:** `pkg/whail/whailtest/fake_client.go`, `helpers.go`, `doc.go`

### Implementation Phase

1. Read `pkg/whail/engine.go` to understand `NewFromExisting` signature and `EngineOptions`
2. Read moby's `client.APIClient` interface to get exact method signatures (find it via whail's imports)
3. Create `pkg/whail/whailtest/doc.go` with package documentation
4. Create `pkg/whail/whailtest/fake_client.go`:
   - `FakeAPIClient` struct with function fields for every moby `APIClient` method whail uses
   - Embed a panic base (unexported struct where every method panics with "not implemented: MethodName")
   - Each method: if `XxxFn != nil`, append method name to `Calls`, call `XxxFn`; else delegate to panic base
   - `NewFakeAPIClient()` constructor with sensible defaults:
     - `ContainerInspectFn` → returns managed container (so IsContainerManaged passes)
     - `VolumeInspectFn` → returns managed volume
     - `NetworkInspectFn` → returns managed network
     - `ImageInspectWithRawFn` → returns managed image
   - `Reset()` method to clear `Calls` slice
5. Create `pkg/whail/whailtest/helpers.go`:
   - `ManagedContainerJSON(id string, opts whail.EngineOptions)` — ContainerJSON with managed labels
   - `UnmanagedContainerJSON(id string)` — ContainerJSON without managed labels
   - `ManagedVolume(name string, opts whail.EngineOptions)` — volume.Volume with managed labels
   - `UnmanagedVolume(name string)` — volume.Volume without managed labels
   - `ManagedNetwork(name string, opts whail.EngineOptions)` — network resource with managed labels
   - `UnmanagedNetwork(name string)` — network resource without managed labels
   - `FakeContainerWaitOK()` — buffered channels returning exit code 0
   - `FakeContainerWaitExit(code int64)` — buffered channels returning given exit code
   - `AssertCalled(t, fake, method)`, `AssertNotCalled(t, fake, method)`, `AssertCalledN(t, fake, method, n)`

### Acceptance Criteria
```bash
go build ./pkg/whail/whailtest/
go test ./pkg/whail/... -count=1
go test ./... 2>&1 | tail -5
```

### Wrap Up
1. Update Progress Tracker: Task 1 → `complete`
2. Append key learnings (exact `NewFromExisting` signature, any surprises about moby interface, etc.)
3. **STOP.** Do not proceed to Task 2. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the whail testing initiative. Read the Serena memory `whail-testing-master-plan` — Task 1 is complete. Begin Task 2: Label helper tests."

---

## Task 2: Label Helper Tests

**Creates:** `pkg/whail/labels_test.go`
**Depends on:** Task 1 complete (whailtest package exists, but this task doesn't use it — pure function tests)

### Tests

| Test | What it proves |
|------|----------------|
| `TestMergeLabels_Empty` | No inputs → empty map (not nil) |
| `TestMergeLabels_Single` | Single map returned as-is |
| `TestMergeLabels_Precedence` | Later maps override earlier keys |
| `TestMergeLabels_NilHandling` | Nil maps in variadic are skipped |
| `TestLabelFilter` | Returns Filters with correct `label` key containing `key=value` |
| `TestLabelFilterMultiple` | Multiple labels → single Filters with all `label` entries |
| `TestAddLabelFilter` | Appends new label to existing Filters |
| `TestMergeLabelFilters` | Merges label map into existing Filters |
| `TestLabelConfig_ContainerLabels` | Default + container-specific + extra labels merged correctly |
| `TestLabelConfig_VolumeLabels` | Default + volume-specific + extra labels merged |
| `TestLabelConfig_NetworkLabels` | Default + network-specific + extra labels merged |
| `TestLabelConfig_ImageLabels` | Default + image-specific + extra labels merged |
| `TestLabelConfig_Precedence` | Extra labels override config-level labels |
| `TestLabels_Merge` | Labels slice merges maps in order |

### Acceptance Criteria
```bash
go test ./pkg/whail/ -v -run "TestMergeLabels|TestLabelFilter|TestLabelConfig|TestLabels_Merge" -count=1
go test ./pkg/whail/... -count=1
```

### Wrap Up
1. Update Progress Tracker: Task 2 → `complete`
2. Append key learnings (how Filters work internally, any edge cases discovered)
3. **STOP.** Do not proceed to Task 3. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the whail testing initiative. Read the Serena memory `whail-testing-master-plan` — Tasks 1-2 are complete. Begin Task 3: Unit jail tests — REJECT table-driven test."

---

## Task 3: Unit Jail Tests — REJECT (Table-Driven)

**Creates:** `pkg/whail/jail_test.go` (partial — REJECT tests only)
**Depends on:** Task 1 complete (needs `whailtest.FakeAPIClient`)

### `TestJail_RejectsUnmanaged` — single table-driven test, 28 entries

Each entry:
- Name (method name)
- `setup` func that configures FakeAPIClient inspect to return unmanaged resource
- `call` func that invokes the Engine method
- `dangerous` string — the moby method name that must NOT appear in `Calls`
- `errType` — expected error type string for assertion

| Resource | Methods |
|----------|---------|
| Container (21) | Stop, Remove, Kill, Pause, Unpause, Restart, Rename, Resize, Attach, Wait, Logs, Top, Stats, StatsOneShot, Update, Inspect, Start, ExecCreate, CopyToContainer, CopyFromContainer, ContainerStatPath |
| Volume (2) | Remove, Inspect |
| Network (4) | Remove, Inspect, Connect, Disconnect |
| Image (2) | Remove, Inspect |

### Acceptance Criteria
```bash
go test ./pkg/whail/ -v -run TestJail_RejectsUnmanaged -count=1
go test ./pkg/whail/... -count=1
```

### Wrap Up
1. Update Progress Tracker: Task 3 → `complete`
2. Append key learnings (any methods with surprising signatures, ContainerWait channel handling, etc.)
3. **STOP.** Do not proceed to Task 4. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the whail testing initiative. Read the Serena memory `whail-testing-master-plan` — Tasks 1-3 are complete. Begin Task 4: Unit jail tests — INJECT_LABELS, INJECT_FILTER, and Label Override Prevention. Add these to the existing `pkg/whail/jail_test.go`."

---

## Task 4: Unit Jail Tests — INJECT_LABELS + INJECT_FILTER + Override Prevention

**Modifies:** `pkg/whail/jail_test.go` (adds to file created in Task 3)
**Depends on:** Task 3 complete

### `TestJail_InjectsLabels` — 4 input-spy tests

| Subtest | Input-spy target |
|---------|------------------|
| ContainerCreate | `ContainerCreateFn` closure asserts `config.Labels` contains managed label key=value |
| VolumeCreate | `VolumeCreateFn` closure asserts labels contain managed label |
| NetworkCreate | `NetworkCreateFn` closure asserts options labels contain managed label |
| ImageBuild | `ImageBuildFn` closure asserts build options labels contain managed label |

### `TestJail_InjectsFilter` — table-driven, 12 entries

| Method | Fn field to spy on |
|--------|-------------------|
| ContainerList | ContainerListFn |
| ContainerListAll | ContainerListFn |
| ContainerListRunning | ContainerListFn |
| ContainerListByLabels | ContainerListFn |
| FindContainerByName | ContainerListFn |
| VolumeList | VolumeListFn |
| VolumeListAll | VolumeListFn |
| VolumesPrune | VolumesPruneFn |
| NetworkList | NetworkListFn |
| NetworksPrune | NetworksPruneFn |
| ImageList | ImageListFn |
| ImagesPrune | ImagesPruneFn |

### `TestJail_LabelOverridePrevention` — 4 tests

Each passes `managed=false` in extra labels and uses input-spy to assert the value reaching moby is still `managed=true`.

### Acceptance Criteria
```bash
go test ./pkg/whail/ -v -run TestJail -count=1
go test ./pkg/whail/... -count=1
```

### Wrap Up
1. Update Progress Tracker: Task 4 → `complete`
2. Append key learnings (filter structure details, label merge order, etc.)
3. **STOP.** Do not proceed to Task 5. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the whail testing initiative. Read the Serena memory `whail-testing-master-plan` — Tasks 1-4 are complete. Begin Task 5: Integration test gaps. These require Docker and modify existing test files."

---

## Task 5: Integration Test Gaps

**Modifies:** `pkg/whail/container_test.go`, `pkg/whail/volume_test.go`
**Depends on:** Tasks 1-4 complete
**Requires:** Docker daemon running

### New Tests

| Test | File | What it proves |
|------|------|----------------|
| `TestContainerResize_RejectsUnmanaged` | `container_test.go` | Resize on real unmanaged container returns not-managed error |
| `TestExecCreate_RejectsUnmanaged` | `container_test.go` | ExecCreate on real unmanaged container returns not-managed error |
| `TestContainerCreate_LabelOverridePrevention` | `container_test.go` | Create with `managed=false` in extra labels → inspect shows `managed=true` |
| `TestVolumeListAll_OnlyManaged` | `volume_test.go` | Create managed + unmanaged volumes → VolumeListAll only returns managed |

Follow existing patterns: `setupManagedContainer`/`setupUnmanagedContainer`, `cleanupManagedContainer`/`cleanupUnmanagedContainer`, `generateContainerName`, `//go:build integration` build tag.

### Acceptance Criteria
```bash
go test -tags=integration ./pkg/whail/ -v -run "TestContainerResize_RejectsUnmanaged|TestExecCreate_RejectsUnmanaged|TestContainerCreate_LabelOverridePrevention|TestVolumeListAll_OnlyManaged" -count=1 -timeout 5m
go test -tags=integration ./pkg/whail/ -v -timeout 10m
go test ./pkg/whail/... -count=1
```

### Wrap Up
1. Update Progress Tracker: Task 5 → `complete`
2. Append key learnings
3. Run final verification:
   ```bash
   go test ./pkg/whail/ -v -count=1           # unit tests
   go test ./... -count=1                       # full suite no regressions
   ```
4. **STOP.** Inform the user all 5 tasks are complete. Present summary of all files created/modified and suggest next steps (clawker consumer tests in future PR).

