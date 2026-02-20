# Brainstorm: Config Package Split — Mock Infrastructure Pattern

> **Status:** Active
> **Created:** 2026-02-20
> **Last Updated:** 2026-02-20 03:45

## Problem / Topic

Multiple packages have a **moq regeneration deadlock**: interface, moq-generated mock, and test helpers (stubs) all live in the same package. When the interface changes, the mock goes stale, stubs reference the stale mock, package won't compile, moq can't run. This affects `internal/config` and `internal/project` today, and will affect any future package that needs generated mocks.

Need a scalable, domain-agnostic pattern — not a per-package bespoke solution.

## The Pattern: `mocks/` subpackage

**Every package keeps contracts, types, and implementation together. All test doubles (generated mocks + behavioral stubs) live in a `mocks/` subpackage.**

```
internal/
  config/
    config.go          ← Config interface + schema types + configImpl
    ...                ← implementation files (viper, dirty tracking, etc.)
    mocks/
      config_mock.go   ← moq-generated ConfigMock
      stubs.go         ← NewBlankConfig(), NewFromString(), NewIsolatedTestConfig()

  project/
    manager.go         ← ProjectManager + Project interfaces + impls
    ...                ← implementation files
    mocks/
      manager_mock.go  ← moq-generated ProjectManagerMock
      project_mock.go  ← moq-generated ProjectMock
      stubs.go         ← NewProjectManagerMock(), NewIsolatedTestManager(), etc.

  <any-future-pkg>/
    <pkg>.go           ← interface + types + impl
    mocks/
      <pkg>_mock.go    ← moq-generated
      stubs.go         ← behavioral test helpers
```

### Why it works

- Parent package never imports `mocks/` → always compiles → moq can always regenerate
- `mocks/` imports parent for interface + types (child imports parent, no cycle)
- Stubs in `mocks/` reference the mock in the same package — no cross-package mock dependency
- One subpackage per package — scales linearly, not 3x

### Import conventions

Callers alias to avoid collisions:
```go
import configmocks "internal/config/mocks"
import projectmocks "internal/project/mocks"
```

`go:generate` directive in parent, outputs to `mocks/`:
```go
//go:generate moq -pkg mocks -out mocks/config_mock.go . Config
```

### Relationship to existing `*test/` subpackages

Existing hand-written fakes (`dockertest/FakeClient`, `gittest/InMemoryGitManager`, etc.) were experimental. Will eventually converge to the `mocks/` pattern. Not a blocker for now — coexist until migrated.

## How the Behavioral Doubles Work (the real value)

**`NewFromString(yaml)`** — behavioral proxy over real production code:
1. Calls `ReadFromString()` → builds a REAL `configImpl` (real viper, real YAML parsing, real defaults)
2. Creates a `ConfigMock` shell
3. Wires every read method to delegate to the real configImpl's bound methods
4. Leaves `Set`/`Write`/`Watch` unwired — panics if called
5. Returns `*ConfigMock`

**`NewBlankConfig()`** — `NewFromString("")`. Real config, no custom values.

**`NewIsolatedTestConfig(t)`** — real `configImpl` with isolated filesystem sandbox. Returns `Config` (the interface), not mock. For mutation tests.

**`StubWriteConfig(t)`** — sets up isolated filesystem, returns reader callback.

## Decisions Made

- **`mocks/` subpackage pattern** — universal, domain-agnostic. One subpackage per package that needs generated mocks. All test doubles (moq mocks + behavioral stubs) live here.
- **No implementation split** — interface, types, and implementation stay in the same package for now. No `configloader/`. The split was about architectural purity; the deadlock fix only requires moving test doubles out.
- **moq stays** — dropping moq is "building around the crack." Mock infrastructure (method overriding, call recording) is real infrastructure needed at scale.
- **Pattern is expansion-ready** — any future package adds `mocks/` subpackage, same pattern, no new conventions to learn.

## Migration: Blast Radius

**Config package:**
- ~35 test files: `config.NewBlankConfig()` → `configmocks.NewBlankConfig()` (mechanical find/replace + add import)
- 2 production files to check (`internal/cmd/monitor/status/status.go`, `internal/cmd/monitor/up/up.go`)
- 0 direct `ConfigMock` references outside config — all callers use stubs

**Project package:**
- ~2-3 test files + docs
- 0 direct mock references in .go files outside project
- `project/mocks/stubs.go` will import `configmocks` (cross-dependency, no cycle)

**Documentation:** ~8 docs/memory files reference old names

**Total:** ~40 files, almost all single-line import + reference changes. No logic changes.

## Open Items / Questions

- Exact naming of stubs files within `mocks/` — single `stubs.go` or split by concern?
- Those 2 production files referencing config stubs — why? Need investigation.
- Should the `go:generate` directive live in the parent's interface file or in a separate `generate.go`?
- Migration sequencing: config first, then project? Or both at once?

## Gotchas / Risks

- `StubWriteConfig()` uses private constants (`clawkerConfigDirEnv`, etc.) — either hardcode env var strings in mocks/ or export from config.
- `project/mocks/stubs.go` imports `config/mocks` — cross-test-package dependency. Fine architecturally but worth tracking.
- `config_test.go` tests both interface and implementation behavior — stays in `config/` package, imports `config/mocks` for test helpers.
- Coordinate with ongoing configapocalypse migration to avoid conflicting changes.

## Next Steps

- User to confirm if ready to move to implementation planning / prototype
- Count exact callers for final migration checklist
- Investigate the 2 production files referencing stubs
