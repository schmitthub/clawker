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
| Function-field mocks | Docker CLI `fakeClient` | `whailtest.FakeAPIClient`, future `dockertest.FakeClient` |
| Factory + runF seams | GitHub CLI `cmdutil.Factory` | Per-command Options structs with function references |
| `*test` subpackage | Go stdlib (`net/http/httptest`) | `pkg/whail/whailtest/`, future `internal/docker/dockertest/` |
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

---

## Phase Roadmap

| Phase | Scope | Test Double | Seam Boundary | Status |
|-------|-------|-------------|---------------|--------|
| 1 | `pkg/whail/` | `whailtest.FakeAPIClient` | moby `client.APIClient` | **COMPLETE** |
| 2 | `internal/docker/` | `whailtest.FakeAPIClient` via `whail.NewFromExisting` | `whail.Engine.APIClient` | **IN PROGRESS** |
| 3 | `internal/docker/dockertest/` | `dockertest.FakeClient` (docker.Client method fakes) | `docker.Client` methods | NOT STARTED |
| 4 | `internal/cmd/*/` command tests | Function-field fakes on Options structs via `runF` | Options struct function refs | NOT STARTED |
| 5 | Golden file tests | File-based output snapshots | CLI stdout/stderr | NOT STARTED |
| 6 | `FakeCli` integration | Top-level CLI test shell (Docker CLI pattern) | Full CLI pipeline | NOT STARTED |

### Phase Details

**Phase 1 — whail (COMPLETE)**
- Created `whailtest.FakeAPIClient` with 41 Fn fields
- Jail REJECT tests (29 methods), INJECT_LABELS (4), INJECT_FILTER (12)
- Label override prevention tests
- Integration test gaps filled
- Branch: `a/testing` (merged)
- Memory: `whail-testing-master-plan`

**Phase 2 — internal/docker/ (IN PROGRESS)**
- Pure function tests, build output parsing, client methods with faked engine, volume methods
- Branch: `a/docker-internal-testing`
- Memory: `internal-docker-testing-plan`

**Phase 3 — dockertest package (NOT STARTED)**
- Create `internal/docker/dockertest/` with `FakeClient` struct
- Function-field fakes for every `docker.Client` public method
- Used by command tests (Phase 4) to avoid reaching through to whail layer

**Phase 4 — Command test migration (NOT STARTED)**
- Replace GoMock-based command tests with function-field fakes
- Inject through existing `runF` hook on Options structs
- ~60+ tests to migrate incrementally

**Phase 5 — Golden file tests (NOT STARTED)**
- File-based output snapshots for CLI commands
- Detect unintended output changes in CI

**Phase 6 — FakeCli (NOT STARTED)**
- Top-level test shell factory (Docker CLI pattern)
- Full CLI pipeline testing without Docker daemon

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
