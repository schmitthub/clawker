# Release Build Guide

Quick reference for triggering and understanding Clawker's release pipeline.

## How to Trigger a Release

Push a semver-compliant tag (`v*`) to `origin`. The GitHub Actions workflow `.github/workflows/release.yml` runs automatically.

```bash
# 1. Ensure main is green (CI passing)
# 2. Tag must be on the main branch

git tag v1.0.0          # or v1.0.0-rc.1 for prerelease
git push origin v1.0.0
```

## Prerequisites

- **Main must be green** — all CI checks passing on main
- **Tag must be on main** — the workflow validates with `git merge-base --is-ancestor`
- **Tag must match semver** — regex: `^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9._-]+)?(\+[a-zA-Z0-9._-]+)?$`

## What Happens Automatically

1. **Checkout** with full history (`fetch-depth: 0`)
2. **Validate** tag is semver and on main
3. **Setup Go** from `go.mod` version
4. **Verify build** — `go build ./cmd/clawker`
5. **Install cosign** (sigstore) + **syft** (SBOM generation)
6. **GoReleaser v2** — `goreleaser release --clean`
   - Pre-hooks: `go mod tidy`, `go generate ./...`
   - Builds 4 platform binaries (CGO_ENABLED=0)
   - Creates tar.gz archives with LICENSE + README.md
   - Generates SHA256 checksums
   - Signs checksums with cosign (keyless, OIDC)
   - Generates SBOMs via syft for each archive
   - Creates GitHub Release with changelog

## Platforms Built

| OS | Arch |
|----|------|
| linux | amd64 |
| linux | arm64 |
| darwin | amd64 |
| darwin | arm64 |

## Artifacts Produced

- `clawker_VERSION_OS_ARCH.tar.gz` — binary + LICENSE + README.md (4 archives)
- `checksums.txt` — SHA256 checksums of all archives
- `checksums.txt.sig` — cosign signature
- `checksums.txt.pem` — cosign certificate
- `clawker_VERSION_OS_ARCH.tar.gz.sbom.json` — SBOM per archive (syft)
- Auto-generated changelog (excludes docs/test/ci/chore commits and merge commits)

## Prerelease Convention

Tags with a `-suffix` (e.g. `v1.0.0-rc.1`, `v1.0.0-beta.2`) are **automatically marked as prerelease** on GitHub via GoReleaser's `prerelease: auto` setting.

## Version Injection

GoReleaser injects version via ldflags:
```
-X github.com/schmitthub/clawker/internal/build.Version={{.Version}}
-X github.com/schmitthub/clawker/internal/build.Date={{.Date}}
```

These set `build.Version` and `build.Date` in `internal/build/build.go`. Without ldflags, `Version` defaults to `"DEV"` with a `debug.ReadBuildInfo()` fallback.

## Cosign Verification

After a release, verify artifact signatures:

```bash
cosign verify-blob \
  --signature checksums.txt.sig \
  --certificate checksums.txt.pem \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp 'github\.com/schmitthub/clawker' \
  checksums.txt
```

## Local Build

```bash
make clawker              # Build for current platform with version from git describe
make clawker-build-all    # Cross-compile for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
```

Local builds use the same ldflags pattern. Version is derived from `git describe --tags --always --dirty`.

## Key Files

| File | Purpose |
|------|---------|
| `.github/workflows/release.yml` | Release workflow (trigger: `v*` tag push) |
| `.goreleaser.yaml` | GoReleaser v2 config (builds, archives, signing, changelog) |
| `internal/build/build.go` | Build-time metadata vars (`Version`, `Date`) |
| `Makefile` | Local build targets with ldflags injection |
