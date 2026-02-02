# BuildKit Integration Tests (`test/whail/`)

**Branch:** `a/image-caching`
**Goal:** Create self-contained BuildKit integration tests that build real images via BuildKit against a running Docker daemon.

---

## Status: COMPLETE ✅

All implementation steps finished. Tests pass. Documentation updated.

---

## What Was Done

### Files Created
- `test/whail/main_test.go` — TestMain with `com.whail.test.managed` label cleanup + SIGINT handler
- `test/whail/helpers_test.go` — Self-contained helpers (requireDocker, requireBuildKit, newTestEngine, newRawClient, uniqueTag, writeDockerfile, writeContextFile, buildImage, execInImage, cleanupTestImages)
- `test/whail/buildkit_test.go` — 7 integration tests:
  1. `TestBuildKit_MinimalImage` — Happy path, verifies file content via exec
  2. `TestBuildKit_LabelsApplied` — Managed label + custom label preserved
  3. `TestBuildKit_MultipleTags` — Both tags resolve to same image ID
  4. `TestBuildKit_BuildArgs` — ARG/build-arg propagation
  5. `TestBuildKit_ContextFiles` — COPY from build context
  6. `TestBuildKit_CacheMounts` — `RUN --mount=type=cache` works (the whole point of BuildKit)
  7. `TestBuildKit_InvalidDockerfile` — Error returned for typo

### Files Modified
- `Makefile` — Added `test-whail` target, `.PHONY`, test exclusion from `make test`, added to `test-all`
- `test/CLAUDE.md` — Added `whail/` to directory tree and running instructions
- `CLAUDE.md` — Added `test/whail/` to structure and build commands
- `.claude/rules/testing.md` — Added `test-whail` command and Whail row to categories table
- `.serena/memories/buildkit-build-path-bugfix.md` — Marked Step 3c complete

### Deliberately Skipped
- `routing_test.go` — Per user feedback, routing is a thin conditional already covered by unit tests. No need for integration tests on managed/jail features either — those are covered by `pkg/whail/whailtest` unit tests.

## Key Design Decisions
- Zero imports from `internal/` — only `pkg/whail`, `pkg/whail/buildkit`, `moby/moby/client`
- `execInImage` uses whail Engine (not raw moby client) since test images are managed
- Raw moby client used only for label verification (ImageInspect) and cleanup
- Labels: `com.whail.test.managed=true` (whail-native, not clawker labels)
- Auto-skips via `requireDocker`/`requireBuildKit` when Docker/BuildKit unavailable

## Verification Results
- `make test` — 2735 unit tests pass, `test/whail` correctly excluded
- `go test ./test/whail/... -v -timeout 5m` — All 7 tests pass (~7s)
- `make test-whail` — Works as expected

## Steps (All Complete)
1. ✅ Create `test/whail/main_test.go`
2. ✅ Create `test/whail/helpers_test.go`
3. ✅ Create `test/whail/buildkit_test.go` (7 tests)
4. ✅ ~~Create `test/whail/routing_test.go`~~ — Skipped per user feedback
5. ✅ Update Makefile (test-whail target, .PHONY, test exclusion, test-all)
6. ✅ Update documentation (test/CLAUDE.md, root CLAUDE.md, .claude/rules/testing.md)
7. ✅ Update Serena memory (buildkit-build-path-bugfix)
8. ✅ Compile check + run all tests

## Plan File Reference
Original plan transcript: `/Users/andrew/.claude/projects/-Users-andrew-Code-clawker/456c3c37-c5d1-47c1-a70a-14d12292b2fc.jsonl`

---

## IMPERATIVE

**Always check with the user before proceeding with the next todo item.** If all work is done, ask the user if they want to delete this memory.
