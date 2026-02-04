# Bugfix: Firewall IP Range Sources + BuildKit --no-cache

## End Goal
Fix two related bugs preventing Go package downloads from `storage.googleapis.com`:
1. Content hash doesn't include embedded scripts (causes stale images)
2. BuildKit `--no-cache` doesn't work as expected

## Root Cause Analysis (COMPLETED)

### Problem 1: Old Firewall Script in Container
- Container had OLD `init-firewall.sh` that only fetches GitHub IP ranges
- NEW script (in codebase) supports configurable `ip_range_sources` including `google`
- User's `clawker.yaml` correctly has `google` IP range source configured
- But container was built with old image

### Problem 2: Why Rebuild Didn't Help
Two potential causes identified:

**A) Content Hash Bug (affects `EnsureImage()`):**
- `ContentHash()` in `internal/bundler/hash.go` only hashes:
  - Dockerfile content
  - `agent.includes` files
- Does NOT hash embedded scripts (`FirewallScript`, `EntrypointScript`, etc.)
- When scripts change but Dockerfile template doesn't, hash stays same
- `EnsureImage()` skips rebuild if hash-tagged image exists
- NOTE: `clawker image build` uses `Build()` directly (no hash check)

**B) BuildKit no-cache Bug:**
- BuildKit's `no-cache` attribute works differently than legacy builder
- Per [moby/buildkit#2409](https://github.com/moby/buildkit/issues/2409): "BuildKit's --no-cache does not disable cache, but instead verifies cache"
- Current implementation sets `attrs["no-cache"] = ""` in `pkg/whail/buildkit/solve.go:43-44`
- This may not be sufficient for complete cache invalidation

## Code Locations

| File | Purpose |
|------|---------|
| `internal/bundler/hash.go` | `ContentHash()` - needs to include embedded scripts |
| `internal/bundler/dockerfile.go` | Embedded scripts: `FirewallScript`, `EntrypointScript`, etc. |
| `internal/docker/builder.go` | `EnsureImage()` (hash check), `Build()` (no hash check) |
| `pkg/whail/buildkit/solve.go` | `toSolveOpt()` - sets `no-cache` attribute |
| `internal/cmd/image/build/build.go` | Build command - uses `Build()` directly |

## Embedded Scripts (from dockerfile.go)
```go
//go:embed assets/entrypoint.sh
var EntrypointScript string

//go:embed assets/init-firewall.sh
var FirewallScript string

//go:embed assets/statusline.sh
var StatuslineScript string

// ... and others
```

## Implementation Plan

### TODO 1: Fix Content Hash to Include Embedded Scripts ✅ DONE
- [x] Identified the bug in `internal/bundler/hash.go`
- [x] Added `EmbeddedScripts()` helper in `internal/bundler/dockerfile.go`
- [x] Modified `ContentHash()` to accept and hash `embeddedScripts []string` parameter
- [x] Updated all callers (builder.go, builder_test.go, hash_test.go)
- [x] Added tests: `TestContentHash_EmbeddedScripts`, `TestContentHash_EmbeddedScriptsHelper`

### TODO 2: Fix BuildKit no-cache ✅ DONE
- [x] Identified that `no-cache` frontend attribute may not be sufficient
- [x] Set `CacheImports = []bkclient.CacheOptionsEntry{}` when NoCache=true
- [x] Updated `pkg/whail/buildkit/solve.go`
- [x] Added test `TestToSolveOpt_NoCacheOff` to verify behavior

### TODO 3: Make --no-cache Also Set ForceBuild ✅ DONE
- [x] In `internal/cmd/image/build/build.go`, set `ForceBuild: opts.NoCache`
- [x] This ensures hash check is skipped when user explicitly requests no-cache

### TODO 4: Verify Fixes
- [ ] Build new clawker binary
- [ ] Test `clawker build --no-cache`
- [ ] Verify new container has updated `init-firewall.sh`
- [ ] Verify `storage.googleapis.com` is reachable

**All unit tests pass (2936 tests).**

## Workaround (User Can Use Now)
```bash
# Delete existing hash-tagged images
docker images | grep clawker
docker rmi clawker-<project>:sha-<hash>

# Or remove all project images
docker images -q "clawker-*" | xargs -r docker rmi -f

# Then rebuild
clawker build
```

## Key Findings
- `clawker image build` calls `builder.Build()` directly (no content hash check)
- `EnsureImage()` is designed for implicit builds (like `clawker run @`)
- BuildKit documentation: [moby/buildkit#4437](https://github.com/moby/buildkit/issues/4437), [moby/buildkit#2409](https://github.com/moby/buildkit/issues/2409)

---

**IMPORTANT:** Before proceeding with any TODO item, check with the user first. When all work is complete, ask if this memory should be deleted.
