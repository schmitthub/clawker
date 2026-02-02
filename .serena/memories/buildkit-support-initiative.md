# BuildKit Support via moby/buildkit Client Library

**Branch:** `a/buildkit-support`
**Parent memory:** `image-caching-optimization`
**PRD Reference:** `.claude/prds/buildkit_support_adaptation/`

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Spike — BuildKit client connection in whail/buildkit subpackage | `complete` | opus-4.5 |
| Task 2: whail ImageBuildKit method with label enforcement + closure injection | `complete` | opus-4.5 |
| Task 3: Wire BuildKit into build pipeline | `complete` | opus-4.5 |
| Task 4: Conditional Dockerfile template (BuildKit vs legacy) | `complete` | opus-4.5 |
| Task 5: Testing infrastructure and fakes | `complete` | opus-4.5 |
| Task 6: whail README.md — package docs and BuildKit extension guide | `complete` | opus-4.5 |
| Task 7: Documentation and memory updates | `complete` | opus-4.5 |

## Key Learnings

(Agents append here as they complete tasks)

### Task 3 Learnings (2026-02-01)

1. **Routing pattern in docker.Client.BuildImage:** When `BuildKitEnabled && ContextDir != ""`, routes to `c.ImageBuildKit()` (whail BuildKit path). Otherwise falls through to legacy `c.ImageBuild()` SDK path. Both paths go through whail for label enforcement.
2. **ContextDir sourcing:** In `Builder.Build()`, context dir comes from `gen.GetBuildContext()` (respects `config.Build.Context` or defaults to workDir). In `BuildDefaultImage()`, it's the `dockerfilesDir`. In the image build command, it flows through `build.Options.BuildKitEnabled` → `docker.BuildImageOpts.BuildKitEnabled/ContextDir`.
3. **Factory wiring is one line:** `client.BuildKitImageBuilder = buildkit.NewImageBuilder(client.APIClient)` inside `clientOnce.Do` block, after `docker.NewClient(ctx)`. The `docker.Client` embeds `*whail.Engine`, so `BuildKitImageBuilder` is promoted.
4. **BuildKit detection result captured:** In `buildRun`, the detection result variable was already scoped to the if-block. Moved to function scope so it can be passed to `build.Options`.
5. **No test changes needed:** All 2721 existing tests pass. The new fields are zero-valued (false/"") in existing tests, which means legacy path is used — backward-compatible.

### Task 2 Learnings (2026-02-01)

1. **SolveOpt field mapping:** Labels → `FrontendAttrs["label:KEY"]`, BuildArgs → `FrontendAttrs["build-arg:KEY"]`, Target → `FrontendAttrs["target"]`, Pull → `FrontendAttrs["image-resolve-mode"]="pull"`, NetworkMode → `FrontendAttrs["force-network-mode"]`, NoCache → `FrontendAttrs["no-cache"]=""`, Dockerfile → `FrontendAttrs["filename"]`.
2. **LocalMounts vs LocalDirs:** `SolveOpt.LocalDirs` is deprecated — use `LocalMounts map[string]fsutil.FS` with `fsutil.NewFS(dir)`.
3. **Export entry for local image store:** `Type: "image"`, `Attrs: {"name": "tag1,tag2", "push": "false"}`. Multiple tags are comma-separated in a single name attribute.
4. **Closure injection ergonomics:** Works exactly as designed — `engine.BuildKitImageBuilder = buildkit.NewImageBuilder(engine.APIClient)` is the one-liner. Tests set it to a func literal. Zero ceremony.
5. **Label enforcement identical to ImageBuild:** Copy options, merge `imageLabels()` + caller labels, force managed label. Same 3-line pattern.
6. **Progress handling:** `drainProgress` goroutine reads from `chan *bkclient.SolveStatus` until closed. Vertexes have names, Logs have data bytes. All logged via zerolog at debug level.

### Task 4 Learnings (2026-02-01)

1. **8 cache mount locations:** The template had `--mount=type=cache` in: ssh-proxy-builder Go module cache, Alpine apk cache (×3), Debian apt caches (×2 with dual mounts), Debian /tmp/downloads for git-delta, and npm cache for Claude Code install. All conditionally wrapped.
2. **Template data threading:** Added `BuildKitEnabled` field to `DockerfileContext`, `ProjectGenerator`, and `DockerfileManager`. Both code paths (project builds via `ProjectGenerator.buildContext()` and default image builds via `DockerfileManager.createContext()`) wire the field through.
3. **BuildDefaultImage restructured:** Moved Docker client creation and BuildKit detection (steps 3-4) before Dockerfile generation (step 5) so the template knows about BuildKit availability at render time. Previously detection happened after rendering.
4. **Zero-value backward compatibility:** `BuildKitEnabled` defaults to `false` (Go zero value), so all existing code paths and tests that don't set it explicitly get legacy Dockerfiles with no `--mount=type=cache` — no test changes needed for existing tests.
5. **Hash divergence is correct:** BuildKit and legacy Dockerfiles produce different content hashes because they are structurally different images. Added `TestContentHash_BuildKitVsLegacy`, `TestContentHash_StableBuildKit`, `TestContentHash_StableLegacy`, and `TestContentHash_BuildKitAlpineVsLegacy` tests.
6. **Template pattern:** Used `{{- if $.BuildKitEnabled}}` with `$` (root context) inside `range` blocks where `.` is rebound. Outside ranges, `.BuildKitEnabled` works fine.

### Task 5 Learnings (2026-02-01)

1. **Closure-based faking already covers whail level:** The existing `image_buildkit_test.go` already had NilBuilder, LabelEnforcement, ManagedLabelCannotBeOverridden, and BuilderError tests — all using inline closure fakes. The pattern `engine.BuildKitImageBuilder = func(...)` is so simple that a helper is optional but nice for convenience.
2. **whailtest helper pattern:** `BuildKitCapture` struct with `Opts`, `CallCount`, `Err` fields — returned by `FakeBuildKitBuilder(capture)`. Same pattern as the function-field fakes but for closures. Callers set `capture.Err` to simulate failures.
3. **dockertest SetupBuildKit wiring:** `f.Client.Engine.BuildKitImageBuilder = whailtest.FakeBuildKitBuilder(capture)` — the `Engine` is directly accessible on `docker.Client` since it's an embedded field. One-liner setup.
4. **Docker routing tests:** Three cases needed: (a) `BuildKitEnabled=true && ContextDir!=""` routes to BuildKit, (b) `BuildKitEnabled=false` routes to legacy, (c) `BuildKitEnabled=true && ContextDir=""` falls to legacy. All use `whailtest.FakeBuildKitBuilder` + `ImageBuildFn` with empty body.
5. **Legacy path needs valid response body:** `Client.BuildImage()` processes the legacy response body with a scanner. An empty `io.NopCloser(bytes.NewReader(nil))` works fine — scanner returns immediately.
6. **Build command test:** `BuildKitEnabled` is a runtime closure (not a CLI flag), so it's wired from `Factory.BuildKitEnabled` to `BuildOptions.BuildKitEnabled`. Test verifies the closure delegation.
7. **2732 tests pass, 0 failures.** The `BuildKitCapture` type alias `type BuildKitCapture = whailtest.BuildKitCapture` in dockertest avoids a second struct definition.

### Task 7 Learnings (2026-02-01)

1. **Updated CLAUDE.md files:** `pkg/whail/CLAUDE.md` (BuildKit section with detection, closure, options, subpackage table, whailtest BuildKit faking), `internal/docker/CLAUDE.md` (delegation pattern, routing docs, BuildKit faking in dockertest), `internal/build/CLAUDE.md` (conditional template behavior, hash divergence), root `CLAUDE.md` (buildkit/ subpackage in repo structure, BuildKitImageBuilder concept).
2. **Updated memories:** `image-caching-optimization` (marked SDK vs CLI section as resolved), `buildkit-support-initiative` (all 7 tasks complete).
3. **Freshness check:** 29 files checked, 0 warnings. All 2732 tests pass.

### Task 6 Learnings (2026-02-01)

1. **README scope:** Covers quick start, custom config, `NewFromExisting`, label enforcement, BuildKit extension (enabling, detection, options table, how it works), error handling, full operation listing, testing (FakeAPIClient + BuildKit closure faking), and package layout.
2. **No code changes needed:** Pure documentation task — 2732 tests pass unchanged.

### Task 1 Learnings (2026-02-01)

1. **Dependency versions:** `github.com/moby/buildkit v0.27.1` and `github.com/tonistiigi/fsutil` (latest). BuildKit pulls in gRPC, protobuf, containerd, opentelemetry — significant dependency weight, validating the subpackage isolation approach.
2. **DialHijack works cleanly:** `bkclient.New(ctx, "", WithContextDialer(...), WithSessionDialer(...))` connects to Docker Desktop's embedded buildkitd without issues. The `/grpc` endpoint uses `h2c` protocol, `/session` uses the proto string from the callback.
3. **Worker verification:** `bkClient.ListWorkers(ctx)` confirms connectivity and returns worker info. Docker Desktop exposes at least one worker.
4. **Module compatibility:** `go 1.25.0` — buildkit upgraded the go directive. `containerd/platforms` bumped from v0.2.1 to v1.0.0-rc.2. `fsnotify` bumped. No breaking changes observed.
5. **Delegation pattern:** `type Pinger = whail.Pinger` (type alias, not new type) ensures callers don't need code changes — `docker.Pinger` IS `whail.Pinger`.
6. **Integration test gate:** Used `CLAWKER_INTEGRATION=1` env var guard (matches project pattern). Test creates real Docker client, connects BuildKit, verifies workers > 0.

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

### Architecture Decision: BuildKit in whail via Closure Injection + Subpackage

BuildKit support belongs in `pkg/whail`, but the heavy `moby/buildkit` dependency is isolated in a `pkg/whail/buildkit/` subpackage. The whail core gets zero new imports — it defines the options type and does label enforcement via a closure field on Engine. Consumers opt into BuildKit by importing the subpackage and wiring the closure.

**Why this design:**

1. **Label enforcement is whail's job.** `Engine.ImageBuildKit()` merges managed labels before delegating to the closure. The closure never sees un-merged labels. Same guarantee as `Engine.ImageBuild()`.

2. **Dependency isolation.** `moby/buildkit` pulls in gRPC, protobuf, containerd, opentelemetry — significant weight. Consumers who only want whail's label-based Docker wrapper don't pay this cost. Only importing `pkg/whail/buildkit/` adds the dependency tree.

3. **Matches existing patterns.** The project already uses closure fields everywhere (`cmdutil.Factory`). Same pattern: closure field, wired at construction, nil means not available.

4. **Testing is trivial.** Set `Engine.BuildKitImageBuilder` to a func literal in tests. No interface needed, no fake struct — just a closure.

**Package layout:**

```
pkg/whail/
    engine.go              — Engine struct + BuildKitImageBuilder closure field
    image.go               — ImageBuild() (legacy, unchanged)
                           — ImageBuildKit() (label enforcement → delegates to closure)
    types.go               — ImageBuildKitOptions (plain struct, zero buildkit imports)
    buildkit.go            — BuildKitEnabled() detection (uses moby types only, not moby/buildkit)
    buildkit/              — subpackage (ONLY place that imports moby/buildkit)
        builder.go         — NewImageBuilder() returns the closure
        client.go          — NewBuildKitClient() via DialHijack
        solve.go           — toSolveOpt() conversion, progress handling
    whailtest/
        fake_client.go     — existing fakes (updated)

internal/docker/
    client.go              — Client.BuildImage() routes to whail BuildKit or legacy
    buildkit.go            — REMOVE (move BuildKitEnabled to whail)
```

**Wiring example (how clawker uses it):**
```go
import (
    "github.com/anthropics/clawker/pkg/whail"
    "github.com/anthropics/clawker/pkg/whail/buildkit"
)

engine, _ := whail.New(ctx)
engine.BuildKitImageBuilder = buildkit.NewImageBuilder(engine.APIClient)
// Now engine.ImageBuildKit() works — labels enforced by whail, Solve by subpackage
```

**Core whail code (zero moby/buildkit imports):**
```go
// pkg/whail/engine.go
type Engine struct {
    // ...existing fields...
    BuildKitImageBuilder func(ctx context.Context, opts ImageBuildKitOptions) error
}

// pkg/whail/image.go
func (e *Engine) ImageBuildKit(ctx context.Context, opts ImageBuildKitOptions) error {
    if e.BuildKitImageBuilder == nil {
        return ErrBuildKitNotConfigured()
    }
    opts.Labels = MergeLabels(e.imageLabels(), opts.Labels)
    opts.Labels[e.managedLabelKey] = e.managedLabelValue
    return e.BuildKitImageBuilder(ctx, opts)
}
```

**Subpackage (isolated moby/buildkit imports):**
```go
// pkg/whail/buildkit/builder.go
func NewImageBuilder(apiClient whail.APIClient) func(context.Context, whail.ImageBuildKitOptions) error {
    return func(ctx context.Context, opts whail.ImageBuildKitOptions) error {
        bkClient, err := NewBuildKitClient(ctx, apiClient)
        if err != nil { return err }
        defer bkClient.Close()
        solveOpt := toSolveOpt(opts)
        statusChan := make(chan *bkclient.SolveStatus)
        go drainProgress(statusChan, opts.SuppressOutput)
        _, err = bkClient.Solve(ctx, nil, solveOpt, statusChan)
        return err
    }
}
```

### Background

Clawker's image building pipeline currently uses the moby SDK's `ImageBuild` API (`POST /build`), which does NOT support BuildKit's session/gRPC protocol. The Dockerfile template (`internal/build/templates/Dockerfile.tmpl`) contains `--mount=type=cache` directives that require BuildKit but silently fail on the legacy builder.

**Solution:** Use the `moby/buildkit` Go client library to connect directly to Docker's embedded BuildKit daemon. Docker Desktop and Docker Engine 23.0+ embed a BuildKit daemon accessible via the Docker API's `/grpc` and `/session` hijack endpoints. This is exactly how `docker buildx` works internally (see `docker/buildx` repo, `driver/docker/driver.go`).

**Connection pattern** (proven by docker/buildx):
```go
import bkclient "github.com/moby/buildkit/client"

bkClient, err := bkclient.New(ctx, "",
    bkclient.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
        return apiClient.DialHijack(ctx, "/grpc", "h2c", nil)
    }),
    bkclient.WithSessionDialer(func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
        return apiClient.DialHijack(ctx, "/session", proto, meta)
    }),
)
```

**Build pattern** (from buildkit examples):
```go
_, err := bkClient.Solve(ctx, nil, bkclient.SolveOpt{
    Frontend:      "dockerfile.v0",
    FrontendAttrs: frontendAttrs,
    LocalMounts: map[string]fsutil.FS{
        "context":    contextFS,
        "dockerfile": dockerfileFS,
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
| `pkg/whail/engine.go` | `Engine` struct, `APIClient` field, `BuildKitImageBuilder` closure, label helpers |
| `pkg/whail/image.go` | `Engine.ImageBuild()` (legacy), `Engine.ImageBuildKit()` (label enforcement + delegate) |
| `pkg/whail/types.go` | `ImageBuildKitOptions` (plain struct, no buildkit imports) |
| `pkg/whail/buildkit.go` | `BuildKitEnabled()` detection (moby types only) |
| `pkg/whail/buildkit/` | **NEW subpackage** — `NewImageBuilder()`, `NewBuildKitClient()`, `toSolveOpt()` |
| `pkg/whail/whailtest/fake_client.go` | `FakeAPIClient` with function-field fakes |
| `internal/docker/client.go` | `Client` struct (embeds `*whail.Engine`), `BuildImage()`, `BuildImageOpts` |
| `internal/docker/buildkit.go` | `BuildKitEnabled()` — **TO BE MOVED to whail** |
| `internal/docker/env.go` | `RuntimeEnv()` — config-dependent env vars at container creation |
| `internal/build/build.go` | `Builder`, `Options`, `EnsureImage()`, `Build()`, `mergeImageLabels()` |
| `internal/build/defaults.go` | `BuildDefaultImage()` — builds default clawker image |
| `internal/build/templates/Dockerfile.tmpl` | Dockerfile with `--mount=type=cache` directives |
| `internal/build/hash.go` | `ContentHash()` — SHA-256 of rendered Dockerfile bytes |
| `internal/cmdutil/factory.go` | `Factory` struct with `BuildKitEnabled` closure field |
| `internal/cmd/factory/default.go` | Factory constructor — wires real dependencies |
| `internal/cmd/image/build/build.go` | `image build` command — detects BuildKit but doesn't propagate |

### Current State (as of 2026-02-01)

- `BuildKitEnabled()` detection works and is tested (currently in `internal/docker`, needs to move to whail)
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
- **Closure injection for extensions:** `Engine.BuildKitImageBuilder` is a closure field — nil means not configured, set at construction time by consumers who want BuildKit

### Dependencies to Add

```
github.com/moby/buildkit       — BuildKit client, SolveOpt, session, progress
github.com/tonistiigi/fsutil   — Local filesystem mount for build context
```

These go in `pkg/whail/buildkit/` subpackage imports ONLY. The whail core (`pkg/whail/*.go`) gets zero new imports.

### Rules

- Read `CLAUDE.md`, relevant `.claude/rules/` files, and package `CLAUDE.md` before starting
- Use Serena tools for code exploration — read symbol bodies only when needed
- All new code must compile and tests must pass (`make test`)
- Follow existing test patterns in the package
- Never store `context.Context` in struct fields
- Use `zerolog` for logging (never `fmt.Print`)
- After code changes, update relevant CLAUDE.md files

---

## Task 1: Spike — BuildKit Client Connection in whail/buildkit Subpackage

**Creates/modifies:** `pkg/whail/buildkit/client.go` (new), `pkg/whail/buildkit.go` (new, detection moved from internal/docker), `go.mod`, `go.sum`
**Also modifies:** `internal/docker/buildkit.go` (remove or delegate), `internal/docker/buildkit_test.go` (move tests)
**Depends on:** Nothing

### Goal

Prove that whail can create a `moby/buildkit` client connected to Docker Desktop's embedded BuildKit daemon. Move `BuildKitEnabled` detection from `internal/docker` to `pkg/whail` (it imports moby types). Create the `pkg/whail/buildkit/` subpackage with the client connection code. This is a spike — the Solve wrapper comes in Task 2.

### Implementation Phase

1. **Read existing code:** Read `pkg/whail/engine.go` to understand `Engine.APIClient` and how DialHijack is available. Read `internal/docker/buildkit.go` and `internal/docker/buildkit_test.go` to understand the existing `BuildKitEnabled` code that needs to move.

2. **Add dependency:** Run `go get github.com/moby/buildkit@latest` and `go get github.com/tonistiigi/fsutil@latest`.

3. **Create `pkg/whail/buildkit.go`** (in whail core, NOT the subpackage — uses moby types only):

   a. **Move `BuildKitEnabled` from `internal/docker`:**
   ```go
   package whail

   // Pinger is the subset of the Docker API needed for BuildKit detection.
   type Pinger interface {
       Ping(ctx context.Context, options client.PingOptions) (client.PingResult, error)
   }

   // BuildKitEnabled checks whether BuildKit is available.
   func BuildKitEnabled(ctx context.Context, p Pinger) (bool, error)
   ```

4. **Create `pkg/whail/buildkit/client.go`** (subpackage — imports moby/buildkit):

   a. **`NewBuildKitClient`:**
   ```go
   package buildkit

   import bkclient "github.com/moby/buildkit/client"

   // NewBuildKitClient creates a BuildKit client connected to Docker's embedded
   // buildkitd via the /grpc and /session hijack endpoints.
   func NewBuildKitClient(ctx context.Context, apiClient DockerDialer) (*bkclient.Client, error) {
       return bkclient.New(ctx, "",
           bkclient.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
               return apiClient.DialHijack(ctx, "/grpc", "h2c", nil)
           }),
           bkclient.WithSessionDialer(func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
               return apiClient.DialHijack(ctx, "/session", proto, meta)
           }),
       )
   }

   // DockerDialer abstracts the DialHijack capability on the moby client.
   type DockerDialer interface {
       DialHijack(ctx context.Context, url, proto string, meta map[string][]string) (net.Conn, error)
   }
   ```
   Note: `DockerDialer` is defined in the subpackage (not whail core) since it's only needed by BuildKit client creation. `Engine.APIClient` satisfies it.

   b. **Verification function** (temporary, for the spike):
   ```go
   func VerifyConnection(ctx context.Context, c *bkclient.Client) error
   ```

5. **Update `internal/docker/buildkit.go`:** Replace with thin delegation to whail:
   ```go
   func BuildKitEnabled(ctx context.Context, p whail.Pinger) (bool, error) {
       return whail.BuildKitEnabled(ctx, p)
   }
   ```
   Or update all callers to use `whail.BuildKitEnabled` directly and delete the file.

6. **Move tests** from `internal/docker/buildkit_test.go` to `pkg/whail/buildkit_test.go` (for detection) and `pkg/whail/buildkit/client_test.go` (for connection). Add integration test:
   ```go
   func TestNewBuildKitClient_Integration(t *testing.T) {
       // Skip if no Docker available
       // Create real docker client via whail.New()
       // Call buildkit.NewBuildKitClient(ctx, engine.APIClient)
       // Call buildkit.VerifyConnection
       // Assert no error, workers > 0
   }
   ```

7. **Fix all references** to `docker.BuildKitEnabled` and `docker.Pinger` across the codebase. Key locations:
   - `internal/cmd/factory/default.go` — Factory wiring
   - `internal/cmd/image/build/build.go` — build command
   - `internal/build/defaults.go` — default image building

8. **Verify compilation:** `go build ./...` and `make test`

### Acceptance Criteria

```bash
go build ./...                     # Full compilation (all references updated)
make test                          # All existing tests pass
# Integration test (requires Docker):
go test ./pkg/whail/buildkit/ -run TestNewBuildKitClient_Integration -v
```

### Wrap Up

1. Update Progress Tracker: Task 1 -> `complete`
2. Append key learnings (dependency versions, DialHijack gotchas, module compatibility, reference migration)
3. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the BuildKit support initiative. Read the Serena memory `buildkit-support-initiative` — Task 1 is complete. Begin Task 2: whail ImageBuildKit method with label enforcement and closure injection."

---

## Task 2: whail ImageBuildKit Method with Label Enforcement + Closure Injection

**Creates/modifies:** `pkg/whail/engine.go` (add closure field), `pkg/whail/image.go` (add ImageBuildKit), `pkg/whail/types.go` (add ImageBuildKitOptions), `pkg/whail/errors.go` (add ErrBuildKitNotConfigured), `pkg/whail/buildkit/builder.go` (new — NewImageBuilder)
**Depends on:** Task 1

### Goal

Implement the closure injection pattern. whail core gets `Engine.BuildKitImageBuilder` closure field and `Engine.ImageBuildKit()` method (label enforcement + delegate). The `buildkit/` subpackage provides `NewImageBuilder()` that returns the closure.

### Implementation Phase

1. **Read key files:** `pkg/whail/image.go` (`ImageBuild` — study label injection), `pkg/whail/engine.go` (`imageLabels()`, `managedLabels()`, `managedLabelKey`).

2. **Add `ImageBuildKitOptions` to `pkg/whail/types.go`:**
   ```go
   type ImageBuildKitOptions struct {
       Tags           []string
       ContextDir     string                // Build context directory (required)
       Dockerfile     string                // Relative to context (default: "Dockerfile")
       BuildArgs      map[string]*string
       NoCache        bool
       Labels         map[string]string     // Managed labels injected automatically
       Target         string
       Pull           bool
       SuppressOutput bool
       NetworkMode    string
   }
   ```

3. **Add closure field to `Engine`** in `pkg/whail/engine.go`:
   ```go
   type Engine struct {
       // ...existing fields...
       // BuildKitImageBuilder handles BuildKit image builds when set.
       // Label enforcement is applied by ImageBuildKit before delegating.
       // Wire via: engine.BuildKitImageBuilder = buildkit.NewImageBuilder(engine.APIClient)
       BuildKitImageBuilder func(ctx context.Context, opts ImageBuildKitOptions) error
   }
   ```

4. **Add `ImageBuildKit` method** in `pkg/whail/image.go`:
   ```go
   func (e *Engine) ImageBuildKit(ctx context.Context, opts ImageBuildKitOptions) error {
       if e.BuildKitImageBuilder == nil {
           return ErrBuildKitNotConfigured()
       }
       // Label enforcement — same pattern as ImageBuild
       optsCopy := opts
       optsCopy.Labels = MergeLabels(e.imageLabels(), opts.Labels)
       optsCopy.Labels[e.managedLabelKey] = e.managedLabelValue
       return e.BuildKitImageBuilder(ctx, optsCopy)
   }
   ```

5. **Add `ErrBuildKitNotConfigured`** to `pkg/whail/errors.go`.

6. **Create `pkg/whail/buildkit/builder.go`** — the closure implementation:
   ```go
   func NewImageBuilder(apiClient DockerDialer) func(context.Context, whail.ImageBuildKitOptions) error {
       return func(ctx context.Context, opts whail.ImageBuildKitOptions) error {
           bkClient, err := NewBuildKitClient(ctx, apiClient)
           if err != nil { return err }
           defer bkClient.Close()
           solveOpt := toSolveOpt(opts)
           statusChan := make(chan *bkclient.SolveStatus)
           go drainProgress(statusChan, opts.SuppressOutput)
           _, err = bkClient.Solve(ctx, nil, solveOpt, statusChan)
           return err
       }
   }
   ```

7. **Create `pkg/whail/buildkit/solve.go`** — SolveOpt conversion:
   - `opts.Labels` → `FrontendAttrs["label:KEY"]` = `VALUE` (labels already merged by whail core)
   - `opts.Tags` → Export entry `name` (comma-separated)
   - `opts.Dockerfile` → `FrontendAttrs["filename"]`
   - `opts.BuildArgs` → `FrontendAttrs["build-arg:KEY"]` = `VALUE`
   - `opts.NoCache` → `FrontendAttrs["no-cache"]` = `""`
   - `opts.Target` → `FrontendAttrs["target"]`
   - `opts.Pull` → `FrontendAttrs["image-resolve-mode"]` = `"pull"`
   - `opts.NetworkMode` → `FrontendAttrs["force-network-mode"]`
   - `LocalMounts["context"]` + `LocalMounts["dockerfile"]` from `ContextDir`
   - Export type = `"image"`, `push` = `"false"`

8. **Create `pkg/whail/buildkit/progress.go`** — `drainProgress()` goroutine, logs via zerolog.

9. **Write unit tests:**
   - `pkg/whail/buildkit/solve_test.go`: `TestToSolveOpt_*` — verify all field mappings
   - `pkg/whail/image_test.go` (or `buildkit_test.go`): `TestImageBuildKit_LabelEnforcement` — verify labels merged and managed label forced, using a fake closure
   - `pkg/whail/image_test.go`: `TestImageBuildKit_NilBuilder` — returns ErrBuildKitNotConfigured

10. **Add integration test** (Docker required):
    ```go
    func TestImageBuildKit_Integration(t *testing.T) {
        engine, _ := whail.New(ctx)
        engine.BuildKitImageBuilder = buildkit.NewImageBuilder(engine.APIClient)
        // Write minimal Dockerfile to temp dir
        // Call engine.ImageBuildKit(ctx, opts)
        // Verify image exists via engine.ImageList
        // Verify managed labels present
    }
    ```

### Acceptance Criteria

```bash
go build ./pkg/whail/...          # Core compiles without moby/buildkit
go build ./pkg/whail/buildkit/... # Subpackage compiles with moby/buildkit
make test                          # All unit tests pass
# Integration (requires Docker):
go test ./pkg/whail/buildkit/ -run TestImageBuildKit_Integration -v
```

### Wrap Up

1. Update Progress Tracker: Task 2 -> `complete`
2. Append key learnings (SolveOpt mapping, closure injection ergonomics, label FrontendAttrs format)
3. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the BuildKit support initiative. Read the Serena memory `buildkit-support-initiative` — Task 2 is complete. Begin Task 3: Wire BuildKit into build pipeline."

---

## Task 3: Wire BuildKit into Build Pipeline

**Creates/modifies:** `internal/docker/client.go` (BuildImage routing, BuildImageOpts), `internal/build/build.go`, `internal/build/defaults.go`, `internal/cmd/image/build/build.go`, `internal/cmd/factory/default.go` (wire BuildKit into engine)
**Depends on:** Task 2

### Goal

Thread `BuildKitEnabled` from detection through to whail's `ImageBuildKit()` at every call site. Wire `buildkit.NewImageBuilder` into the Engine at factory construction time.

### Implementation Phase

1. **Read key files:** `internal/docker/client.go` (BuildImage, BuildImageOpts), `internal/build/build.go` (Builder.Build, EnsureImage), `internal/build/defaults.go` (BuildDefaultImage), `internal/cmd/image/build/build.go` (buildRun), `internal/cmd/factory/default.go` (factory wiring).

2. **Wire BuildKit into Engine at factory construction** in `internal/cmd/factory/default.go`:
   ```go
   import "github.com/anthropics/clawker/pkg/whail/buildkit"

   // In the factory constructor, after creating the engine:
   engine.BuildKitImageBuilder = buildkit.NewImageBuilder(engine.APIClient)
   ```

3. **Add fields to `BuildImageOpts`:**
   ```go
   type BuildImageOpts struct {
       // ...existing fields...
       BuildKitEnabled bool
       ContextDir      string
   }
   ```

4. **Modify `Client.BuildImage()` to route:**
   ```go
   func (c *Client) BuildImage(ctx context.Context, buildContext io.Reader, opts BuildImageOpts) error {
       if opts.BuildKitEnabled && opts.ContextDir != "" {
           return c.Engine.ImageBuildKit(ctx, whail.ImageBuildKitOptions{
               Tags: opts.Tags, ContextDir: opts.ContextDir, Dockerfile: opts.Dockerfile,
               BuildArgs: opts.BuildArgs, NoCache: opts.NoCache, Labels: opts.Labels,
               Target: opts.Target, Pull: opts.Pull, SuppressOutput: opts.SuppressOutput,
               NetworkMode: opts.NetworkMode,
           })
       }
       // Legacy SDK path unchanged
   }
   ```
   Both paths go through whail — label enforcement either way.

5. **Add `BuildKitEnabled` to `build.Options`.**

6. **Modify `Builder.Build()`** — pass `BuildKitEnabled` and `b.workDir` as `ContextDir` through to `BuildImageOpts`.

7. **Modify `BuildDefaultImage()`** — already detects BuildKit but doesn't use it. Pass through.

8. **Modify `buildRun()`** — already detects BuildKit but only warns. Capture and pass through.

### Acceptance Criteria

```bash
make test                          # All existing tests pass
go build ./...                     # Full compilation
```

### Wrap Up

1. Update Progress Tracker: Task 3 -> `complete`
2. Append key learnings
3. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the BuildKit support initiative. Read the Serena memory `buildkit-support-initiative` — Task 3 is complete. Begin Task 4: Conditional Dockerfile template."

---

## Task 4: Conditional Dockerfile Template (BuildKit vs Legacy)

**Creates/modifies:** `internal/build/templates/Dockerfile.tmpl`, `internal/build/dockerfile.go`, `internal/build/hash_test.go`
**Depends on:** Task 3

### Goal

When BuildKit is NOT available, the Dockerfile template should not emit `--mount=type=cache` directives (which cause the legacy builder to error). When BuildKit IS available, emit them for optimal caching.

### Implementation Phase

1. **Read:** `internal/build/templates/Dockerfile.tmpl` (identify all `--mount=type=cache` directives), `internal/build/dockerfile.go` (template data structs).

2. **Add `BuildKitEnabled` to template data struct** (likely `DockerfileContext`). Wire from caller.

3. **Wrap cache mount directives in template conditionals:**
   ```dockerfile
   {{- if .BuildKitEnabled}}
   RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
       --mount=type=cache,target=/var/lib/apt,sharing=locked \
       apt-get update && apt-get install -y {{.Packages}}
   {{- else}}
   RUN apt-get update && apt-get install -y {{.Packages}}
   {{- end}}
   ```
   Apply to all ~8 `--mount=type=cache` directives.

4. **Content hash impact:** Different hashes for BuildKit vs legacy is correct — structurally different images.

5. **Update tests:**
   - `TestContentHash_BuildKitVsLegacy` — different hashes
   - `TestContentHash_StableBuildKit` / `TestContentHash_StableLegacy` — stable within each mode
   - Existing `TestContentHash_MetadataStability` passes in both modes

### Acceptance Criteria

```bash
make test                          # All tests pass
# BuildKit mode: contains --mount=type=cache
# Legacy mode: NO --mount=type=cache
# Hashes differ between modes, stable within each
```

### Wrap Up

1. Update Progress Tracker: Task 4 -> `complete`
2. Append key learnings
3. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the BuildKit support initiative. Read the Serena memory `buildkit-support-initiative` — Task 4 is complete. Begin Task 5: Testing infrastructure and fakes."

---

## Task 5: Testing Infrastructure and Fakes

**Creates/modifies:** `pkg/whail/whailtest/fake_client.go`, `pkg/whail/buildkit/builder_test.go`, `internal/docker/dockertest/fake_client.go`, `internal/cmd/image/build/build_test.go`
**Depends on:** Tasks 1-4

### Goal

Ensure BuildKit integration is testable without a real Docker daemon at every layer.

### Implementation Phase

1. **whail level — closure-based faking (simplest):**
   No interface needed. Tests set `Engine.BuildKitImageBuilder` to a func literal:
   ```go
   var capturedOpts whail.ImageBuildKitOptions
   engine.BuildKitImageBuilder = func(ctx context.Context, opts whail.ImageBuildKitOptions) error {
       capturedOpts = opts
       return nil
   }
   ```

2. **Add whailtest helper:**
   ```go
   // whailtest.WithBuildKit returns an engine option or post-creation helper
   func FakeBuildKitBuilder() func(context.Context, whail.ImageBuildKitOptions) error
   ```

3. **Update `FakeClient` in `internal/docker/dockertest/`:**
   - Ensure `FakeClient.Engine` gets a fake BuildKit builder wired
   - Add setup helper: `SetupBuildKit()` or `SetupBuildKitBuilder()`

4. **Add unit tests:**
   - `TestImageBuildKit_LabelEnforcement` — managed labels injected, cannot be overridden (whail level)
   - `TestImageBuildKit_NilBuilder` — returns error (whail level)
   - `TestBuildImage_RoutesToBuildKit` — BuildKitEnabled=true routes to whail BuildKit (docker level)
   - `TestBuildImage_RoutesToLegacy` — BuildKitEnabled=false uses legacy (docker level)
   - Build command test with both modes using fakes

5. **Add integration tests** (Docker required):
   - `TestImageBuildKit_FullRoundTrip` — build Dockerfile, verify managed labels
   - `TestImageBuildKit_CacheMounts` — `--mount=type=cache` works
   - `TestImageBuildKit_ManagedVisibility` — built image visible in `ImageList` with managed filter

### Acceptance Criteria

```bash
make test                          # All unit tests pass (no Docker)
go test ./pkg/whail/... -v         # Whail tests pass
go test ./internal/docker/... -v   # Docker tests pass
```

### Wrap Up

1. Update Progress Tracker: Task 5 -> `complete`
2. Append key learnings
3. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the BuildKit support initiative. Read the Serena memory `buildkit-support-initiative` — Task 5 is complete. Begin Task 6: whail README.md."

---

## Task 6: whail README.md — Package Docs and BuildKit Extension Guide

**Creates:** `pkg/whail/README.md`
**Depends on:** Tasks 1-5

### Goal

Create a README.md for the whail package that describes what it does, how to use it, and how to enable BuildKit support via the buildkit subpackage.

### Implementation Phase

1. **Read key files for context:** `pkg/whail/CLAUDE.md` (API reference), `pkg/whail/engine.go` (Engine struct, constructors), `pkg/whail/types.go` (re-exported types), `pkg/whail/errors.go` (error types).

2. **Write `pkg/whail/README.md`** covering:

   a. **Overview** — What whail is: a reusable Docker engine wrapper with automatic label-based resource isolation. All operations (create, list, remove, build) are filtered through managed labels. Resources created outside whail are invisible; resources created by whail cannot escape.

   b. **Quick Start** — Minimal usage:
   ```go
   engine, err := whail.New(ctx)
   // All operations auto-tagged and filtered
   engine.ContainerCreate(ctx, opts)  // managed labels injected
   engine.ContainerList(ctx, opts)    // only managed containers returned
   engine.ImageBuild(ctx, reader, opts) // managed labels on image
   ```

   c. **Custom Configuration** — `EngineOptions`, `LabelConfig`, custom label prefixes:
   ```go
   engine, err := whail.NewWithOptions(ctx, whail.EngineOptions{
       LabelPrefix:  "com.myapp",
       ManagedLabel: "managed",
       Labels: whail.LabelConfig{...},
   })
   ```

   d. **Wrapping an Existing Client** — `NewFromExisting` for testing or custom clients.

   e. **BuildKit Extension** — How to enable BuildKit support:
   ```go
   import "github.com/anthropics/clawker/pkg/whail/buildkit"

   engine, _ := whail.New(ctx)
   engine.BuildKitImageBuilder = buildkit.NewImageBuilder(engine.APIClient)

   // Now BuildKit builds work with full label enforcement
   engine.ImageBuildKit(ctx, whail.ImageBuildKitOptions{
       Tags:       []string{"myimage:latest"},
       ContextDir: "./build-context",
   })
   ```

   Explain: why it's a separate import (dependency isolation), what you get (BuildKit Solve via Docker's embedded buildkitd, cache mounts, faster builds), label enforcement is automatic.

   f. **BuildKit Detection** — How to check if BuildKit is available:
   ```go
   enabled, err := whail.BuildKitEnabled(ctx, engine.APIClient)
   ```

   g. **Label Enforcement** — How managed labels work, the guarantee that labels can't be overridden, how filtering works.

   h. **Error Handling** — `DockerError` type with user-friendly messages and remediation steps.

   i. **Testing** — `whailtest` package overview, `FakeAPIClient`, how to fake BuildKit with a closure.

3. **Keep it practical** — code examples over prose. This is a Go package README, not marketing copy. Focus on "how do I use this" over "why is this great."

### Acceptance Criteria

```bash
# README exists and is well-structured
cat pkg/whail/README.md
# No compilation impact
make test
```

### Wrap Up

1. Update Progress Tracker: Task 6 -> `complete`
2. Append key learnings
3. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the BuildKit support initiative. Read the Serena memory `buildkit-support-initiative` — Task 6 is complete. Begin Task 7: Documentation and memory updates."

---

## Task 7: Documentation and Memory Updates

**Creates/modifies:** `pkg/whail/CLAUDE.md`, `internal/docker/CLAUDE.md`, `internal/build/CLAUDE.md`, `CLAUDE.md` (root), memories
**Depends on:** Tasks 1-6

### Goal

Update all CLAUDE.md files and memories to reflect the new BuildKit integration.

### Implementation Phase

1. **Update `pkg/whail/CLAUDE.md`:**
   - Document `BuildKitEnabled()`, `Pinger` interface
   - Document `Engine.BuildKitImageBuilder` closure field
   - Document `Engine.ImageBuildKit()` method and label enforcement
   - Document `ImageBuildKitOptions` type
   - Document `ErrBuildKitNotConfigured`
   - Add `buildkit.go` to file table
   - Document `buildkit/` subpackage: `NewImageBuilder()`, `NewBuildKitClient()`, `DockerDialer`
   - Update whailtest section with BuildKit closure faking

2. **Update `internal/docker/CLAUDE.md`:**
   - Remove `BuildKitEnabled`, `Pinger` docs (moved to whail)
   - Document `BuildImageOpts.BuildKitEnabled` and `ContextDir` fields
   - Document routing in `Client.BuildImage()` → whail BuildKit or legacy
   - Note: both paths go through whail for label enforcement

3. **Update `internal/build/CLAUDE.md`:**
   - Document `BuildKitEnabled` on `build.Options`
   - Document conditional Dockerfile template behavior
   - Note content hash differences between BuildKit and legacy modes

4. **Update root `CLAUDE.md`:** Note whail now supports BuildKit via subpackage extension pattern.

5. **Update `image-caching-optimization` memory:**
   - Mark "SDK vs CLI BuildKit" section as resolved
   - Update current state to reflect BuildKit support complete

6. **Update this memory:** Mark all tasks complete, final key learnings summary.

7. **Run freshness check:** `bash scripts/check-claude-freshness.sh`

### Acceptance Criteria

```bash
bash scripts/check-claude-freshness.sh   # No stale CLAUDE.md files
make test                                  # Tests still pass
```

### Wrap Up

1. Update Progress Tracker: Task 7 -> `complete`
2. **STOP.** Inform the user the initiative is complete:

> **Initiative complete.** All 7 tasks done. BuildKit support is integrated in `pkg/whail` via closure injection pattern — whail core has zero `moby/buildkit` imports, label enforcement works identically for both paths, and the `buildkit/` subpackage isolates the heavy dependencies. Cache mount directives in the Dockerfile template work with BuildKit and gracefully degrade for legacy builds. `pkg/whail/README.md` documents the package and BuildKit extension setup.
