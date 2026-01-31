# Update Serena Memories with Audited Docker Testing Scope

Both `internal-docker-testing-plan` and `testing-initiative-master-plan` memories already exist. Update them to reflect the reduced scope from our audit discussion.

## Context

- Audited all production code and tests in `internal/docker/`
- Package is flagged for refactoring (CLAUDE.md TODO)
- Mock-heavy tests for Docker-calling methods are low-ROI
- Reduced to 5 pure functions, single task, no mocks needed

## Step 1: Update `internal-docker-testing-plan` memory

Use `edit_memory` (regex mode) to make targeted replacements:

### 1a. Replace Progress Tracker (4 tasks → 1 task)

Replace the 4-row task table with:

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Pure function tests | `pending` | — |

### 1b. Replace Target Functions tables

Keep only these 5 pure functions (remove all Docker-calling methods):

| Symbol | Type | File | Mock Needs |
|--------|------|------|------------|
| `parseContainers` | Function | client.go | None (pure) |
| `isNotFoundError` | Function | client.go | None (pure) |
| `shouldIgnore` | Function | volume.go | None (pure) |
| `matchPattern` | Function | volume.go | None (pure) |
| `LoadIgnorePatterns` | Function | volume.go | None (pure) |

### 1c. Remove sections

- Remove "Test Helper Pattern" (`newTestClient()`) — no mocks needed
- Remove Tasks 2, 3, 4 entirely
- Remove GoMock Assessment section

### 1d. Rewrite Task 1 with test cases

**TestParseContainers:** empty list, single container (slash stripping + label extraction), missing labels (no panic), no names, multiple containers

**TestIsNotFoundError:** DockerError "not found", DockerError "No such", DockerError other, generic "not found", generic other, wrapped DockerError

**TestShouldIgnore:** .git always ignored, .git subpath, empty patterns, exact match, glob, no match, directory-only pattern, comment skipped, negation stub

**TestMatchPattern:** exact, no match, wildcard basename, wildcard full path, doublestar prefix, doublestar suffix, directory prefix, basename match

**TestLoadIgnorePatterns:** file not found → empty slice, valid file, whitespace trimmed, empty file

### 1e. Add Audit Findings section

Document what was deliberately excluded:
- `createTarArchive` — complex setup, CopyToVolume may be refactored
- `processBuildOutput/Quiet` — may move to separate package
- `IsAlpineImage` — trivial one-liner
- All Docker-calling methods — mock-always-passes, package may be refactored

### 1f. Update Agent Rules

Remove references to whailtest, mock imports, `newTestClient()`. Tests use `package docker` (internal), table-driven subtests, testify, `t.TempDir()` for file tests.

## Step 2: Update `testing-initiative-master-plan` memory

### 2a. Update Phase 2 row in roadmap table

Change from:
> `internal/docker/` | `whailtest.FakeAPIClient` via `whail.NewFromExisting` | `whail.Engine.APIClient` | **IN PROGRESS**

To:
> `internal/docker/` | None (pure function tests only) | N/A | **IN PROGRESS** (reduced scope — package flagged for refactor)

### 2b. Update Phase 2 detail section

Change from full client + volume method testing to: 5 pure functions, single task, no mocks. Note package refactoring TODO.

## Verification

`list_memories` → confirm both exist
`read_memory` each → confirm updated content is consistent
