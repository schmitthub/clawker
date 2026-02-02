# BuildKit Build Path Bugfix

**Branch:** `a/image-caching`
**Goal:** Fix `clawker image build` failing with "the --mount option requires BuildKit" despite BuildKit being detected and enabled.

---

## Root Cause Analysis (COMPLETED)

The error "build error: the --mount option requires BuildKit" comes from the **legacy Docker builder** (confirmed by tracing the "build error:" prefix to `internal/docker/client.go:199,248` — only in `processBuildOutput`/`processBuildOutputQuiet`, which are legacy-path-only).

### Three bugs identified:

**Bug 1: Generated Dockerfile only exists in tar stream, not on disk for BuildKit**
- `Builder.Build()` generates the Dockerfile via `gen.GenerateBuildContext()` which creates a **tar stream** containing the Dockerfile + scripts (entrypoint.sh, firewall, etc.)
- `ContextDir` is set to `gen.GetBuildContext()` which returns `workDir` (the project directory)
- BuildKit's `toSolveOpt()` creates `LocalMounts["dockerfile"]` from `ContextDir` — so BuildKit looks for `Dockerfile` in `workDir`
- But the **generated** Dockerfile is NOT in `workDir` — it's only in the tar stream
- BuildKit either fails to find the Dockerfile, or finds a different/stale one in `workDir`
- The error propagates and somehow the legacy path ends up being used with a Dockerfile that has `--mount=type=cache`
- **Files:** `internal/build/build.go` (Builder.Build), `internal/build/dockerfile.go` (GenerateBuildContext — tar only), `pkg/whail/buildkit/solve.go` (toSolveOpt reads from ContextDir)

**Bug 2: `BuildDefaultImage` doesn't wire `BuildKitImageBuilder`**
- `internal/build/defaults.go:69` creates `docker.NewClient(ctx)` directly (not through factory)
- The factory (`internal/cmd/factory/default.go:58`) is the only place that wires `client.BuildKitImageBuilder = buildkit.NewImageBuilder(client.APIClient)`
- So `BuildDefaultImage`'s client has `BuildKitImageBuilder = nil`
- When routing to BuildKit, `ImageBuildKit()` returns `ErrBuildKitNotConfigured`
- **Files:** `internal/build/defaults.go`, `internal/cmd/factory/default.go`

### Template conditionals ARE correct
- All ~8 `--mount=type=cache` directives in `internal/build/templates/Dockerfile.tmpl` are properly wrapped in `{{- if .BuildKitEnabled}}` / `{{- else}}` blocks
- `BuildKitEnabled` propagates correctly through `DockerfileContext`, `ProjectGenerator`, `DockerfileManager`

---

## Implementation Plan (NOT STARTED)

### Step 1: Write build context to temp dir for BuildKit ✅
- Added `WriteBuildContextToDir(dir string, dockerfile []byte) error` on `ProjectGenerator`
- Modified `Builder.Build()` to create temp dir, write context via `WriteBuildContextToDir`, pass temp dir as `ContextDir` for BuildKit
- Legacy path unchanged: uses tar stream as before

### Step 2: Wire BuildKitImageBuilder in BuildDefaultImage ✅
- Added `client.BuildKitImageBuilder = buildkit.NewImageBuilder(client.APIClient)` in `defaults.go`

### Step 3: Tests ✅
- Added `TestWriteBuildContextToDir` — verifies all scripts written with correct permissions
- Added `TestWriteBuildContextToDir_NoFirewall` — firewall script omitted when disabled
- Added `TestWriteBuildContextToDir_WithIncludes` — include files copied from workDir
- All 2735 tests pass

### Step 3c: BuildKit Integration Tests ✅
- Created `test/whail/` with self-contained BuildKit integration tests
- 7 tests: MinimalImage, LabelsApplied, MultipleTags, BuildArgs, ContextFiles, CacheMounts, InvalidDockerfile
- Zero imports from `internal/` — only `pkg/whail`, `pkg/whail/buildkit`, moby client
- Auto-skips when Docker/BuildKit unavailable
- Self-contained TestMain with `com.whail.test.managed` label cleanup
- Added `make test-whail` target, included in `make test-all`

### Step 3b: Fix BuildKit exporter type ✅
- Changed exporter from `"image"` to `"moby"` in `pkg/whail/buildkit/solve.go`
- `"image"` exporter is for standalone buildkitd; Docker's embedded BuildKit registers `"moby"`
- Updated `solve_test.go` assertion accordingly

### Step 4: Manual verification ☐
- `go build -o bin/clawker ./cmd/clawker`
- `./bin/clawker image build` — should use BuildKit path, no `--mount` error

### Step 5: Update documentation ✅
- Update `internal/build/CLAUDE.md` with `WriteBuildContextToDir` method
- Update Serena memory `buildkit-support-initiative` with bug fix details

---

## Completed Work (Task 7 of BuildKit initiative)

Before discovering the bugs, Task 7 (documentation updates) was completed:
- Updated `pkg/whail/CLAUDE.md` — BuildKit section, whailtest faking
- Updated `internal/docker/CLAUDE.md` — delegation pattern, routing, dockertest helpers
- Updated `internal/build/CLAUDE.md` — conditional template, hash divergence
- Updated root `CLAUDE.md` — buildkit/ subpackage, BuildKitImageBuilder concept
- Updated `image-caching-optimization` memory — marked SDK vs CLI resolved
- Updated `buildkit-support-initiative` memory — all 7 tasks marked complete
- Freshness check: 0 warnings. Tests: 2732 pass.

---

## Key Context

- BuildKit routing condition: `opts.BuildKitEnabled && opts.ContextDir != ""` in `internal/docker/client.go:125`
- Factory wiring: `internal/cmd/factory/default.go:58` — `client.BuildKitImageBuilder = buildkit.NewImageBuilder(client.APIClient)`
- `BuildDefaultImage` called from `internal/resolver/image.go:216` during `clawker run` when default image missing
- The "build error:" prefix is EXCLUSIVELY from legacy path JSON parsing at `client.go:199,248`
- `ProjectGenerator.GetBuildContext()` always returns non-empty (workDir or config Build.Context)
- Related memories: `buildkit-support-initiative`, `image-caching-optimization`

---

## IMPERATIVE

**Always check with the user before proceeding with the next todo item.** If all work is done, ask the user if they want to delete this memory.
