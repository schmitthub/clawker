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
- **Reusable — `.github/workflows/release-build.yml`** (~85 lines). `on: workflow_call` with `HOMEBREW_TAP_GITHUB_TOKEN` required. Single `build` job, no internal `permissions:` block (inherits from caller's job). All build, sign, and attest steps live here.

The split provides same-repo **SLSA Build L3 isolation**: signing happens in the reusable workflow's isolated job context, so the Fulcio cert SAN cryptographically binds to `release-build.yml@refs/tags/<tag>` and cannot be impersonated by any other workflow file in this repo.

**Calling job permissions** (must declare full superset — reusable inherits from the calling JOB):

```yaml
permissions:
  contents: write          # goreleaser publishes the GitHub release
  id-token: write          # Sigstore OIDC token for attestation signing
  attestations: write      # GitHub attestation API write
  artifact-metadata: write # required by actions/attest@v4
```

## What Happens Automatically (reusable workflow)

1. **Checkout** with full history (`fetch-depth: 0`).
2. **Setup Go** from `go.mod` version.
3. **Install BPF toolchain** — `sudo make bpf-deps` apt-installs the pinned clang/llvm/libbpf-dev/linux-libc-dev set from the Makefile's `BPF_APT_DEPS` variable.
4. **`make release-embeds`** — produces both linux arch embed sets (amd64 + arm64) natively on `ubuntu-latest`:
   - Runs `go generate ./internal/controlplane/firewall/ebpf/...` once to produce the bpf2go bindings (`clawker_*_bpfel.{go,o}`).
   - Cross-compiles the four embed binaries for each arch via plain `CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build`: `clawker-cp`, `clawkerd`, `ebpf-manager`, `coredns-clawker`.
   - Stages them under `embeds/{amd64,arm64}/` outside `dist/`.
   - `verify-release-embeds` asserts ELF magic + class + endianness + `e_machine` on each staged binary (silent wrong-arch is unrecoverable once published).
   - No Docker, no buildx. `Dockerfile.controlplane` is NOT in the CI build path — it exists for macOS developers who can't run clang natively.
5. **Install cosign** (sigstore) + **syft** (SBOM generation).
6. **GoReleaser v2** — `goreleaser release --clean --parallelism 1`.
   - Two build IDs: `clawker-amd64`, `clawker-arm64`. Each has a `hooks.pre` calling `make stage-embeds-<arch>` to swap the matching arch's embeds into `assets/` paths immediately before that build's `go build` runs.
   - `--parallelism 1` is REQUIRED — the build IDs share the `assets/` paths and would race otherwise.
   - Builds 4 platform binaries (`CGO_ENABLED=0`, pure-Go cross-compile of `./cmd/clawker`).
   - Creates tar.gz archives with LICENSE + README.md.
   - Generates SHA256 checksums.
   - Signs checksums with cosign (keyless, OIDC).
   - Generates SBOMs via syft for each archive (SPDX-JSON) — published as release assets, NOT attested separately.
   - Creates GitHub Release with changelog.
7. **Attest release artifacts** — single `actions/attest@v4` step. `subject-path` glob covers `dist/clawker_*.tar.gz` + `embeds/amd64/*` + `embeds/arm64/*` = 12 subjects in one attestation. Mode auto-detected as SLSA build provenance (no `sbom-path`, no `predicate-*` inputs). The predicate is auto-generated from GitHub Actions context: source commit, workflow ref, builder ID, runner environment. That source-commit binding transitively pins every reproducibility input in the source tree (Makefile `BPF_APT_DEPS`, `Dockerfile.controlplane`, `.goreleaser.yaml`, `gen.go` clang flags + bpf2go pin, BPF C sources, workflow YAMLs with pinned action SHAs).

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

- **1 SLSA v1 build-provenance attestation** with 12 subjects (4 archives + 8 embed binaries) and an auto-generated predicate populated from GHA context.

SBOMs are emitted by goreleaser/syft as release assets and signed via cosign. They are NOT attested through `actions/attest` — verification of SBOM authenticity goes through the cosign signature on `checksums.txt`, which covers every release asset by SHA256.

**Not shipped as release assets:** `scripts/install.sh` lives at `raw.githubusercontent.com/.../main/scripts/install.sh`, is not bundled into releases, and is not signed. It is a bootstrap helper, not a build artifact.

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

For the SLSA build-provenance attestation, prefer the GitHub-side verifier — it auto-fetches the bundle:

```bash
gh attestation verify <artifact> \
  --owner schmitthub \
  --signer-workflow schmitthub/clawker/.github/workflows/release-build.yml
```

`gh attestation verify` returns no stdout on success (only an exit code). Use `--format json` + `jq` if you want to surface the subject digest, builder ID, or signing identity.

## Local Build

```bash
make clawker              # Build for current platform
sudo make bpf-deps        # (linux only, once) apt-install pinned BPF toolchain
make ebpf                 # produce bpf2go bindings — native on linux, via Dockerfile.controlplane on macOS
make release-embeds       # both linux arch embed sets, plain Go cross-compile
```

## Key Files

| File | Purpose |
|------|---------|
| `.github/workflows/release.yml` | Caller (trigger: `v*` tag push). Validates tag + delegates to reusable. |
| `.github/workflows/release-build.yml` | Reusable build + attest workflow (~85 lines). Single signing identity for L3 isolation. |
| `.goreleaser.yaml` | GoReleaser v2 config (two build IDs + per-arch pre-hooks). No `extra_files:` block — install.sh is not a release asset. |
| `Makefile` | `BPF_APT_DEPS` (pinned apt versions, single source of truth shared with Dockerfile.controlplane), `bpf-deps` (apt install), `ebpf` (host-aware bpf2go runner — native on linux, docker buildx on macOS), `release-embeds` (per-arch embed sets), `stage-embeds-{amd64,arm64}` (per-build swap), `verify-release-embeds` (ELF magic+class+endian+e_machine). |
| `Dockerfile.controlplane` | macOS-dev convenience for running bpf2go inside a pinned `debian:bookworm-slim` image. **NOT used in CI.** Reads `BPF_APT_DEPS` via `make bpf-deps` so version pins stay aligned with the Linux CI path. |
| `internal/build/build.go` | Build-time metadata vars (`Version`, `Date`). |

## Pinned Action SHAs (live across release.yml + release-build.yml)

| Action | Tag | Commit SHA |
|---|---|---|
| `actions/attest` | v4.1.0 | `59d89421af93a897026c735860bf21b6eb4f7b26` |
| `actions/checkout` | v6.0.2 | `de0fac2e4500dabe0009e67214ff5f5447ce83dd` |
| `actions/setup-go` | v6.4.0 | `4a3601121dd01d1626a1e23e37211e3254c1c06c` |
| `sigstore/cosign-installer` | v4.1.2 | `6f9f17788090df1f26f669e9d70d6ae9567deba6` |
| `anchore/sbom-action/download-syft` | v0.24.0 | `e22c389904149dbc22b58101806040fa8d37a610` |
| `goreleaser/goreleaser-action` | v7.2.1 | `1a80836c5c9d9e5755a25cb59ec6f45a3b5f41a8` |

Goreleaser CLI version (the `with: { version: ... }` input on goreleaser-action): `v2.15.4`. Only floating-by-tag bit; goreleaser binary integrity comes from its own release-time signing verified by the action.

When re-pinning: `git rev-parse <annotated-tag>` returns the tag object SHA, not the commit — use `^{}` or `git rev-list -n 1` to peel.

## What was removed in the prior iteration of this branch

A previous cut had a 692-line `cmd/gen-provenance/` Go CLI emitting a custom SLSA v1 predicate with hand-built `resolvedDependencies` (apt closure, BPF C source hashes, action SHAs, file content hashes), plus `scripts/release-subjects.sh` (16-subject list) and `scripts/capture-apt-packages.sh` (dpkg-query closure). All deleted. `actions/attest@v4` auto-generates the SLSA predicate from GitHub Actions context, and the source-commit binding inside that auto-predicate transitively pins everything in the source tree. The custom predicate duplicated what `git checkout <source-commit>` already provides.

Also removed: docker-buildx orchestration from `make release-embeds`. The CI build path is now native — `sudo make bpf-deps` apt-installs the pinned toolchain on `ubuntu-latest`, then `make ebpf` runs `go generate` directly, then the four embed binaries cross-compile via plain `go build`. Dockerfile.controlplane is a macOS-dev convenience only.
