# `clawker build` ↔ `docker build` Parity Initiative

## End Goal

Bring `clawker image build` (aliased as `clawker build`) to **flag-surface parity with `docker build`**.

The justification is "drop-in replacement" claim — users typing `clawker build` should get the docker CLI conventions they already know. Prune anything that exists for **packaging / deployment / distribution to remote systems** (clawker images are intended to run locally on the same daemon they were built in).

Holistic secondary goal: follow the standard build-CLI conventions for **proper metadata, tags, digest capture, attestations** — across the board, not just flag names. For a security-positioned tool, attestations (`--provenance`, `--sbom`) are **first-class even for local-only builds** because they provide audit trail + SBOM (foundation of CVE scanning), not just registry trust.

## Why — Standardization with Container CLI Builders

clawker is one of a tiny handful of projects that programmatically drive image builds via SDK (most consumers shell out to `docker build`). For users to treat `clawker build` as a drop-in replacement they don't have to relearn, it must match the conventions established by peer SDK-driving projects (docker CLI, podman, buildx, kaniko, ko, dagger, etc.) for:

- **Flag surface** (what users type)
- **Metadata capture** (digest extraction from `SolveResponse.ExporterResponse[ExporterImageDigestKey]`, legacy `aux.ID`)
- **Standard outputs** (`--iidfile`, `--metadata-file` shapes)
- **Tag application** (moby exporter `name=` attr, multi-tag via comma-separated)
- **Cache trust model** (delegate to daemon-side cache: BuildKit Solve or classic builder `probeCache` — don't invent client-side hash schemes)

### SDK Pattern Alignment Table (the rosetta stone driving this work)

| Aspect | Standard SDK pattern | Clawker today | Clawker dead path (now removed) |
|---|---|---|---|
| Apply user tags | exporter `name=` attr | ✅ same | — |
| Extract digest | parse `ExporterResponse[ExporterImageDigestKey]` (BuildKit) + `aux.ID` (legacy) | ✅ via `OnComplete` callback (this session) | ❌ ignored |
| Skip-rebuild | trust daemon-side cache (Solve / `probeCache`) is the lookup | ✅ implicit (Solve runs) | invented `:sha-<custom>` tag check |
| Content-addressed local ref | `--iidfile` (or `name-canonical=true` if moby honored it — it doesn't) | ✅ `--iidfile` flag (this session) | invented `:sha-<custom>` (not OCI digest) |

Original (before this session) had ❌ on rows 2 + 4. Both now ✅. Row 3 was where the prior author's dead path lived (`EnsureImage` + `ImageTagWithHash` with custom 12-hex-char short hash, never actually applied because EnsureImage was unreachable from `clawker build`).

### Peer-project references benchmarked

- **docker/cli** `cli/command/image/build.go` — flag parsing, ImageBuildOptions
- **moby/moby** `daemon/internal/builder-next/exporter/exporter.go imageExporterMobyWrapper` — confirms `name-canonical=true` is silently stripped; `aux.ID` in stream is the legacy digest source
- **moby/buildkit** `exporter/containerimage/export.go` — sets `ExporterResponse[ExporterImageDigestKey]` (`containerimage.digest`)
- **docker/buildx** `commands/build.go getImageID` — pattern for extracting digest from SolveResponse + writing `--iidfile`
- **containers/podman** `libimage/normalize.go NormalizeName` — defaults `:latest` if tag missing (same convention)

## Branch State (as of save)

- Branch: `feat/image-optimization`
- HEAD commit: `bb51901c feat(bundler): npm-resolved Claude Code version + Dockerfile.tmpl cache locality`
- **Uncommitted changes are substantial** — entire dead `EnsureImage` chain deleted + BuildKit digest capture wired. See "Already Done" below.

## Already Done (uncommitted, all tests pass — `go test ./internal/docker/... ./internal/bundler/... ./pkg/whail/...` green)

1. **Deleted dead `EnsureImage` chain** (was test-only since PR #139, ~3.5 months ago):
   - `Builder.EnsureImage` + 5 `TestEnsureImage_*` tests
   - `internal/docker/names.go::ImageTagWithHash` + its test
   - `internal/docker/client.go::TagImage` method
   - `internal/bundler/hash.go` + `hash_test.go` (entire files deleted)
   - `internal/bundler/dockerfile.go::EmbeddedScripts` function
   - `internal/hostproxy/internals/embed.go::AllScripts` function
   - `pkg/whail/image.go::Engine.ImageTag` + `TestImageTag`
   - `BuilderOptions.ForceBuild` field (only EnsureImage read it)
   - All related CLAUDE.md docs updated (`internal/bundler/`, `internal/docker/`, `pkg/whail/`, `internal/hostproxy/internals/`)
   - Stale comment in `internal/cmd/image/build/build.go:232` removed

2. **BuildKit `SolveResponse` digest capture** (was dropped on floor — `_, err = bkClient.Solve(...)`):
   - Added `whail.BuildResult{ImageID string}` + `whail.BuildCompleteFunc` types in `pkg/whail/types.go`
   - Added `OnComplete BuildCompleteFunc` field to `whail.ImageBuildKitOptions`
   - `pkg/whail/buildkit/builder.go` parses `resp.ExporterResponse[exptypes.ExporterImageDigestKey]` and fires `OnComplete`
   - Legacy path: extended `buildEvent` struct with `Aux.ID`, threaded `onComplete` through all three `processBuildOutput*` functions in `internal/docker/client.go`
   - `BuildImageOpts` + `BuilderOptions` both have `OnComplete` field, plumbed through `toBuildImageOpts`
   - `internal/cmd/image/build/build.go` wires `OnComplete` to:
     - log `image_id` at Info level
     - write to `--iidfile PATH` (new flag, same shape as `docker buildx --iidfile`)
   - Verified empirically: file matches `docker image inspect`'s `.Id`

3. **`--iidfile` flag added** to `clawker image build` (the only flag-surface addition so far).

4. **Reverted UAT marker** on `internal/bundler/assets/clawker-agent-prompt.md` (was sitting at marker `BBBBB` from prior session). Now clean.

## Critical Confusions to Avoid (lessons learned)

1. **`docker build` ≠ `docker buildx` ≠ `docker buildx build`.** Three distinct flag surfaces. Modern docker routes `docker build` through buildx but with a curated flag subset.
2. **NEVER trust deepwiki for "list all flags of X"** — it synthesizes from mixed sources (legacy + buildx) without delineation. Agent got burned twice making up flags (`--security-opt`, `--memory`, `--pids-limit`, etc. — none of these are in `docker buildx build`).
3. **ALWAYS get authoritative `--help` output directly** via `Bash` against an actual docker binary, not via deepwiki.
4. **Attestations are NOT distribution-only for security tools.** `--provenance` = build audit trail. `--sbom` = vuln-scan foundation. Both have local-only value.
5. **`name-canonical=true` exporter attribute is silently stripped by moby's wrapper** (confirmed in `daemon/internal/builder-next/exporter/exporter.go imageExporterMobyWrapper`). RepoDigests for local builds is genuinely unsatisfiable via this path. The standard alternative is `--iidfile` (which is now wired).

## TODOs

### Phase 0 — Commit current work
- [ ] Decide: commit cleanup + iidfile as their own PR/commit on this branch, OR bundle with parity work?
- [ ] Recommended: commit now (mechanical cleanup, low risk, sizable diff). Parity work is a fresh diff on top.

### Phase 1 — Get authoritative flag list (DO NOT SKIP)
- [x] `docker build --help` output captured in chat (preserved in Flag Inventory section below).
- [ ] If anything ambiguous about a flag, run `docker build --help` again on user's machine — never synthesize via deepwiki.

### Flag Inventory (in-flight; refine after Phase 1 legacy `docker build --help` capture)

**Authoritative `docker build --help` output user pasted in chat (DO NOT re-synthesize via deepwiki):**

```
docker build [OPTIONS] PATH | URL | -

      --add-host strings              Add a custom host-to-IP mapping (format: "host:ip")
      --allow stringArray             Allow extra privileged entitlement (network.host, security.insecure, device)
      --annotation stringArray        Add annotation to the image
      --attest stringArray            Attestation parameters (format: "type=sbom,generator=image")
      --build-arg stringArray         Set build-time variables
      --build-context stringArray     Additional build contexts (e.g., name=path)
      --builder string                Override the configured builder instance
      --cache-from stringArray        External cache sources
      --cache-to stringArray          Cache export destinations
      --call string                   Set method for evaluating build ("check", "outline", "targets")
      --cgroup-parent string          Set the parent cgroup for "RUN" instructions during build
      --check                         Shorthand for "--call=check"
  -D, --debug                         Enable debug logging
  -f, --file string                   Name of the Dockerfile (default: "PATH/Dockerfile")
      --iidfile string                Write the image ID to a file
      --label stringArray             Set metadata for an image
      --load                          Shorthand for "--output=type=docker"
      --metadata-file string          Write build result metadata to a file
      --network string                Set the networking mode for "RUN" instructions
      --no-cache                      Do not use cache when building the image
      --no-cache-filter stringArray   Do not cache specified stages
  -o, --output stringArray            Output destination
      --platform stringArray          Set target platform for build
      --policy stringArray            Policy configuration
      --progress string               Set type of progress output (auto/none/plain/quiet/rawjson/tty)
      --provenance string             Shorthand for "--attest=type=provenance"
      --pull                          Always attempt to pull all referenced images
      --push                          Shorthand for "--output=type=registry,unpack=false"
  -q, --quiet                         Suppress build output and print image ID on success
      --sbom string                   Shorthand for "--attest=type=sbom"
      --secret stringArray            Secret to expose to the build
      --shm-size bytes                Shared memory size for build containers
      --ssh stringArray               SSH agent socket or keys to expose to the build
  -t, --tag stringArray               Image identifier
      --target string                 Set the target build stage to build
      --ulimit ulimit                 Ulimit options
```

**User goal: `docker build == clawker build` parity, flag-for-flag where applicable.**

### Already on clawker

`-f/--file`, `-t/--tag`, `--no-cache`, `--pull`, `--build-arg`, `--label`, `--target`, `-q/--quiet`, `--progress`, `--network`, `--iidfile` (added this session)

### Tentative keep (in-flight, subject to legacy-help recheck)

| Flag | Rationale |
|---|---|
| `--add-host` | Build-time DNS — corp / internal mirror IPs |
| `--allow` | Default-deny privileged entitlements. Matches clawker's security posture. |
| `--attest`, `--sbom`, `--provenance` | Security tool needs audit trail + SBOM for CVE scanning. Local-relevant. |
| `--annotation` | OCI manifest annotations — local tooling can read via `docker inspect` |
| `--build-context` | Multi-source builds (monorepo overlays) |
| `--cache-from type=local`, `--cache-to type=local` | Filesystem-backed cache sharing across dev machines without registry |
| `--call`, `--check` | Lint/outline modes for generated Dockerfile |
| `--cgroup-parent` | Edge case but cheap |
| `--metadata-file` | JSON output superset of `--iidfile`; standard tool integration |
| `--no-cache-filter` | Selective stage cache invalidation |
| `-D/--debug` | Debug logging (align name with docker) |
| `--output type=local`/`type=tar` | Forensic extraction of built filesystem for security inspection |
| `--platform` | Cross-arch via QEMU emulation for security-regression testing |
| `--policy` | BuildKit policy enforcement — security tool concern |
| `--secret` | Build-time creds without baking into layers |
| `--shm-size` | `/dev/shm` cap — DoS defense during build |
| `--ssh` | SSH agent forwarding for private git pulls |
| `--ulimit` | nofile/nproc caps — fork-bomb / fd-exhaustion defense |

### Tentative drop (distribution / non-applicable)

| Flag | Reason |
|---|---|
| `--push` | Pure registry upload |
| `--load` | Default for clawker (we always load); redundant |
| `--builder` | Builder-instance management; we own that |
| `--output type=registry/oci` | Registry push variants only |
| `--cache-from`/`--cache-to type=registry` | Registry-backed cache |

### Output-shape gaps (separate from flag adds)

- `--quiet` currently suppresses everything; should print image ID to stdout on success (docker `-q` spec)
- `BUILDKIT_PROGRESS` env not honored (mirror our `--progress` flag)

### Agent's prior errors on this list (avoid repeats)

- Made up `--security-opt`, `--memory`, `--memory-swap`, `--pids-limit`, `--cpu-shares`, `--cpu-period`, `--cpu-quota`, `--cpuset-cpus`, `--cpuset-mems`, `--isolation`. **NONE in `docker build`.** Conflated with `docker run` / `docker container create` surface.
- Initially dismissed `--security-opt` then "corrected" by keeping it — both wrong, flag doesn't exist on `docker build` at all.
- Initially dismissed attestation flags (`--provenance`, `--sbom`, `--attest`) as distribution-only — wrong for security tool; reinstated.

### Phase 2 — Categorize each flag (with proper rigor this time)
Apply two filters in order:
1. **Distribution / packaging / deployment** → drop. Examples: `--push`, `--load`, `--builder`, `--cache-to type=registry`.
2. **Useful for local dev / security tool** → keep, even if it has a distribution angle. Examples: `--provenance` (audit), `--sbom` (CVE scan), `--cache-from type=local`, `--output type=local` (forensic extraction).

### Phase 3 — Implementation (per-flag, mechanical)
For each kept flag:
- [ ] Add to `BuildOptions` in `internal/cmd/image/build/build.go`
- [ ] Register cobra flag with help text matching docker CLI's wording verbatim
- [ ] Plumb through `BuilderOptions` (`internal/docker/builder.go`) → `BuildImageOpts` (`internal/docker/client.go`) → `whail.ImageBuildKitOptions` (`pkg/whail/types.go`) → BuildKit Solve attrs (`pkg/whail/buildkit/solve.go`)
- [ ] Add test
- [ ] Verify behavior matches docker CLI (run `docker build` with same flag, compare result)

### Phase 4 — Output-shape parity
- [ ] Fix `--quiet`: today suppresses all output; should print just image ID to stdout on success (matches `docker build -q`)
- [ ] Honor `BUILDKIT_PROGRESS` env (currently we only honor `--progress` flag, not the env equivalent)

### Phase 5 — Resume the paused UAT (separate concern, see also `dockerfile_cache_optimization_uat` memory)
- [ ] Test 1 still pending: modify `internal/bundler/assets/clawker-agent-prompt.md` (real edit, no fake marker accumulation), build, observe expected invalidation (steps 19–27), revert immediately
- [ ] Tests 2–5 same pattern for host-open.sh, claude-settings.json, clawkerd binary, socket-server main.go
- [ ] Confirm with user before each test (per existing UAT memory's instruction)

## Plan File Reference

There IS a prior plan file documenting the cache-optimization design that this branch implemented:
`/home/claude/.claude/plans/internal-bundler-assets-dockerfile-tmpl-imperative-teacup.md`

That plan is already implemented (`ARG CLAUDE_CODE_VERSION` + three-tier asset placement). The remaining UAT (Phase 5 above) is validation, not implementation.

## Files Modified in This Session (uncommitted — `git status` to inspect)

Code:
- `internal/docker/builder.go` (EnsureImage deleted, ForceBuild field deleted, OnComplete plumb)
- `internal/docker/client.go` (TagImage deleted, BuildImageOpts.OnComplete added, aux parsing, processBuildOutput* signatures)
- `internal/docker/names.go` (ImageTagWithHash deleted)
- `internal/docker/names_test.go` (TestImageTagWithHash deleted)
- `internal/docker/builder_test.go` (5 TestEnsureImage_* + helpers deleted, imports cleaned)
- `internal/docker/client_progress_test.go` (nil arg added to processBuildOutputWithProgress calls)
- `internal/bundler/hash.go` (DELETED — file)
- `internal/bundler/hash_test.go` (DELETED — file)
- `internal/bundler/dockerfile.go` (EmbeddedScripts deleted, imports cleaned, stale comment fixed)
- `pkg/whail/image.go` (Engine.ImageTag deleted)
- `pkg/whail/image_test.go` (TestImageTag deleted)
- `pkg/whail/types.go` (BuildResult + BuildCompleteFunc + OnComplete field added)
- `pkg/whail/buildkit/builder.go` (exptypes import + SolveResponse digest capture)
- `internal/hostproxy/internals/embed.go` (AllScripts deleted, doc comment cleaned)
- `internal/cmd/image/build/build.go` (--iidfile flag, OnComplete callback, ForceBuild assignment removed, stale comment removed)

Docs:
- `internal/bundler/CLAUDE.md`
- `internal/docker/CLAUDE.md`
- `pkg/whail/CLAUDE.md`
- `internal/hostproxy/internals/CLAUDE.md`

## SDK Pattern Status (after this session)

| Aspect | Standard SDK pattern | Clawker now |
|---|---|---|
| Apply user tags | exporter `name=` attr | ✅ |
| Extract digest | parse `ExporterResponse[ExporterImageDigestKey]` (BuildKit) + `aux.ID` (legacy) | ✅ via `OnComplete` |
| Skip-rebuild | trust daemon-side cache (BuildKit Solve / classic `probeCache`) | ✅ implicit |
| Content-addressed local ref | `--iidfile` writes digest to file | ✅ |

## IMPORTANT

- **Always check with the user before proceeding with the next TODO item.** Each phase / flag addition needs explicit user signoff because there are decisions about scope, naming, behavior alignment with docker CLI's exact wording.
- **DELETE THIS MEMORY when work complete.** Once docker build parity scope is finalized + implemented + UAT completed, ask user if they want to delete this memory via `mcp__serena__delete_memory` with name `docker_build_parity_initiative`. This memory captures in-flight investigation state, not durable project facts.
