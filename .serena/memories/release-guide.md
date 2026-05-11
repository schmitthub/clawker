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
4. **Setup Docker Buildx** — required for the pinned multi-stage build that produces the embedded firewall stack binaries
5. **`make release-embeds`** — builds both linux arch embed sets (amd64 + arm64). Three of the four binaries (clawker-cp, ebpf-manager, coredns-clawker) go through the pinned `Dockerfile.controlplane` chain; clawkerd is a plain CGO_ENABLED=0 host-Go cross-compile (pure Go, no BPF). Stages them under `embeds/{amd64,arm64}/` outside `dist/`. Asserts ELF magic+class+endianness+e_machine on each staged binary — silent wrong-arch is unrecoverable once published.
6. **Install cosign** (sigstore) + **syft** (SBOM generation)
7. **GoReleaser v2** — `goreleaser release --clean --parallelism 1`
   - Two build IDs: `clawker-amd64`, `clawker-arm64`. Each has a `hooks.pre` calling `make stage-embeds-<arch>` to swap the matching arch's embeds into `assets/` paths immediately before that build's `go build` runs.
   - `--parallelism 1` is REQUIRED — the build IDs share the `assets/` paths and would race otherwise.
   - Builds 4 platform binaries (CGO_ENABLED=0, pure-Go cross-compile of `./cmd/clawker`)
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
- `checksums.txt.sigstore.json` — cosign bundle (signature + certificate in Sigstore bundle format)
- `clawker_VERSION_OS_ARCH.tar.gz.sbom.json` — SBOM per archive (syft)
- Auto-generated changelog (excludes docs/test/ci/chore commits and merge commits)

## Prerelease Convention

Tags with a `-suffix` (e.g. `v1.0.0-rc.1`, `v1.0.0-beta.2`) are **automatically marked as prerelease** on GitHub via GoReleaser's `prerelease: auto` setting.

## Version Injection

GoReleaser injects version via ldflags:
```
-X github.com/schmitthub/clawker/internal/build.Version={{.Version}}
-X github.com/schmitthub/clawker/internal/build.Date={{.CommitDate}}
```

These set `build.Version` and `build.Date` in `internal/build/build.go`. Without ldflags, `Version` defaults to `"DEV"` with a `debug.ReadBuildInfo()` fallback. Dev builds via `make clawker` leave `build.Date` empty.

## Cosign Verification

After a release, verify the goreleaser checksum signature. The signing identity is anchored to the reusable workflow `release-build.yml` (where the cosign sign step actually runs) at the release tag, NOT the caller `release.yml`. Substring-only regexes (e.g. `'github\.com/schmitthub/clawker'`) are insufficient — they would also match a forged attestation produced by any other workflow file in this repo.

```bash
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --new-bundle-format \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/schmitthub/clawker/\.github/workflows/release-build\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9._-]+)?(\+[A-Za-z0-9._-]+)?$' \
  checksums.txt
```

`--new-bundle-format` is required for bundles emitted by `actions/attest@v4` / `gh attestation download` (`mediaType: application/vnd.dev.sigstore.bundle.v0.3+json`). cosign v3 defaults to the legacy format and rejects v0.3 without the flag.

For the SLSA build-provenance and SPDX SBOM attestations (one per release artifact), prefer the GitHub-side verifier — it auto-fetches the bundle:

```bash
gh attestation verify <artifact> \
  --owner schmitthub \
  --signer-workflow schmitthub/clawker/.github/workflows/release-build.yml
```

`gh attestation verify` returns no stdout on success (only an exit code). Use `--format json` + `jq` if you want to surface the subject digest, builder ID, or signing identity.

## Local Build

```bash
make clawker              # Build for current platform with version from git describe
make release-embeds       # Build both linux arch embed sets via pinned Docker chain
```

## Key Files

| File | Purpose |
|------|---------|
| `.github/workflows/release.yml` | Release workflow (trigger: `v*` tag push) |
| `.goreleaser.yaml` | GoReleaser v2 config (two build IDs + per-arch pre-hooks) |
| `internal/build/build.go` | Build-time metadata vars (`Version`, `Date`) |
| `Makefile` | `release-embeds` (pinned Docker), `stage-embeds-{amd64,arm64}` (per-build swap) |
| `Dockerfile.controlplane` | Pinned multi-stage recipe for the four linux embeds |
