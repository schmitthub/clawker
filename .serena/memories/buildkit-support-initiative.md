# BuildKit Support via moby/buildkit Client Library

**Branch:** `a/buildkit-support`
**Parent memory:** `image-caching-optimization`
**PRD Reference:** `.claude/prds/buildkit_support_adaptation/`

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Spike — BuildKit client connection via Docker DialHijack | `pending` | — |
| Task 2: BuildKit Solve wrapper for Dockerfile builds | `pending` | — |
| Task 3: Wire BuildKit into build pipeline (replace SDK ImageBuild) | `pending` | — |
| Task 4: Conditional Dockerfile template (BuildKit vs legacy) | `pending` | — |
| Task 5: Testing infrastructure and fakes | `pending` | — |
| Task 6: Documentation and memory updates | `pending` | — |

## Key Learnings

(Agents append here as they complete tasks)

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Present the handoff prompt from the task's Wrap Up section to the user
5. Wait for the user to start a new conversation with the handoff prompt

This ensures each task gets a fresh context window. Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### Background

Clawker's image building pipeline currently uses the moby SDK's `ImageBuild` API (`POST /build`), which does NOT support BuildKit's session/gRPC protocol. The Dockerfile template (`internal/build/templates/Dockerfile.tmpl`) contains `--mount=type=cache` directives that require BuildKit but silently fail on the legacy builder.

**Solution:** Use the `moby/buildkit` Go client library to connect directly to Docker's embedded BuildKit daemon. Docker Desktop and Docker Engine 23.0+ embed a BuildKit daemon accessible via the Docker API's `/grpc` and `/session` hijack endpoints. This is exactly how `docker buildx` works internally (see `docker/buildx` repo, `driver/docker/driver.go`).

**Connection pattern** (proven by docker/buildx):
```go
import bkclient "github.com/moby/buildkit/client"

bkClient, err := bkclient.New(ctx, "",
    bkclient.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
        return dockerAPI.DialHijack(ctx, "/grpc", "h2c", nil)
    }),
    bkclient.WithSessionDialer(func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
        return dockerAPI.DialHijack(ctx, "/session", proto, meta)
    }),
)
```

**Build pattern** (from buildkit examples):
```go
_, err := bkClient.Solve(ctx, nil, bkclient.SolveOpt{
    Frontend:      "dockerfile.v0",
    FrontendAttrs: frontendAttrs,  // filename, build-args, target, labels, no-cache
    LocalMounts: map[string]fsutil.FS{
        "context":    contextFS,     // build context directory
        "dockerfile": dockerfileFS,  // directory containing Dockerfile
    },
    Exports: []bkclient.ExportEntry{{
        Type:  "image",
        Attrs: map[string]string{"name": imageTag, "push": "false"},
    }},
}, statusChan)
```

### Key Files

| File | Role |
|------|------|
| `internal/docker/client.go` | `Client` struct (embeds `*whail.Engine`), `BuildImage()`, `BuildImageOpts` |
| `internal/docker/buildkit.go` | `BuildKitEnabled()` detection — env var / daemon ping / OS heuristic |
| `internal/docker/env.go` | `RuntimeEnv()` — injects config-dependent env vars at container creation |
| `internal/build/build.go` | `Builder`, `Options`, `EnsureImage()`, `Build()`, `mergeImageLabels()` |
| `internal/build/defaults.go` | `BuildDefaultImage()` — builds default clawker image |
| `internal/build/templates/Dockerfile.tmpl` | Dockerfile with `--mount=type=cache` directives |
| `internal/build/hash.go` | `ContentHash()` — SHA-256 of rendered Dockerfile bytes |
| `internal/cmdutil/factory.go` | `Factory` struct with `BuildKitEnabled` closure field |
| `internal/cmd/factory/default.go` | Factory constructor — wires real dependencies |
| `internal/cmd/image/build/build.go` | `image build` command — detects BuildKit but doesn't propagate |
| `pkg/whail/whailtest/fake_client.go` | `FakeAPIClient` with function-field fakes |
| `internal/docker/dockertest/fake_client.go` | `FakeClient` wrapping `docker.Client` + `FakeAPIClient` |

### Current State (as of 2026-02-01)

- `BuildKitEnabled()` detection works and is tested
- Factory has `BuildKitEnabled` closure, wired in `default.go`
- Build command detects BuildKit but only logs a warning — result is NOT propagated
- `BuildImageOpts` has NO `BuildKitEnabled` field
- `build.Options` has NO `BuildKitEnabled` field
- `Client.BuildImage()` calls whail's `ImageBuild()` which calls moby SDK — NO BuildKit support
- Dockerfile template has ~8 `--mount=type=cache` directives that silently fail on legacy builder
- Content-addressed image caching works (hash of rendered Dockerfile bytes)

### Design Patterns

- **Factory closures:** `cmdutil.Factory` struct with closure fields, constructor in `internal/cmd/factory/`
- **Function-field fakes:** `FakeAPIClient` has `ImageBuildFn`, `ContainerCreateFn`, etc.
- **Test seam:** Commands use `NewCmd(f, runF)` pattern — `runF` is the test seam
- **Label-based isolation:** whail uses `com.clawker.managed` labels for resource filtering
- **Content hashing:** Only Dockerfile bytes are hashed — metadata injected via API/runtime

### Dependencies to Add

```
github.com/moby/buildkit       — BuildKit client, SolveOpt, session, progress
github.com/tonistiigi/fsutil   — Local filesystem mount for build context
```

### Rules

- Read `CLAUDE.md`, relevant `.claude/rules/` files, and package `CLAUDE.md` before starting
- Use Serena tools for code exploration — read symbol bodies only when needed
- All new code must compile and tests must pass (`make test`)
- Follow existing test patterns in the package
- Never store `context.Context` in struct fields
- Use `zerolog` for logging (never `fmt.Print`)
- After code changes, update relevant CLAUDE.md files

---

## Task 1: Spike — BuildKit Client Connection via Docker DialHijack

**Creates/modifies:** `internal/docker/buildkit.go`, `go.mod`, `go.sum`
**Depends on:** Nothing

### Goal

Prove that clawker can create a `moby/buildkit` client connected to Docker Desktop's embedded BuildKit daemon using the existing Docker connection. This is a spike — the code will be refined in Task 2.

### Implementation Phase

1. **Read existing code:** Read `internal/docker/client.go` to understand the `Client` struct and how `whail.Engine.APIClient` works. Verify `DialHijack` is available on the concrete moby client type.

2. **Add dependency:** Run `go get github.com/moby/buildkit@latest` and `go get github.com/tonistiigi/fsutil@latest`. These are the two core dependencies needed.

3. **Implement `NewBuildKitClient`** in `internal/docker/buildkit.go` (extend existing file):
   ```go
   import bkclient "github.com/moby/buildkit/client"
   
   // NewBuildKitClient creates a BuildKit client connected to Docker's embedded
   // buildkitd via the /grpc and /session hijack endpoints.
   func NewBuildKitClient(ctx context.Context, dockerClient DockerDialer) (*bkclient.Client, error) {
       return bkclient.New(ctx, "",
           bkclient.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
               return dockerClient.DialHijack(ctx, "/grpc", "h2c", nil)
           }),
           bkclient.WithSessionDialer(func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
               return dockerClient.DialHijack(ctx, "/session", proto, meta)
           }),
       )
   }
   ```

4. **Define `DockerDialer` interface** to abstract the DialHijack dependency:
   ```go
   // DockerDialer abstracts the Docker API's connection hijacking capability,
   // used to establish gRPC connections to Docker's embedded BuildKit daemon.
   type DockerDialer interface {
       DialHijack(ctx context.Context, url, proto string, meta map[string][]string) (net.Conn, error)
   }
   ```

5. **Add a verification function** (temporary, for the spike):
   ```go
   // VerifyBuildKitConnection tests the BuildKit connection by listing workers.
   func VerifyBuildKitConnection(ctx context.Context, c *bkclient.Client) error {
       workers, err := c.ListWorkers(ctx)
       if err != nil {
           return fmt.Errorf("buildkit connection failed: %w", err)
       }
       logger.Debug().Int("workers", len(workers)).Msg("BuildKit connection verified")
       return nil
   }
   ```

6. **Write a test** in `internal/docker/buildkit_test.go` (extend existing file) that verifies the connection works with a real Docker daemon (skip if no Docker):
   ```go
   func TestNewBuildKitClient_Integration(t *testing.T) {
       // Skip if no Docker available
       // Create real docker client
       // Call NewBuildKitClient
       // Call VerifyBuildKitConnection
       // Assert no error, workers > 0
   }
   ```

7. **Verify compilation:** `go build ./...` and `make test`

### Acceptance Criteria

```bash
go build ./internal/docker/...    # Must compile with new dependency
make test                          # All existing tests pass
# Integration test (requires Docker):
go test ./internal/docker/ -run TestNewBuildKitClient_Integration -v
```

### Wrap Up

1. Update Progress Tracker: Task 1 -> `complete`
2. Append key learnings (dependency versions, any gotchas with DialHijack, module compatibility issues)
3. **STOP.** Do not proceed to Task 2. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the BuildKit support initiative. Read the Serena memory `buildkit-support-initiative` — Task 1 is complete. Begin Task 2: BuildKit Solve wrapper for Dockerfile builds."

---

## Task 2: BuildKit Solve Wrapper for Dockerfile Builds

**Creates/modifies:** `internal/docker/buildkit_solve.go`, `internal/docker/buildkit_solve_test.go`
**Depends on:** Task 1

### Goal

Create a `BuildImageBuildKit()` method on `docker.Client` that builds images using BuildKit's `Solve` API with the `dockerfile.v0` frontend. This replaces the moby SDK's `ImageBuild` for BuildKit-enabled builds.

### Implementation Phase

1. **Read key files:** `internal/docker/client.go` (BuildImage, BuildImageOpts), `internal/build/build.go` (how Build/EnsureImage call BuildImage), `internal/docker/buildkit.go` (NewBuildKitClient from Task 1).

2. **Create `internal/docker/buildkit_solve.go`** with:

   a. **`BuildImageBuildKit` method** on `Client`:
   ```go
   // BuildImageBuildKit builds a Docker image using BuildKit's Solve API.
   // It connects to Docker's embedded BuildKit daemon and uses the dockerfile.v0
   // frontend to process the Dockerfile. The build context is a local directory.
   func (c *Client) BuildImageBuildKit(ctx context.Context, contextDir string, opts BuildImageOpts) error
   ```

   b. **Convert `BuildImageOpts` to `SolveOpt`:**
   - `opts.Tags` → Export entry `name` attribute (comma-separated for multiple tags)
   - `opts.Dockerfile` → `FrontendAttrs["filename"]` (base name) + `LocalMounts["dockerfile"]` (directory)
   - `opts.BuildArgs` → `FrontendAttrs["build-arg:KEY"]` = `VALUE`
   - `opts.NoCache` → `FrontendAttrs["no-cache"]` = `""`
   - `opts.Labels` → `FrontendAttrs["label:KEY"]` = `VALUE`
   - `opts.Target` → `FrontendAttrs["target"]`
   - `opts.Pull` → `FrontendAttrs["image-resolve-mode"]` = `"pull"` (or default)
   - `opts.NetworkMode` → `FrontendAttrs["force-network-mode"]`
   - `opts.SuppressOutput` → controls progress display behavior
   - `LocalMounts["context"]` = build context directory
   - `LocalMounts["dockerfile"]` = directory containing the Dockerfile
   - Export type = `"image"` with `name` = first tag, `push` = `"false"`

   c. **Progress streaming:** Create a goroutine that reads from the status channel and logs progress via zerolog. In non-quiet mode, show vertex names/status. In quiet mode, only log to debug.

   d. **BuildKit client lifecycle:** Create the BuildKit client per-build (using `NewBuildKitClient` from Task 1), close after build completes. This avoids connection lifecycle issues.

3. **Handle the Dockerfile path properly:**
   - Current code: `BuildImage` receives `io.Reader` (tar archive) for the build context
   - BuildKit: needs filesystem paths, not tar streams
   - The build context tar is created in `internal/build/build.go` via `docker.CreateBuildContext()`
   - **Key change:** `BuildImageBuildKit` takes a `contextDir string` instead of `io.Reader`
   - This means `BuildImageOpts` needs a new field: `ContextDir string` for the BuildKit path
   - The SDK path continues to use `io.Reader`; BuildKit path uses `contextDir`

4. **Add `ContextDir` and `BuildKitEnabled` to `BuildImageOpts`:**
   ```go
   type BuildImageOpts struct {
       // ... existing fields ...
       BuildKitEnabled bool   // Use BuildKit Solve API instead of legacy SDK
       ContextDir      string // Build context directory (required for BuildKit path)
   }
   ```

5. **Modify `BuildImage` to branch:**
   ```go
   func (c *Client) BuildImage(ctx context.Context, buildContext io.Reader, opts BuildImageOpts) error {
       if opts.BuildKitEnabled && opts.ContextDir != "" {
           return c.BuildImageBuildKit(ctx, opts.ContextDir, opts)
       }
       // existing SDK path unchanged
   }
   ```

6. **Write unit tests** in `buildkit_solve_test.go`:
   - `TestSolveOptsFromBuildImageOpts` — verify correct mapping of all fields
   - `TestBuildImageBuildKit_FrontendAttrs` — build-args, labels, target, no-cache all mapped correctly
   - `TestBuildImageBuildKit_MultipleTags` — comma-separated tag names in export attrs

### Acceptance Criteria

```bash
go build ./internal/docker/...   # Compiles
make test                         # All unit tests pass
# Integration (requires Docker):
go test ./internal/docker/ -run TestBuildImageBuildKit -v
```

### Wrap Up

1. Update Progress Tracker: Task 2 -> `complete`
2. Append key learnings (SolveOpt mapping gotchas, Dockerfile path handling, progress streaming approach)
3. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the BuildKit support initiative. Read the Serena memory `buildkit-support-initiative` — Task 2 is complete. Begin Task 3: Wire BuildKit into build pipeline."

---

## Task 3: Wire BuildKit into Build Pipeline (Replace SDK ImageBuild)

**Creates/modifies:** `internal/build/build.go`, `internal/build/defaults.go`, `internal/cmd/image/build/build.go`, `internal/cmdutil/factory.go`, `internal/cmd/factory/default.go`
**Depends on:** Task 2

### Goal

Thread `BuildKitEnabled` from detection through to `BuildImageOpts` at every call site. The build pipeline should use BuildKit when available and fall back to the legacy SDK path when not.

### Implementation Phase

1. **Read key files:** `internal/build/build.go` (Builder.Build, Builder.EnsureImage), `internal/build/defaults.go` (BuildDefaultImage), `internal/cmd/image/build/build.go` (buildRun), `internal/cmdutil/factory.go` (Factory struct).

2. **Add `BuildKitEnabled` to `build.Options`:**
   ```go
   type Options struct {
       // ... existing fields ...
       BuildKitEnabled bool
   }
   ```

3. **Modify `Builder.Build()`** to pass `BuildKitEnabled` and `ContextDir` through to `docker.BuildImageOpts`:
   - The `Builder` struct has `workDir` field — use this as the context directory
   - Pass `opts.BuildKitEnabled` and `b.workDir` through to `BuildImageOpts`
   - Both the custom Dockerfile path and generated Dockerfile path need updating
   - For generated Dockerfiles: write the rendered Dockerfile to a temp file in `workDir`, set `ContextDir` to `workDir`
   - For custom Dockerfiles: `ContextDir` is still `workDir`

4. **Modify `Builder.EnsureImage()`** — same pattern: thread through to the `Build` call.

5. **Modify `BuildDefaultImage()`** in `defaults.go`:
   - Already detects BuildKit (line ~103) but doesn't use the result
   - Pass detected `buildkitEnabled` to `BuildImageOpts`
   - Need to pass context directory for BuildKit path

6. **Modify `buildRun()` in `image/build/build.go`:**
   - Already detects BuildKit (line ~160) but only warns
   - Capture the result and pass to `build.Options{BuildKitEnabled: buildkitEnabled}`

7. **No Factory changes needed** — `BuildKitEnabled` is already on Factory and wired. The BuildKit client is created per-build inside `docker.Client.BuildImageBuildKit`.

### Acceptance Criteria

```bash
make test                          # All existing tests pass
go build ./...                     # Full compilation
# Verify the flow:
# image build command → build.Options{BuildKitEnabled} → docker.BuildImageOpts{BuildKitEnabled, ContextDir} → BuildImageBuildKit
```

### Wrap Up

1. Update Progress Tracker: Task 3 -> `complete`
2. Append key learnings (how context directory is passed, temp file handling for generated Dockerfiles)
3. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the BuildKit support initiative. Read the Serena memory `buildkit-support-initiative` — Task 3 is complete. Begin Task 4: Conditional Dockerfile template."

---

## Task 4: Conditional Dockerfile Template (BuildKit vs Legacy)

**Creates/modifies:** `internal/build/templates/Dockerfile.tmpl`, `internal/build/dockerfile.go`, `internal/build/hash_test.go`
**Depends on:** Task 3

### Goal

When BuildKit is NOT available, the Dockerfile template should not emit `--mount=type=cache` directives (which cause the legacy builder to error). When BuildKit IS available, emit them for optimal caching.

### Implementation Phase

1. **Read:** `internal/build/templates/Dockerfile.tmpl` (identify all `--mount=type=cache` directives), `internal/build/dockerfile.go` (template data structs — `DockerfileContext`, `DockerfileInstructions`).

2. **Add `BuildKitEnabled` to template data struct** (whichever struct feeds the template):
   - Find the struct that gets passed to `template.Execute()` — likely `DockerfileContext`
   - Add `BuildKitEnabled bool` field
   - Wire it from the caller (which has access to `build.Options.BuildKitEnabled`)

3. **Wrap cache mount directives in template conditionals:**

   For each `--mount=type=cache` occurrence in the template, create two variants:
   ```dockerfile
   {{- if .BuildKitEnabled}}
   RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
       --mount=type=cache,target=/var/lib/apt,sharing=locked \
       apt-get update && apt-get install -y {{.Packages}}
   {{- else}}
   RUN apt-get update && apt-get install -y {{.Packages}}
   {{- end}}
   ```

   Apply this to all ~8 `--mount=type=cache` directives in the template.

4. **Content hash impact:** The rendered Dockerfile will differ based on `BuildKitEnabled`, producing different content hashes. This is correct — BuildKit and legacy builds are structurally different images.

5. **Update tests:**
   - Add `TestContentHash_BuildKitVsLegacy` — verify different hashes
   - Add `TestContentHash_StableBuildKit` — same config → same hash (BuildKit mode)
   - Add `TestContentHash_StableLegacy` — same config → same hash (legacy mode)
   - Ensure `TestContentHash_MetadataStability` still passes in both modes

### Acceptance Criteria

```bash
make test                          # All tests pass
# Verify template renders correctly in both modes:
# - BuildKit mode: contains --mount=type=cache directives
# - Legacy mode: NO --mount=type=cache, same RUN commands without mounts
# - Content hashes differ between modes
# - Content hashes are stable within each mode
```

### Wrap Up

1. Update Progress Tracker: Task 4 -> `complete`
2. Append key learnings (template conditional syntax, hash stability verification)
3. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the BuildKit support initiative. Read the Serena memory `buildkit-support-initiative` — Task 4 is complete. Begin Task 5: Testing infrastructure and fakes."

---

## Task 5: Testing Infrastructure and Fakes

**Creates/modifies:** `internal/docker/dockertest/fake_client.go`, `internal/docker/buildkit_test.go`, `internal/docker/buildkit_solve_test.go`, `internal/cmd/image/build/build_test.go`
**Depends on:** Tasks 1-4

### Goal

Ensure the BuildKit integration is properly testable without a real Docker daemon. Add fakes for BuildKit operations and verify the full build pipeline in unit tests.

### Implementation Phase

1. **Define `BuildKitSolver` interface** for testability:
   ```go
   // BuildKitSolver abstracts BuildKit's Solve operation for testing.
   type BuildKitSolver interface {
       Solve(ctx context.Context, def *bkclient.Definition, opt bkclient.SolveOpt, statusChan chan *bkclient.SolveStatus) (*bkclient.SolveResponse, error)
       Close() error
   }
   ```
   Add this to `internal/docker/buildkit_solve.go` or a new `internal/docker/buildkit_iface.go`.

2. **Inject `BuildKitSolver` into `Client`** (or use a factory closure):
   - Option A: Add `BuildKitClientFactory func(ctx context.Context) (BuildKitSolver, error)` to `Client`
   - Option B: Add it to `cmdutil.Factory` as a closure
   - Option A is simpler and keeps BuildKit as an internal detail of `docker.Client`
   - Production: factory creates real BuildKit client via `NewBuildKitClient`
   - Tests: factory returns a fake

3. **Create `FakeBuildKitSolver`** in `internal/docker/dockertest/`:
   ```go
   type FakeBuildKitSolver struct {
       SolveFn func(ctx context.Context, ...) (*bkclient.SolveResponse, error)
       Closed  bool
   }
   ```

4. **Update `FakeClient`** in `internal/docker/dockertest/fake_client.go`:
   - Add `BuildKitSolverFn` field to configure fake BuildKit behavior
   - Wire into `Client.BuildKitClientFactory`

5. **Add unit tests:**
   - `TestBuildImage_RoutesToBuildKit` — when `BuildKitEnabled=true`, uses BuildKit path
   - `TestBuildImage_RoutesToSDK` — when `BuildKitEnabled=false`, uses SDK path
   - `TestBuildImage_BuildKitFallbackOnError` — if BuildKit connection fails, falls back to SDK with warning
   - Test the build command with both modes using fakes

6. **Add integration tests** (Docker required):
   - `TestBuildImageBuildKit_FullRoundTrip` — build a simple Dockerfile, verify image exists with managed labels
   - `TestBuildImageBuildKit_CacheMounts` — build Dockerfile with `--mount=type=cache`, verify success
   - `TestBuildImageBuildKit_Labels` — verify managed labels applied via BuildKit

### Acceptance Criteria

```bash
make test                          # All unit tests pass (no Docker)
go test ./internal/docker/... -v   # Docker tests pass (if Docker available)
```

### Wrap Up

1. Update Progress Tracker: Task 5 -> `complete`
2. Append key learnings (testing patterns, fake design decisions)
3. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the BuildKit support initiative. Read the Serena memory `buildkit-support-initiative` — Task 5 is complete. Begin Task 6: Documentation and memory updates."

---

## Task 6: Documentation and Memory Updates

**Creates/modifies:** `internal/docker/CLAUDE.md`, `internal/build/CLAUDE.md`, `CLAUDE.md` (root), this memory
**Depends on:** Tasks 1-5

### Goal

Update all documentation to reflect the new BuildKit integration.

### Implementation Phase

1. **Update `internal/docker/CLAUDE.md`:**
   - Document `NewBuildKitClient()`, `BuildImageBuildKit()`, `DockerDialer` interface
   - Document `BuildKitSolver` interface and `BuildKitClientFactory` pattern
   - Document the `/grpc` and `/session` DialHijack connection mechanism
   - Update `BuildImageOpts` with new `BuildKitEnabled` and `ContextDir` fields

2. **Update `internal/build/CLAUDE.md`:**
   - Document `BuildKitEnabled` on `build.Options`
   - Document conditional Dockerfile template behavior
   - Note content hash differences between BuildKit and legacy modes

3. **Update root `CLAUDE.md`** if any key concepts changed (e.g., new dependencies, architecture notes).

4. **Update `image-caching-optimization` memory:**
   - Mark "SDK vs CLI BuildKit" section as resolved — now using moby/buildkit client directly
   - Update "Current state" section to reflect BuildKit support is complete
   - Remove obsolete notes about "shell out to docker build" recommendation

5. **Update this memory** (`buildkit-support-initiative`):
   - Mark all tasks complete
   - Final key learnings summary

6. **Run freshness check:** `bash scripts/check-claude-freshness.sh`

### Acceptance Criteria

```bash
bash scripts/check-claude-freshness.sh   # No stale CLAUDE.md files
make test                                  # Tests still pass
```

### Wrap Up

1. Update Progress Tracker: Task 6 -> `complete`
2. **STOP.** Inform the user the BuildKit support initiative is complete. Present summary:

> **Initiative complete.** All 6 tasks done. BuildKit support is now integrated via `moby/buildkit` client library, connecting to Docker's embedded buildkitd via DialHijack. Cache mount directives in the Dockerfile template work with BuildKit and gracefully degrade for legacy builds.
