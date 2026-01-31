# Testing Initiative Master Plan

Comprehensive testing overhaul for clawker, adopting patterns from Docker CLI (function-field mocks, per-package stubs) and GitHub CLI (factory/runF seams).

**PRD Reference:** `.claude/prds/cli_testing_adaptation/`

---

## Vision & Goals

1. **Replace GoMock incrementally** — Function-field pattern (explicit, no codegen, type-safe) replaces mockgen-based mocks
2. **Test at every seam boundary** — Each architectural layer gets its own test double pattern
3. **No regressions** — Existing GoMock tests remain until explicitly replaced in later phases
4. **Coverage targets** — All public API methods tested; pure functions get 100% coverage

## Adopted Patterns

| Pattern | Source | Usage |
|---------|--------|-------|
| Function-field mocks | Docker CLI `fakeClient` | `whailtest.FakeAPIClient`, `dockertest.FakeClient` |
| Factory + runF seams | GitHub CLI `cmdutil.Factory` | Per-command Options structs with function references |
| `*test` subpackage | Go stdlib (`net/http/httptest`) | `pkg/whail/whailtest/`, `internal/docker/dockertest/` |
| Input-spy closures | Docker CLI test patterns | Assign closures to Fn fields to inspect args passed to dependencies |
| Table-driven subtests | Go convention | All test files use `t.Run` with named subtests |

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | Function-field > GoMock | Explicit, no codegen, type-safe, better IDE support |
| 2 | Incremental deprecation | ~60+ command tests depend on GoMock; bulk rewrite is risky |
| 3 | `*test` subpackage convention | Matches Go stdlib (`httptest`, `iotest`); clean import paths |
| 4 | whailtest.FakeAPIClient at moby boundary | Tests everything above: whail jail logic AND docker.Client middleware |
| 5 | Tasks ordered by mock complexity | Pure functions first (instant wins), complex fakes last |
| 6 | Keep GoMock until Phase 4 | Existing tests work; replacing them is a separate phase |
| 7 | Factory constructor separation | `internal/cmd/factory/` constructor, `internal/cmdutil/` struct — enables test injection |
| 8 | Phase 2 reduced to pure functions only | `internal/docker/` likely needs refactoring; mock-heavy tests for orchestration methods would be wasted. 5 pure functions with real logic tested instead. |
| 9 | Composite fake over interface extraction for docker.Client | `docker.Client` is a concrete struct with 11+ methods; extracting a 45+ method interface would touch 35+ command files. Composing `whailtest.FakeAPIClient` into a real `*docker.Client` gives better coverage (real docker-layer code runs) with zero risk to existing code. |
| 10 | Cancel FakeCli — cobra+Factory pattern sufficient | `NewCmd(f, nil)` with faked Factory closures exercises the full CLI pipeline (cobra lifecycle, real flag parsing, real run function, real docker-layer code through whail jail). FakeCli would only add command routing (cobra's job) and PersistentPreRunE chain tests (simple/stable). Maintenance cost not justified. |

---

## Phase Roadmap

| Phase | Scope | Test Double | Seam Boundary | Status |
|-------|-------|-------------|---------------|--------|
| 1 | `pkg/whail/` | `whailtest.FakeAPIClient` | moby `client.APIClient` | **COMPLETE** |
| 2 | `internal/docker/` | None (pure function tests only) | N/A (reduced scope) | **COMPLETE** |
| 3 | `internal/docker/dockertest/` | `dockertest.FakeClient` (docker.Client method fakes) | `docker.Client` methods | **COMPLETE** |
| 4 | `internal/cmd/*/` command tests | Function-field fakes on Options structs via `runF` | Options struct function refs | NOT STARTED |
| 5 | Golden file tests | File-based output snapshots | CLI stdout/stderr | NOT STARTED |
| 6 | `FakeCli` integration | Top-level CLI test shell (Docker CLI pattern) | Full CLI pipeline | **CANCELLED** |

### Phase Details

**Phase 1 — whail (COMPLETE)**
- Created `whailtest.FakeAPIClient` with 41 Fn fields
- Jail REJECT tests (29 methods), INJECT_LABELS (4), INJECT_FILTER (12)
- Label override prevention tests
- Integration test gaps filled
- Branch: `a/testing` (merged)
- Memory: `whail-testing-master-plan`

**Phase 2 — internal/docker/ (COMPLETE)**
- Pure function unit tests for 5 functions: `parseContainers`, `isNotFoundError`, `shouldIgnore`, `matchPattern`, `LoadIgnorePatterns`
- 29 subtests across `client_test.go` and `volume_test.go`
- Bug fix: `matchPattern` `**/*.ext` glob matching (was doing literal HasSuffix, now uses filepath.Match)
- Audit concluded package likely needs refactoring — mock-heavy tests for orchestration methods deferred
- Deferred: `BuildImage`, `RemoveContainerWithVolumes`, `EnsureVolume`, `CopyToVolume`, `ListContainers`, `processBuildOutput`, `createTarArchive`, etc.
- Branch: `a/docker-internal-testing`
- Memory: `internal-docker-testing-plan`

**Phase 3 — dockertest package (COMPLETE)**
- Created `internal/docker/dockertest/` with `FakeClient` struct
- Composite pattern: `whailtest.FakeAPIClient` → `whail.NewFromExisting` → `&docker.Client{Engine: engine}`
- Uses `com.clawker` label prefix (production-equivalent) so docker-layer methods run real label filtering
- Helpers: `ContainerFixture`, `RunningContainerFixture`, `SetupContainerList`, `SetupFindContainer`, `SetupImageExists`
- Assertion wrappers: `AssertCalled`, `AssertNotCalled`, `AssertCalledN`, `Reset`
- 16 smoke tests across 7 test functions in `fake_client_test.go`
- Branch: `a/docker-internal-testing`

**Phase 4 — Command test migration (IN PROGRESS — Phase 4a complete)**
- Phase 4a (branch `a/command-tests`): dockertest expansion + container/run proof-of-concept
  - Task 0: Added `WorkDir` to `SetupMountsConfig` for testability (removes `os.Getwd()` call)
  - Task 1: Fixed `NewFakeClient` label bug (volume/network/image inspect defaults now use `com.clawker` labels); added 5 new helpers: `SetupContainerCreate`, `SetupContainerStart`, `SetupVolumeExists`, `SetupNetworkExists`, `SetupImageList`; plus `MinimalCreateOpts`, `MinimalStartOpts`, `ImageSummaryFixture` test fixtures
  - Task 2: Proof-of-concept `TestRunRun` with cobra+fake Factory pattern (3 subtests: detached mode, create failure, start failure)
  - Task 3: Migrated `TestImageArg` `@ symbol resolution` from gomock to dockertest; removed gomock import from `run_test.go`
- Canonical test pattern: cobra cmd.Execute() with faked Factory closures (no runF bypass)
- ~55+ remaining tests to migrate incrementally

**Phase 5 — Golden file tests (NOT STARTED)**
- File-based output snapshots for CLI commands
- Detect unintended output changes in CI

**Phase 6 — FakeCli (CANCELLED)**
- **Rationale:** The cobra+Factory pattern (proven in Phase 4a) already covers the full CLI pipeline.
  `NewCmdRun(f, nil)` exercises real run functions through cobra with real flag parsing, workspace setup,
  and Docker calls via `dockertest.FakeClient`. FakeCli would only add command routing tests (cobra's own
  responsibility) and PersistentPreRunE chain tests (simple/stable code). The overhead of building and
  maintaining a CLI test shell is not justified given the coverage already achieved.
- See Decision #10 in Decision Log.

---

## Context Window Management Rules

**After completing each task, agents MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the relevant phase memory's Progress Tracker
3. Append key learnings to the Key Learnings section
4. Present the handoff prompt to the user
5. Wait for the user to start a new conversation

This ensures each task gets a fresh context window. Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## Cross-Phase Key Learnings

- `whailtest.FakeAPIClient` embeds nil `*client.Client` for unexported moby interface methods — unoverridden methods panic (fail-loud)
- `whail.NewFromExisting(c client.APIClient, opts ...EngineOptions)` accepts variadic options
- `client.Filters` is `map[string]map[string]bool` — label entries stored as `"key=value": true`
- Module path: `github.com/schmitthub/clawker`
- `jail_test.go` must use `package whail_test` (external) to avoid import cycle with `whailtest`
- Integration tests use `package whail` (internal) and `//go:build integration` tag
- **Phase 3**: `dockertest.FakeClient` must use `com.clawker` label prefix (not `com.whailtest`) — docker-layer methods like `ListContainers` call `ClawkerFilter()` which filters by `com.clawker.managed`; using test labels would cause zero results
- **Phase 3**: `container.Summary.State` is `container.ContainerState` (a string typedef), not `string` — `assert.Equal` requires `string()` cast
- **Phase 3**: `docker.Client.ImageExists` calls `c.APIClient.ImageInspect` directly (bypasses whail Engine jail) — the `errNotFound` type in `dockertest/helpers.go` must satisfy `errdefs.IsNotFound` via `NotFound()` method interface
- **Phase 3**: `FindContainerByName` (whail Engine method) uses `ContainerList` with name filter + `ContainerInspect` for management check — `SetupFindContainer` must configure both Fn fields
- **Phase 3**: No import cycles: `internal/docker/dockertest` → `internal/docker` + `pkg/whail` + `pkg/whail/whailtest` is clean; `internal/cmd/*/` → `internal/docker/dockertest` will also be clean for Phase 4
- **Phase 3**: `dockertest` test file uses `package dockertest_test` (external) — tests only exercise public API, validating the consumer experience
- **Phase 4a**: `workspace.SetupMounts` previously called `os.Getwd()` internally — added `WorkDir` field to `SetupMountsConfig` with empty-string fallback to `os.Getwd()` for backward compat
- **Phase 4a**: `NewFakeClient` had latent label bug: only overrode `ContainerInspectFn`, leaving volume/network/image inspect with whailtest's `com.whailtest.managed` labels. Fixed all four inspect defaults.
- **Phase 4a**: Cobra+Factory test pattern works: `NewCmdRun(f, nil)` + `cmd.Execute()` exercises the real `runRun` with all flag parsing, `Changed()` support, and workspace setup. No runF bypass needed.
- **Phase 4a**: Default `NewFakeClient` volume/network inspect defaults are sufficient for `EnsureConfigVolumes` and `EnsureNetwork` flows — volumes appear to "already exist", network appears managed.
- **Phase 4a**: `config.FirewallConfig.Enable` is `bool` (not `*bool`), while `SecurityConfig.EnableHostProxy` is `*bool` — inconsistent nullability
- **Phase 4a**: The `*copts.ContainerOptions` embedded field promotes fields to parent structs (e.g., `opts.Agent`). When re-writing struct with `replace_symbol_body`, must keep anonymous embedding syntax (`*copts.ContainerOptions` not `ContainerOptions *copts.ContainerOptions`)
