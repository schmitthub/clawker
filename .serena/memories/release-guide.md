# Release Build Guide

Quick reference for triggering and understanding Clawker's release pipeline.

## How to Trigger a Release

Push a semver-compliant tag (`v*`) to `origin`. The caller workflow `.github/workflows/release.yml` runs automatically and delegates the build + attest stages to the reusable `release-build.yml` in the same repo.

```bash
# 1. Ensure main is green (CI passing)
# 2. Tag must be on the main branch

git tag v1.0.0          # or v1.0.0-rc.1 for prerelease
git push origin v1.0.0
```

## Prerequisites

- **Main must be green** — all CI checks passing on main
- **Tag must be on main** — the caller workflow validates with `git merge-base --is-ancestor`
- **Tag must match semver** — regex: `^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9._-]+)?(\+[a-zA-Z0-9._-]+)?$`

## Workflow split: caller + reusable

The pipeline is split across two same-repo workflow files:

- **Caller — `.github/workflows/release.yml`** (~51 lines). `on: push tags v*`. Two jobs: `validate` (semver + on-main checks; `contents: read` only) → `build` (`needs: validate`; declares the full permission superset; `uses: ./.github/workflows/release-build.yml`; passes only `HOMEBREW_TAP_GITHUB_TOKEN` explicitly — NOT `secrets: inherit`).
- **Reusable — `.github/workflows/release-build.yml`** (~180 lines). `on: workflow_call` with `HOMEBREW_TAP_GITHUB_TOKEN` required. Single `build` job, no internal `permissions:` block (inherits from caller's job). All build, sign, and attest steps live here.

The split provides same-repo **SLSA Build L3 isolation**: signing happens in the reusable workflow's isolated job context, so the Fulcio cert SAN cryptographically binds to `release-build.yml@refs/tags/<tag>` and cannot be impersonated by any other workflow file in this repo. The caller's path leaks into the predicate's `externalParameters.workflow.path` but does NOT control the signing identity.

**Calling job permissions** (must declare full superset — reusable inherits from the calling JOB):

```yaml
permissions:
  contents: write          # goreleaser publishes the GitHub release
  id-token: write          # Sigstore OIDC token for attestation signing
  attestations: write      # GitHub attestation API write
  artifact-metadata: write # NEW in actions/attest@v4 — required even via wrapper
```

## What Happens Automatically (reusable workflow)

1. **Record build start** — exports `BUILD_STARTED_ON` to `$GITHUB_ENV` as the first step. Actions has no native "job started" timestamp; we capture our own.
2. **Compute release version** — strips the leading `v` from `GITHUB_REF_NAME` into `$VERSION` so subsequent `with:` blocks can reference `${{ env.VERSION }}` (Actions `with:` doesn't run a shell).
3. **Checkout** with full history (`fetch-depth: 0`).
4. **Setup Go** from `go.mod` version.
5. **Setup Docker Buildx** — required for the pinned multi-stage build that produces the embedded firewall stack binaries.
6. **`make release-embeds`** — builds both linux arch embed sets (amd64 + arm64). Three of the four binaries (`clawker-cp`, `ebpf-manager`, `coredns-clawker`) go through the pinned `Dockerfile.controlplane` chain; `clawkerd` is a plain `CGO_ENABLED=0` host-Go cross-compile (pure Go, no BPF). Stages them under `embeds/{amd64,arm64}/` outside `dist/`. Asserts ELF magic + class + endianness + `e_machine` on each staged binary — silent wrong-arch is unrecoverable once published. `EI_OSABI` is NOT checked (Go-built binaries set 0 / System V regardless of `GOOS=linux`).
7. **Install cosign** (sigstore) + **syft** (SBOM generation).
8. **GoReleaser v2** — `goreleaser release --clean --parallelism 1`.
   - Two build IDs: `clawker-amd64`, `clawker-arm64`. Each has a `hooks.pre` calling `make stage-embeds-<arch>` to swap the matching arch's embeds into `assets/` paths immediately before that build's `go build` runs.
   - `--parallelism 1` is REQUIRED — the build IDs share the `assets/` paths and would race otherwise.
   - Builds 4 platform binaries (`CGO_ENABLED=0`, pure-Go cross-compile of `./cmd/clawker`).
   - Creates tar.gz archives with LICENSE + README.md.
   - Generates SHA256 checksums.
   - Signs checksums with cosign (keyless, OIDC).
   - Generates SBOMs via syft for each archive (SPDX-JSON).
   - Creates GitHub Release with changelog.
9. **Capture apt closure** — `scripts/capture-apt-packages.sh` runs `docker buildx build --target bpf-builder --load` (cache reused from step 6), then `dpkg-query -W` inside the image. Emits `dist/apt-packages.txt` (typically ~80 transitive entries).
10. **Build attestation subjects** — `scripts/release-subjects.sh` emits `dist/attestation-subjects.txt` in `sha256sum` format with a `wc -l != 16` fail-fast guard. 16 subjects: 4 archive checksums lifted from `dist/checksums.txt`, 4 unpacked CLI binaries (`tar -xzOf <archive> clawker | sha256sum`), 8 embed binaries from `embeds/<arch>/`.
11. **Generate SLSA v1 predicate** — `go run ./cmd/gen-provenance` emits `dist/provenance-predicate.json` populated with full `resolvedDependencies`: source commit, Dockerfile `FROM` images (dedup by `image+digest`), apt closure (`pkg:deb/debian/...`), Go toolchain, bpf2go pin, every BPF C source by content hash, every pinned GitHub Action SHA, content hashes of the workflow + Makefile + goreleaser config. Categorized by `name` prefix (`base-image-*`, `apt-*`, `action-*`, `bpf-source-*`, `tool-*`, `config-*`).
12. **Attest build provenance** — `actions/attest@v4` in custom-predicate mode (`predicate-type: https://slsa.dev/provenance/v1`, `predicate-path: dist/provenance-predicate.json`, `subject-checksums: dist/attestation-subjects.txt`).
13. **Attest SBOMs (×4)** — four explicit `actions/attest@v4` steps (NOT a matrix), one per archive, using the `sbom-path` input. Predicate type auto-detected as `https://spdx.dev/Document` for SPDX-JSON. Four steps share the single `build` job → all four SBOM attestations share the same signing identity as the build provenance.

## Platforms Built

| OS | Arch |
|----|------|
| linux | amd64 |
| linux | arm64 |
| darwin | amd64 |
| darwin | arm64 |

## Artifacts Produced

**Release assets (uploaded to the GitHub Release page):**

- `clawker_VERSION_OS_ARCH.tar.gz` — binary + LICENSE + README.md (4 archives)
- `checksums.txt` — SHA256 checksums of all archives
- `checksums.txt.sigstore.json` — cosign bundle (signature + certificate in Sigstore bundle format)
- `clawker_VERSION_OS_ARCH.tar.gz.sbom.json` — SBOM per archive (syft, SPDX-JSON)
- Auto-generated changelog (excludes docs/test/ci/chore commits and merge commits)

**Attestations (stored on GitHub-side, NOT release assets; fetched via `gh attestation download` or auto-resolved by `gh attestation verify`):**

- **1 SLSA v1 build-provenance attestation** with 16 subjects (4 archives + 4 unpacked CLI binaries + 8 embed binaries) and enriched `resolvedDependencies`.
- **4 SPDX SBOM attestations**, one per release archive.

**Not shipped as release assets:** `scripts/install.sh` lives at `raw.githubusercontent.com/.../main/scripts/install.sh`, is not bundled into releases, and is not signed. It is a bootstrap helper, not a build artifact (Task 6 scope correction — see initiative memory).

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
| `.github/workflows/release.yml` | Caller (trigger: `v*` tag push). Validates tag + delegates to reusable. |
| `.github/workflows/release-build.yml` | Reusable build + attest workflow. Single signing identity for L3 isolation. |
| `.goreleaser.yaml` | GoReleaser v2 config (two build IDs + per-arch pre-hooks). No `extra_files:` block — install.sh is not a release asset. |
| `cmd/gen-provenance/main.go` | SLSA v1 predicate generator (Go, stdlib + pflag). Unit-tested. Parallel to `cmd/gen-docs`. |
| `cmd/gen-provenance/main_test.go` | Coverage for Dockerfile FROM, workflow `uses:`, go.mod toolchain, apt list parsers. |
| `scripts/release-subjects.sh` | Emits `dist/attestation-subjects.txt` (16 entries; `wc -l != 16` fail-fast guard). |
| `scripts/capture-apt-packages.sh` | Runs `docker buildx --target bpf-builder` + `dpkg-query`. Emits `dist/apt-packages.txt`. |
| `internal/build/build.go` | Build-time metadata vars (`Version`, `Date`). |
| `Makefile` | `release-embeds` (pinned Docker), `stage-embeds-{amd64,arm64}` (per-build swap), `verify-release-embeds` (ELF magic+class+endian+e_machine). |
| `Dockerfile.controlplane` | Pinned multi-stage recipe for three of the four linux embeds (clawker-cp, ebpf-manager, coredns-clawker). clawkerd is host-Go cross-compile. |

## Pinned Action SHAs (live across release.yml + release-build.yml)

| Action | Tag | Commit SHA |
|---|---|---|
| `actions/attest` | v4.1.0 | `59d89421af93a897026c735860bf21b6eb4f7b26` |
| `actions/checkout` | v6.0.2 | `de0fac2e4500dabe0009e67214ff5f5447ce83dd` |
| `actions/setup-go` | v6.4.0 | `4a3601121dd01d1626a1e23e37211e3254c1c06c` |
| `docker/setup-buildx-action` | v4.0.0 | `4d04d5d9486b7bd6fa91e7baf45bbb4f8b9deedd` |
| `sigstore/cosign-installer` | v4.1.2 | `6f9f17788090df1f26f669e9d70d6ae9567deba6` |
| `anchore/sbom-action/download-syft` | v0.24.0 | `e22c389904149dbc22b58101806040fa8d37a610` |
| `goreleaser/goreleaser-action` | v7.2.1 | `1a80836c5c9d9e5755a25cb59ec6f45a3b5f41a8` |

Goreleaser CLI version (the `with: { version: ... }` input on goreleaser-action): `v2.15.4`. Only floating-by-tag bit; goreleaser binary integrity comes from its own release-time signing verified by the action.

When re-pinning: `git rev-parse <annotated-tag>` returns the tag object SHA, not the commit — use `^{}` or `git rev-list -n 1` to peel.
