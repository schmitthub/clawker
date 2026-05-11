# Release Pipeline Overhaul

**Branch:** `chore/release-pipeline-overhaul`

This initiative replaces the ad-hoc release pipeline accreted since v0.7.8 (2026-04-05) with a SLSA Build L3 same-repo reusable workflow, enriched SLSA v1 provenance with 16 per-binary subjects, separate SPDX SBOM attestations, cleaned-up Makefile, and updated docs. Scope is build-artifact provenance — inputs/dependencies/outputs of the binary. Helper-script provenance (install.sh) is explicitly out of scope (see Task 6 scope corrections).

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Dry-run validation of same-repo reusable workflow signing pattern | `complete` | claude/opus-4-7 (2026-05-11) |
| Task 2: Makefile cleanup + L1 bug fixes | `complete` | claude/opus-4-7 (2026-05-11) |
| Task 3: Split release.yml into caller + reusable workflow | `complete` | claude/opus-4-7 (2026-05-11) |
| Task 4: Migrate to actions/attest@v4 with enriched SLSA v1 provenance | `complete` | claude/opus-4-7 (2026-05-11) |
| Task 5: Add SPDX SBOM-mode attestation | `complete` | claude/opus-4-7 (2026-05-11) |
| Task 6: Consumer surface fixes | `complete` (with scope corrections) | claude/opus-4-7 (2026-05-11) |
| Task 7: Final review + documentation completion gate | `pending` | — |

---

## Key Learnings

### Pinned Action SHAs (live across release.yml + release-build.yml)

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

`actions/attest-build-provenance@v4.1.0` (`a2bbfa25...`) was used pre-Task-4; replaced by direct `actions/attest@v4`. The wrapper relationship is documented in its README ("As of v4, attest-build-provenance is simply a wrapper on top of actions/attest").

Gotcha when re-pinning: `git rev-parse <annotated-tag>` returns the tag object SHA, not the commit. Use `^{}` or `git rev-list -n 1` to peel. For `gh api .../git/refs/tags/<tag>`, `.object.sha` is the commit only when `.object.type == "commit"` (lightweight tag).

### SLSA L3 Isolation — empirically confirmed (Task 1)

Tested via throwaway `test-caller.yml` + `test-reusable.yml` + `test-dryrun-1` tag (deleted post-validation). Four observations together prove the isolation property:

1. Fulcio cert SAN URI (`1.3.6.1.4.1.57264.1.9` Build Signer URI) anchors to the **REUSABLE** workflow's path+ref, NOT the caller.
2. Build Config URI (`.18`) anchors to the caller.
3. Predicate `runDetails.builder.id` matches SAN URI (reusable).
4. Predicate `externalParameters.workflow.path` is the caller.

Anything signed inside a reusable workflow's job context cryptographically binds to that reusable's path+ref; the caller cannot impersonate it.

**Observed SAN URI format:** `https://github.com/schmitthub/clawker/.github/workflows/<reusable-file>@<git-ref>`

**Production verify regex** (semver-anchored):

```
^https://github\.com/schmitthub/clawker/\.github/workflows/release-build\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9._-]+)?(\+[A-Za-z0-9._-]+)?$
```

**`gh attestation verify --signer-workflow` value:**
```
schmitthub/clawker/.github/workflows/release-build.yml
```

### Working verify commands (production)

```bash
# 1. gh attestation verify — high-level; auto-fetches bundle from GitHub.
gh attestation verify <artifact> \
  --owner schmitthub \
  --signer-workflow schmitthub/clawker/.github/workflows/release-build.yml

# Returns NO stdout on success — surface details via --format json + jq.

# 2. cosign verify-blob — must download bundle first and pass --new-bundle-format.
gh attestation download <artifact> --owner schmitthub
cosign verify-blob \
  --bundle 'sha256:<digest>.jsonl' \
  --new-bundle-format \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '<production regex above>' \
  <artifact>
```

Both fail (exit 1) when the anchor points at the caller (`release.yml`) — verified empirically.

### Permissions / secrets gotchas (Tasks 1, 3)

**Calling job's `permissions:` block must declare full superset** — reusable workflows inherit from the calling JOB (not the caller workflow). The `build` job in `release.yml` declares:

```yaml
permissions:
  contents: write          # goreleaser publishes the GitHub release
  id-token: write          # Sigstore OIDC token for attestation signing
  attestations: write      # GitHub attestation API write
  artifact-metadata: write # NEW in actions/attest@v4 — required even via wrapper
```

`artifact-metadata: write` is the new-in-v4 requirement; v4.1.0 README's Usage section lists it but the inline Examples block omits it. With `create-storage-record` defaulting to true and `push-to-registry` defaulting to false, the dry-run still required it to succeed cleanly.

**Reusable file has NO `permissions:` block.** Declaring one would be additive-only AND scope-down, never scope-up. Single audit point on the calling job is cleaner.

**`validate` job's `contents: read` is intentional** — it runs checkout + git operations only. Tightens blast radius if a future maintainer adds an unsafe step.

**`secrets:` is explicit, NOT `secrets: inherit`** — only `HOMEBREW_TAP_GITHUB_TOKEN` is needed (goreleaser tap publication). Sigstore signing uses the runner's OIDC ID token, not any repo secret. Audit-friendlier than `inherit`. Reusable declares the secret as `required: true`.

### Other Sigstore / `gh attestation` gotchas

1. `cosign verify-blob` requires `--new-bundle-format` for `actions/attest@v4` bundles (`mediaType: application/vnd.dev.sigstore.bundle.v0.3+json`); cosign v3.0.5 defaults to legacy format and rejects v0.3 silently.
2. `gh attestation verify` returns NO stdout on success — only exit code. Use `--format json` plus `jq` to surface subject digest, `builder.id`, signing identity.
3. `gh attestation` has no `list` subcommand. To browse: `gh api repos/<owner>/<repo>/attestations/sha256:<digest>` returns `attestations[]` with full bundles inline.
4. **Firewall caveat**: GitHub Actions log storage host `productionresultssa15.blob.core.windows.net` is NOT in the clawker firewall allow list, so `gh run view --log` fails from inside an agent container. Distinct from `tmaproduction.blob.core.windows.net` (attestation bundle storage), which IS allow-listed. Operators iterating from inside containers may need `clawker firewall add productionresultssa15.blob.core.windows.net`.

### Default `actions/attest@v4` predicate (no inputs) emits a single-entry resolvedDependencies (just the source commit). To enrich it to the 30+ entry inventory this initiative requires, custom-predicate mode is mandatory: `predicate-type: https://slsa.dev/provenance/v1` + `predicate-path: <generated JSON>`.

### Makefile cleanup (Task 2)

**Deleted targets** (zero non-Makefile callers, confirmed via `grep -rn .github/ scripts/`): `clawker-build-{all,linux,darwin}`, `clawker-test`, `clawker-test-internals`. The latter pointed at `./test/internals/...` which doesn't exist in the repo.

**`CLAWKER_DATE` removed** — was wall-clock leak (`$(shell date +%Y-%m-%d)`, host-dependent, no reproducibility property). Goreleaser stamps `build.Date={{.CommitDate}}` for releases. Dev builds leave `build.Date` empty, which `internal/build/build.go` handles cleanly.

**`verify-release-embeds` ELF check tightened** — was reading 1 byte at offset 18 (could pass a Mach-O binary). Now reads 20-byte ELF header + validates four fields:
- bytes 0-3: magic `7f 45 4c 46`
- byte 4: `EI_CLASS = 0x02` (ELFCLASS64)
- byte 5: `EI_DATA = 0x01` (ELFDATA2LSB)
- bytes 18-19: `e_machine` LE word (`003e` x86_64, `00b7` AArch64)

`EI_OSABI` (byte 7) is NOT checked — Go-built binaries set 0 (System V) regardless of `GOOS=linux`. A Linux-OS-ABI assertion would false-positive every release. Comment in Makefile records this gotcha.

**`stage-embeds-{amd64,arm64}` atomicity fix** — prepended `rm -f $(EBPF_BINARY) $(COREDNS_BINARY) $(CP_BINARY) $(CLAWKERD_BINARY)`. Without this, partial cp failure could leave `assets/` half-staged with mixed-arch binaries.

**Reproducibility framing stripped** — Makefile makes no claims to byte-identical reproducibility (Docker buildx builds aren't byte-identical without `SOURCE_DATE_EPOCH` + `--reproducible`, neither set; clawkerd cross-compile depends on host Go toolchain).

### Workflow split (Task 3)

- `.github/workflows/release.yml` (caller, ~51 lines): `on: push tags v*` → 2 jobs: `validate` (semver + on-main checks, `contents: read` only) → `build` (`needs: validate`, declares full permission superset, `uses: ./.github/workflows/release-build.yml`, explicit secrets).
- `.github/workflows/release-build.yml` (reusable, ~80 lines): `on: workflow_call` with required `HOMEBREW_TAP_GITHUB_TOKEN` secret. Single `build` job. No internal `permissions:` block — inherits from caller's `build` job.

Load-bearing rationale comments (buildx requirement, `--parallelism 1` race story, embeds-don't-link-internal/build) live in the reusable where the goreleaser/make-release-embeds steps now sit.

### Enriched SLSA v1 provenance (Task 4)

**Predicate generator: `cmd/gen-provenance/main.go`** (Go, pure stdlib + pflag). Picked over bash for project convention (parallel to `cmd/gen-docs`), static typing for the SLSA v1 schema, and unit-testable parsers (`main_test.go` covers Dockerfile FROM lines, workflow `uses:` lines, go.mod toolchain directive, apt list malformation). Workflow YAMLs parsed by regex, not `yaml.Unmarshal` — keeps binary stdlib-only.

**`resolvedDependencies` enumeration order** (stable for diff-based audit):

1. Source commit — `git+<repo-uri>@<ref>`, `gitCommit` digest
2. Distinct Dockerfile `FROM ...@sha256:<digest>` images (also picks up `COPY --from=<image>@sha256:...`). Dedup by `image+digest`.
3. apt package closure — one `pkg:deb/debian/<pkg>@<version>?arch=<arch>` per line of `dist/apt-packages.txt`
4. Go toolchain — `toolchain go<ver>` directive, fallback to `go <ver>` line in go.mod
5. bpf2go pin — parsed from `//go:generate go run github.com/cilium/ebpf/cmd/bpf2go@<version>` in `internal/controlplane/firewall/ebpf/gen.go`
6. BPF C sources — every file under `internal/controlplane/firewall/ebpf/bpf/` hashed individually, plus gen.go itself
7. Pinned GitHub Actions — `uses: <owner>/<repo>@<40-hex-sha>` from both workflow files, deduped, sorted
8. File content hashes (sha256) — release.yml, release-build.yml, Dockerfile.controlplane, Makefile, .goreleaser.yaml. Redundant with source commit but cheaper for offline verifier.

**`externalParameters.buildConfig`** captures build-output knobs: goreleaser args (`release --clean --parallelism 1`), goreleaser version, Go env (`CGO_ENABLED=0`, `GOFLAGS=-trimpath`), ldflags template values, clang `-cflags`, bpf2go `-target`.

**Subjects = 16** — `scripts/release-subjects.sh <version>` emits `dist/attestation-subjects.txt` (sha256sum format, `wc -l != 16` fail-fast guard):
- 4 archive lines (lifted from `dist/checksums.txt`)
- 4 unpacked `clawker` CLI binaries (`tar -xzOf <archive> clawker | sha256sum`)
- 8 embed binaries from `embeds/<arch>/` ({clawkerd, clawker-cp, ebpf-manager, coredns-clawker} × {amd64, arm64})

The unpacked-CLI digests are "phantom" subjects — they don't correspond to files on disk. `actions/attest@v4` doesn't try to resolve subject names to filesystem paths; it just embeds them in the signed envelope. Investigator validity proof: download archive, `tar -xzf`, `sha256sum bin/clawker`, compare.

**apt closure capture: `scripts/capture-apt-packages.sh <out-file>`** runs `docker buildx build --target bpf-builder --load -t clawker-bpf-builder:provenance .` (cache reused from `make release-embeds`), then `docker run --rm clawker-bpf-builder:provenance dpkg-query -W -f '${Package}=${Version}|${Architecture}\n'`. Smoke test produced 25 dependencies with a 5-package mock; real release-time apt closure expands to ~80 entries (transitive deps).

**Workflow ordering in release-build.yml (post-Task-4):**

```
1. Record build start          → exports BUILD_STARTED_ON via $GITHUB_ENV
2. Compute release version     → exports VERSION via $GITHUB_ENV (Task 5)
3. Checkout
4. Set up Go
5. Set up Docker Buildx
6. Build linux embed sets      → make release-embeds
7. Install cosign
8. Install syft
9. Run GoReleaser              → release --clean --parallelism 1
10. Capture apt package closure→ scripts/capture-apt-packages.sh
11. Build attestation subjects → scripts/release-subjects.sh
12. Generate SLSA v1 predicate → go run ./cmd/gen-provenance
13. Attest build provenance    → actions/attest@v4 custom-predicate mode
14-17. Attest SBOMs (×4)       → actions/attest@v4 sbom-path mode (Task 5)
```

apt capture runs AFTER goreleaser even though it could run earlier — placed adjacent to predicate-generation step to keep "build provenance" block visually grouped.

**Non-obvious decisions:**

- Dockerfile image dedup by `image+digest`: same `golang:1.25.10-alpine@sha256:...` appears 3 times across stages → one entry. `name` field reflects first-encountered stage (slightly misleading; verifiers care about uri+digest, not name).
- apt package `name` field is `apt-<pkg>` not `<pkg>` — prefix lets verifiers filter by category at a glance (`apt-*`, `action-*`, `base-image-*`, `bpf-source-*`).
- `BUILD_STARTED_ON` via `$GITHUB_ENV` rather than `with:` input — Actions doesn't expose a "job started" timestamp; capturing it ourselves as the first step is the only way.
- goreleaser version parser anchors on `v[0-9]`: `^\s*version:\s*"?(v[0-9][^"\s]*)"?` matches the goreleaser-action `with:` block while excluding goreleaser's own top-level `version: 2` (integer value, no `v` prefix).

### SBOM attestations (Task 5)

**`actions/attest@v4.1.0` has a first-class `sbom-path` input** — verified empirically by fetching `action.yml` at the pinned SHA via `gh api repos/actions/attest/contents/action.yml?ref=<sha>`. DeepWiki confidently claimed otherwise; falsified by the action's own schema. Always verify against the actual commit, especially for security-critical inputs.

`sbom-path` is mutually exclusive with `predicate-type` / `predicate` / `predicate-path`. Auto-detects SPDX vs CycloneDX from file content; emits canonical predicate type (`https://spdx.dev/Document` for SPDX-JSON).

**goreleaser SBOM file naming** (verified against goreleaser source): `sboms: - artifacts: archive` with no explicit `cmd`/`documents`/`args` produces `dist/<archive>.sbom.json` in SPDX-JSON format via syft. For `clawker_${VERSION}_${OS}_${ARCH}.tar.gz`, SBOM lands at `dist/clawker_${VERSION}_${OS}_${ARCH}.tar.gz.sbom.json`.

**4 explicit per-archive steps, NOT a matrix.** Matrix strategies are job-level, not step-level, in GitHub Actions. A separate matrixed job would defeat L3 isolation (each shard would get its own Sigstore signing identity = N×4 attestations under N parallel signer identities). 4 explicit steps in the single `build` job → all SBOM attestations share the build-provenance signing identity (reusable workflow path + ref). One identity, one regex covers all 5 attestation types.

`VERSION` env var hoisted to a `Compute release version` step (writes `${GITHUB_REF_NAME#v}` to `$GITHUB_ENV`) so both the subject-list shell step and the four SBOM `with:` blocks can reference `${{ env.VERSION }}` (Actions `with:` blocks don't run a shell — can't expand `${GITHUB_REF_NAME#v}` inline).

Default `actions/attest@v4` SBOM-mode permissions match the existing build-provenance requirements — no additional grants needed on the calling job.

### Task 6 — scope corrections (CRITICAL: Task 7 must NOT revisit)

The implementation-plan agent that wrote Task 6 hallucinated most of its scope. Two subtasks were implemented, then **fully reverted** on review.

**Reverted #1: install.sh distribution.** Spec proposed shipping install.sh as a goreleaser release asset under `releases/latest/download/install.sh`. Rejected because:

1. `latest` is not a pin — it's a moving GitHub redirect. Swapping `raw.../main/scripts/install.sh` for `releases/latest/download/install.sh` shifts the trust surface (script updated only via tag push, not arbitrary main push) but does NOT pin anything.
2. install.sh is a bootstrap helper, not a build artifact. It downloads the actual artifact (signed binary). No per-release lifecycle, no per-release content. Shipping it as a release asset creates N copies of the same file across N releases. No major project does this (rustup/uv/bun/deno host on project domains; gh CLI ships nothing; helm uses raw git).

URL stays at `raw.githubusercontent.com/schmitthub/clawker/main/scripts/install.sh` across README, docs, runtime hint, and self-reference. `.goreleaser.yaml` `extra_files` block removed. Branch protection on main + required PR reviews are the existing defense for the helper-script surface; this initiative's actual scope is build-artifact provenance (Tasks 1-5).

**Real follow-up if helper-script trust ever becomes load-bearing**: make install.sh verify the cosign bundle of the downloaded `checksums.txt` against the pinned `release-build.yml` identity regex before extracting the binary. Bounds blast radius to "refuse to install" or "install genuine binary" — trust shifts from "whoever served the script" to the Sigstore + GitHub OIDC chain. Not in this PR.

**Reverted #2: `--version` flag + version-subcommand visibility.** Spec called for unhiding the `version` subcommand and wiring `--version` to print the same format. Rejected because:

- Cobra already auto-wires `--version` whenever `cmd.Version` is set (was true before this initiative). It just used Cobra's default template (`<binary> version <version>`) instead of clawker's `Format(version, buildDate)`. Difference is purely cosmetic.
- `Hidden: true` on the version subcommand may have been deliberate (canonical surface = `--version`; subcommand = quiet alternative).
- No user-visible bug. Cosmetic change framed as a fix.

`internal/cmd/version/version.go` restored to `Hidden: true`. `internal/cmd/root/root.go` `SetVersionTemplate` block removed. CLAUDE.md, docs.json, generated CLI ref all reverted to baseline.

**Surviving Task 6 surface (legitimate, real bug fixes):**

| File | Change kept |
|---|---|
| `CONTRIBUTING.md` | `go build` → `make clawker` + explanation: bare go build / go install are unsupported because embedded Linux binaries are gitignored and produced by `make release-embeds`. Real bug: bare `go build` produces a CLI with empty embeds → runtime crash. |
| `docs/installation.mdx` | Same fix as CONTRIBUTING — "Build from Source" note added on the parallel doc surface. |
| `.serena/memories/release-guide.md` | cosign verify regex tightened from substring `'github\.com/schmitthub/clawker'` (would accept any workflow's signing identity in this repo) to anchored `release-build.yml@refs/tags/v…` form. Added `--new-bundle-format`. Added parallel `gh attestation verify --signer-workflow` example. |

Branch commits for Task 6: `154687de` (original mixed bag) → `58f1c2b4` (revert install.sh distribution) → `358cbd84` (revert version flag + visibility). Net surface change is the three rows above.

---

## Context for All Agents

### Background

Clawker is approaching its v1 release. Since v0.7.8 (2026-04-05) the project has incrementally added a clawkerd supervisor, a control plane daemon (`clawker-cp`), an Envoy+CoreDNS firewall stack with eBPF egress enforcement, and four `go:embed`-bundled Linux binaries inside the host CLI. The release pipeline was accreted as components landed and now needs a deliberate overhaul before v1.

End state of this initiative:

1. **Same-repo SLSA Build L3 reusable workflow.** Caller `release.yml` handles tag validation only; new `release-build.yml` (`on: workflow_call`) owns build + attest steps. Reusable workflow's isolated job context provides L3 build isolation per SLSA v1.0 spec (no separate repo required).
2. **Enriched SLSA v1 build provenance** (`predicateType: https://slsa.dev/provenance/v1`) populated with full `resolvedDependencies`: source commit, Dockerfile.controlplane base image digests, apt packages, Go toolchain, BPF source hashes, every action SHA, tool versions, build-environment knobs. Replaces thin auto-generated provenance from `actions/attest-build-provenance`.

   **Reproducible-build discovery requirement**: an investigator reading the attestation alone must enumerate every input that affected the output. If something affects the produced bytes and isn't in `resolvedDependencies` or `buildDefinition.externalParameters`, that's a bug in the predicate generator. Completeness of enumeration, NOT byte-identical reproducibility (explicitly not a goal).
3. **16 subjects per build-provenance attestation**: 4 archives + 4 unpacked CLI binaries + 8 embedded Linux binaries. SBOM files are NOT subjects of build provenance (covered by separate SBOM-mode attestation).
4. **Separate SPDX SBOM attestations** via `actions/attest@v4` `sbom-path` mode (one per archive, predicate type auto-detected as `https://spdx.dev/Document`). Goreleaser's existing `sboms:` block stays.
5. **Cleaned-up Makefile** scoped to build/test/QA. Removed dev shortcuts, wall-clock injection, alias test targets, reproducibility-flavored comments.
6. **Updated docs** — threat-model.mdx covering forensic capability + verify commands, release-guide.md rewritten without reproducibility theater, CONTRIBUTING.md build instructions fixed. (NOT install.sh URL changes — see Task 6 scope corrections.)

Forensic motivation: with per-binary subjects, an investigator presented with a running `clawker-cp` inside a CP container can extract it, hash it, and run `gh attestation verify` against the binary directly — answering "which binary diverged" cryptographically rather than by elimination.

### Key Files

- `Makefile` — build/test/QA orchestration
- `.github/workflows/release.yml` — caller (tag validation, calls reusable)
- `.github/workflows/release-build.yml` — reusable (build + attest)
- `.goreleaser.yaml` — keeps `sboms:` block; no `extra_files` (helper scripts not shipped as release assets)
- `Dockerfile.controlplane` — multi-stage build for 3 of 4 embedded Linux binaries (clawkerd is pure-Go cross-compile)
- `cmd/gen-provenance/main.go` — SLSA v1 predicate generator (Go, stdlib + pflag, unit-tested)
- `scripts/capture-apt-packages.sh` — emits `dist/apt-packages.txt`
- `scripts/release-subjects.sh` — emits `dist/attestation-subjects.txt` (16 entries, fail-fast guard)
- `docs/threat-model.mdx` — extend in Task 7 with attestation surface + verify commands
- `.serena/memories/release-guide.md` — broader rewrite in Task 7 (cosign regex section already tightened in Task 6)
- `internal/build/build.go` — read-only reference; `Version`+`Date` injection target

### Pinning Requirements

Every external dependency MUST be pinned by digest (preferred) or exact version. Non-negotiable per `CLAUDE.md` Security: Version Pinning policy.

- GitHub Actions: pin to commit SHA, NOT semver tag. Add version comment after pin.
- Docker images in `Dockerfile.controlplane`: SHA256 manifest list digest.
- Goreleaser CLI version: pinned in `release-build.yml` `version:` input.
- Go toolchain: pinned in `go.mod` `toolchain` directive.

Always research current stable release SHAs before committing to a pin. Training data is out of date.

### Design Patterns

- **Atomic per-task commits** — each task is a separate commit on `chore/release-pipeline-overhaul`.
- **No reproducibility theater** — Makefile is build/test/QA. Reproducibility properties come from CI's pinned environment, not Make.
- **Forensic decomposition** — attestation subjects expose per-binary verifiability so an investigator can answer "which binary diverged" without reinstalling.
- **Same-repo reusable workflow** — caller and reusable both live in `schmitthub/clawker/.github/workflows/`. Signing identity = reusable workflow's path+ref. cosign verify regex anchors there.

### Rules

- Read `CLAUDE.md`, relevant `.claude/rules/` files, and any package `CLAUDE.md` before starting any task
- All new code must compile and existing tests must pass
- Conventional Commits (`feat(release):`, `chore(release):`, `fix(release):`, `docs(release):`, `revert(release):`)
- Pin every external dependency by SHA or exact version with comment indicating version
- Research current state of tools/actions before writing pipeline code
- **Scrutinize spec subtasks against actual user pain or actual security gaps before implementing.** If a subtask reads as cargo-cult ("for convention compliance", "for completeness") with no concrete bug attached, push back before touching files. The Task 6 install.sh and version-flag scope errors were both on this pattern.

---

## Task 7: Final review + documentation completion gate

**Creates/modifies:** `docs/threat-model.mdx`, `.serena/memories/release-guide.md`, possibly `README.md`, `CLAUDE.md` updates if needed
**Depends on:** All prior tasks

### Goal

Land the documentation around the new attestation surface, run a comprehensive review pass over the entire branch's changes, fix all findings, and confirm the end-to-end release pipeline works against a real release tag (or staging tag).

### Research/Planning Phase

1. Inventory all changed/added files across Tasks 1-6 (`git diff main...HEAD --stat`)
2. List documentation surfaces that may need updates:
   - `docs/threat-model.mdx` (extend with attestation surface + forensic verify commands)
   - `.serena/memories/release-guide.md` (broader rewrite without reproducibility framing; reflect new caller/reusable split — Task 6 already tightened the cosign regex section, build on that)
   - `README.md` (already touched in Task 6 reverts; check verify section if any is up to date)
   - `docs/installation.mdx` (already touched; check verify section)
   - Anywhere referencing the old monolithic release.yml signing identity
3. Plan threat-model.mdx structure:
   - Attestation surface: 1 SLSA v1 build provenance (16 subjects, enriched resolvedDependencies) + 4 SPDX SBOM attestations
   - What each layer proves
   - Forensic decomposition: "which binary diverged" use case with concrete verify commands
   - Trust roots: GitHub Sigstore OIDC, Fulcio cert chain, pinned base image digests in `Dockerfile.controlplane`
   - cosign verify-blob + gh attestation verify exact commands

### Implementation Phase

1. **Write/extend `docs/threat-model.mdx`**:
   - Section: Release Provenance & Attestations
   - Subsections: Build pipeline architecture (caller + reusable workflow, SLSA L3 isolation), Attestation types (SLSA v1 provenance enriched, SPDX SBOM), Forensic decomposition use case, Verify commands per subject type (archive, raw CLI, embedded binary, SBOM), Trust roots
2. **Rewrite `.serena/memories/release-guide.md`**:
   - New workflow split + reusable workflow rationale
   - 16-subject build provenance
   - SPDX SBOM attestations
   - Updated `gh attestation verify --signer-workflow` value (cosign regex section already done in Task 6 — extend, don't redo)
   - Strip ALL remaining reproducibility-gap framing
3. **Run review subagents over the full branch diff**:
   - `code-reviewer` — pass over all Tasks 1-6 changes for project guidelines compliance + general correctness
   - `silent-failure-hunter` — pass over workflow YAML + Makefile changes for swallowed errors
   - `comment-analyzer` — verify all new/modified comments are accurate against current code
4. **Fix all findings** raised by the agents. No "out of scope" deferrals at this gate — everything gets addressed or has an explicit follow-up issue filed.
5. **End-to-end validation** — push a test tag (e.g., `v0.7.9-rc.1`) to dry-run the full pipeline. Confirm:
   - Release artifacts produced (4 archives, 4 SBOMs, checksums.txt, checksums.txt.sigstore.json) — note: NO install.sh among release assets per Task 6 reverts
   - SLSA provenance attestation has 16 subjects with enriched resolvedDependencies
   - 4 SBOM attestations with SPDX predicate type
   - `gh attestation verify` succeeds for archive + embedded binary + SBOM
   - `cosign verify-blob` succeeds with new identity regex
6. If RC succeeds: delete the test tag + release. If RC fails: fix and re-run before marking complete.

### Acceptance Criteria

```bash
# Threat model doc covers new surface:
grep -q 'release-build.yml' docs/threat-model.mdx
grep -q 'gh attestation verify' docs/threat-model.mdx
grep -q '16 subjects\|forensic' docs/threat-model.mdx

# release-guide.md rewritten:
! grep -i 'reproducibility gap' .serena/memories/release-guide.md
grep -q 'release-build.yml' .serena/memories/release-guide.md

# Review agents ran (capture their reports in Key Learnings):
# - code-reviewer findings: addressed
# - silent-failure-hunter findings: addressed
# - comment-analyzer findings: addressed

# E2E pipeline RC test:
# - Test tag pushed and pipeline completed
# - All attestation verifies succeeded against the RC artifacts
# - Test tag + release deleted post-validation

# Final commit + branch ready for PR:
git log main..HEAD --oneline   # expect ~7 commits (5 task commits + 2 Task-6 reverts)
```

### Wrap Up

1. Update Progress Tracker: Task 7 → `complete`
2. Append Key Learnings: review agent findings summary, RC tag outcome, any production tag prep notes
3. Commit: `docs(release): threat model + release-guide updates for L3 attestation overhaul`
4. **DONE.** Branch is ready for PR. Inform user.

**PR message draft:**

```
Release pipeline overhaul — SLSA Build L3 via same-repo reusable workflow, enriched SLSA v1 provenance with 16 subjects + full resolvedDependencies, SPDX SBOM-mode attestation per archive, Makefile cleanup. Two Task-6 subtasks (install.sh distribution, --version flag visibility) were reverted as scope errors — see initiative memory key learnings. See .serena/memories/release-pipeline-overhaul for full rationale.
```
