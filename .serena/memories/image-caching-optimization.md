# Docker Image Caching Optimization

**Status:** Completed (2026-02-01)
**Branch:** `a/image-caching`
**Tests:** 2696 pass, 0 failures

## Problem Solved

Content-addressed image caching in `EnsureImage` was **broken**: `ImageLabels()` at `internal/docker/labels.go:84` embeds `time.Now()` into the Dockerfile template, making the content hash different on every invocation. The cache never hit.

Beyond this bug, many config changes (env vars, labels, firewall domains, EXPOSE, VOLUME, HEALTHCHECK) produced different Dockerfile output and thus different hashes, even though they only affect cheap metadata layers that don't need rebuilds.

## Strategy Applied

Moved all non-structural values out of the Dockerfile template. They are now injected at:
- **Container creation time** — env vars via `docker.RuntimeEnv(cfg)` 
- **Docker build API** — labels via `opts.Labels` in `Builder.mergeImageLabels()`

The Dockerfile is now purely structural (base image, packages, RUN, COPY), making the content hash stable across config-only changes.

## Key Changes

### Dockerfile Template (`internal/build/templates/Dockerfile.tmpl`)
- **Removed:** Labels (2 blocks), ENV (firewall, editor, user-defined, agent), EXPOSE, VOLUME, custom HEALTHCHECK, SHELL
- **Added:** BuildKit cache mounts for Go modules (ssh-proxy-builder), npm (Claude Code install), git-delta download
- **Moved:** User COPY instructions to after Claude Code install (better layer caching)
- **Kept:** Default ready-file HEALTHCHECK, all structural instructions

### Structs Slimmed (`internal/build/dockerfile.go`)
- `DockerfileContext`: Removed 7 fields (FirewallDomains, FirewallDomainsJSON, FirewallOverride, ExtraEnv, Editor, Visual, ImageLabels)
- `DockerfileInstructions`: Removed 6 fields (Env, Labels, Expose, Volumes, Healthcheck, Shell) — now only has Copy, Args, UserRun, RootRun
- Removed types: `ExposeInstruction`, `HealthcheckInstruction`
- Removed functions: `convertExposeInstructions`, `convertHealthcheck`, `domainsToJSON`

### New Runtime Env Injection (`internal/docker/env.go`)
```go
func RuntimeEnv(cfg *config.Config) []string
```
Returns: EDITOR, VISUAL, CLAWKER_FIREWALL_DOMAINS, CLAWKER_FIREWALL_OVERRIDE, agent env, instruction env. Called at container creation time via Factory closure.

### Factory Wiring
- `cmdutil.Factory` — added `RuntimeEnv func() []string` field
- `cmd/factory/default.go` — wired `f.RuntimeEnv` closure calling `docker.RuntimeEnv(cfg)`
- `container/run/run.go` and `container/create/create.go` — added `RuntimeEnv` to Options, injected after git credential setup

### Labels via Build API (`internal/build/build.go`)
- Added `mergeImageLabels()` method on `Builder`
- `Build()` merges clawker internal labels + user-defined labels into `opts.Labels`
- Labels applied via Docker build API, not in Dockerfile

### Tests Added
- `internal/docker/env_test.go` — 8 tests for RuntimeEnv (defaults, editor, firewall, agent env, instruction env, determinism)
- `internal/build/hash_test.go` — `TestContentHash_MetadataStability` verifying env/label/editor/firewall/project changes don't affect hash
- Removed: `TestDomainsToJSON` from `firewall_test.go` (function moved to docker/env.go)

## Files Modified (12 total)

| File | Type |
|------|------|
| `internal/build/templates/Dockerfile.tmpl` | Modified |
| `internal/build/dockerfile.go` | Modified |
| `internal/build/build.go` | Modified |
| `internal/build/hash_test.go` | Modified |
| `internal/build/firewall_test.go` | Modified |
| `internal/docker/env.go` | **New** |
| `internal/docker/env_test.go` | **New** |
| `internal/cmdutil/factory.go` | Modified |
| `internal/cmd/factory/default.go` | Modified |
| `internal/cmd/container/run/run.go` | Modified |
| `internal/cmd/container/create/create.go` | Modified |
| `internal/cmd/container/run/run_test.go` | Modified |

## Key Learnings for Future Agents

### 1. Content Hash Sensitivity
The content hash (`internal/build/hash.go`) hashes the **rendered Dockerfile bytes**. Anything in the template output changes the hash. This is why `time.Now()` in labels was catastrophic — it's rendered into the Dockerfile string. Always ask: "Does this value appear in the rendered Dockerfile? If so, will it change between invocations?"

### 2. Dockerfile Template = Structural Only
The Dockerfile template should ONLY contain instructions that affect the image filesystem (FROM, RUN, COPY, USER, WORKDIR, ARG, static ENV like PATH/SHELL). Config-dependent metadata belongs elsewhere:
- **Labels** → Docker build API `opts.Labels`
- **Runtime ENV** → `docker.RuntimeEnv()` at container creation
- **EXPOSE/VOLUME/HEALTHCHECK/SHELL** → container config at runtime

### 3. Factory Closure Pattern
Adding a new dependency to commands follows this pattern:
1. Add field to `cmdutil.Factory` struct (pure data, no methods)
2. Wire closure in `cmd/factory/default.go`
3. Add to command Options struct
4. Wire in `NewCmd` from `f.XXX`
5. Call in run function
6. Add to test factory helper (e.g., `func() []string { return nil }`)

### 4. Test Factory Pattern
Each command package has its own `testFactory` helper (NOT shared). When adding new Factory fields, you must also update the test factory in affected `*_test.go` files. Missing this causes compile errors.

### 5. BuildKit Cache Mounts
Added `--mount=type=cache,target=...` for:
- `/go/pkg/mod` (Go module cache for ssh-proxy-builder stage)
- `/home/${USERNAME}/.npm` (npm cache for Claude Code install)
- `/tmp/downloads` (git-delta .deb download cache)

These survive across builds and significantly speed up rebuilds. They work with `# syntax=docker/dockerfile:1` at the top of the template.

### 6. Layer Ordering Matters
User COPY instructions were moved from before firewall scripts to after Claude Code install. This means user file changes only invalidate the last few layers, not the expensive Claude Code install layer. The general principle: put the most frequently changing instructions last.

### 7. Struct Field Removal Cascade
When removing fields from `DockerfileContext` or `DockerfileInstructions`, you must update:
- The struct definition
- All struct literal constructors (both `createContext` and `buildContext`)
- Template references (`.FieldName` in Dockerfile.tmpl)
- Conversion functions (remove if unused)
- Types (remove if unused)
- Imports (remove if unused)
- Tests that reference the removed fields

### 8. domainsToJSON Migration
`domainsToJSON` was removed from `internal/build/dockerfile.go` since the Dockerfile no longer needs firewall domain JSON. The equivalent logic now lives inline in `docker.RuntimeEnv()` using `json.Marshal` directly. The test `TestDomainsToJSON` in `firewall_test.go` was removed accordingly.
