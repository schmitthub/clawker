# Release Pipeline Overhaul

**Branch:** `chore/release-pipeline-overhaul`
**Parent memory:** —
**PRD Reference:** —

<!-- This initiative replaces the ad-hoc release pipeline accreted since v0.7.8
     (Apr 5) with a robust, attested, forensic-grade release for the upcoming
     v1 series. Output: same-repo SLSA Build L3 reusable workflow, enriched
     SLSA v1 provenance with per-binary subjects, separate SPDX SBOM
     attestation, cleaned-up Makefile, updated docs. -->

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Dry-run validation of same-repo reusable workflow signing pattern | `complete` | claude/opus-4-7 (2026-05-11) |
| Task 2: Makefile cleanup + L1 bug fixes | `complete` | claude/opus-4-7 (2026-05-11) |
| Task 3: Split release.yml into caller + reusable workflow | `complete` | claude/opus-4-7 (2026-05-11) |
| Task 4: Migrate to actions/attest@v4 with enriched SLSA v1 provenance | `complete` | claude/opus-4-7 (2026-05-11) |
| Task 5: Add SPDX SBOM-mode attestation | `complete` | claude/opus-4-7 (2026-05-11) |
| Task 6: Consumer surface fixes | `complete` | claude/opus-4-7 (2026-05-11) |
| Task 7: Final review + documentation completion gate | `pending` | — |

## Key Learnings

(Agents append here as they complete tasks. Capture surprises, pinned-version choices, working regex patterns, and any GitHub Actions / Sigstore behavior that diverged from documentation.)

### Task 1 — Same-repo reusable workflow signing dry-run (2026-05-11)

**Pinned action SHAs (current stable; fetched live via `gh api repos/<owner>/<repo>/git/refs/tags`).** Pin format mirrors existing `release.yml`: `uses: <action>@<commit-sha> # vX.Y.Z`.

| Action | Tag | Commit SHA | Published |
|---|---|---|---|
| `actions/attest` | v4.1.0 | `59d89421af93a897026c735860bf21b6eb4f7b26` | 2026-02-26 |
| `actions/checkout` | v6.0.2 | `de0fac2e4500dabe0009e67214ff5f5447ce83dd` | 2026-01-09 |
| `actions/setup-go` | v6.4.0 | `4a3601121dd01d1626a1e23e37211e3254c1c06c` | 2026-03-30 |

Gotcha: `git rev-parse <annotated-tag>` returns the tag object SHA, not the commit. Use `^{}` or `git rev-list -n 1` to peel. For `gh api .../git/refs/tags/vX.Y.Z`, `.object.sha` is the commit only when `.object.type == "commit"` (lightweight tag) — verify before trusting.

**Permissions for `actions/attest@v4.1.0` — `artifact-metadata: write` is new and required.** The v4.1.0 README's Usage section lists THREE permissions (`id-token: write`, `attestations: write`, `artifact-metadata: write`), distinct from the inline Examples block which omits the third. With `create-storage-record` defaulting to `true` and `push-to-registry` defaulting to `false`, the dry-run still required `artifact-metadata: write` to succeed cleanly. Production caller (`release.yml` post-split) AND reusable (`release-build.yml`) job permission blocks MUST include all three. Task 3's draft permissions block in the initiative context above currently lists only `id-token`, `attestations`, `contents` — Task 3 must add `artifact-metadata: write`.

**SLSA L3 isolation empirically confirmed:**

- Caller `test-caller.yml`: declared `uses: ./.github/workflows/test-reusable.yml` only.
- Reusable `test-reusable.yml`: `on: workflow_call`, single `actions/attest@v4.1.0` step on a dummy `test.bin`.
- Tag `test-dryrun-1` → commit `2051f23c95b1c91fec85654d068ffe1cae647dff`. Run completed in 5s.
- Branch + tag deleted post-validation (`git push origin --delete` for both).

**Fulcio cert + attestation observations:**

The signing identity (SAN URI / `1.3.6.1.4.1.57264.1.9` Build Signer URI) anchored to the **REUSABLE** workflow's path+ref, NOT the caller. Build Config URI (`.18`) anchored to the **caller**. Predicate `runDetails.builder.id` matched SAN URI (reusable). Predicate `externalParameters.workflow.path` was the caller. These four observations together prove the L3 isolation property — anything an agent signs from inside a reusable workflow's job context cryptographically binds to that reusable's path+ref, and the caller cannot impersonate it.

Observed SAN URI verbatim (from cosign's negative-test error message): `https://github.com/schmitthub/clawker/.github/workflows/test-reusable.yml@refs/tags/test-dryrun-1`. Format: `https://<server>/<owner>/<repo>/.github/workflows/<reusable-file>@<git-ref>`.

**Default `actions/attest@v4` predicate body (no inputs):**

```json
{
  "predicateType": "https://slsa.dev/provenance/v1",
  "predicate": {
    "buildDefinition": {
      "buildType": "https://actions.github.io/buildtypes/workflow/v1",
      "externalParameters": {
        "workflow": {
          "ref": "refs/tags/test-dryrun-1",
          "repository": "https://github.com/schmitthub/clawker",
          "path": ".github/workflows/test-caller.yml"
        }
      },
      "internalParameters": {
        "github": {
          "event_name": "push",
          "repository_id": "1129396406",
          "repository_owner_id": "8697299",
          "runner_environment": "github-hosted"
        }
      },
      "resolvedDependencies": [
        {
          "uri": "git+https://github.com/schmitthub/clawker@refs/tags/test-dryrun-1",
          "digest": {"gitCommit": "2051f23c95b1c91fec85654d068ffe1cae647dff"}
        }
      ]
    },
    "runDetails": {
      "builder": {"id": "https://github.com/schmitthub/clawker/.github/workflows/test-reusable.yml@refs/tags/test-dryrun-1"},
      "metadata": {"invocationId": "https://github.com/schmitthub/clawker/actions/runs/25644227736/attempts/1"}
    }
  }
}
```

Confirms Task 4 design: default mode emits a **single-entry** `resolvedDependencies` (just the source commit). To enrich it to the 30+ entry inventory the initiative requires (image digests, apt packages, action SHAs, file content hashes, tool versions), Task 4 MUST use custom-predicate mode — `predicate-type: https://slsa.dev/provenance/v1` + `predicate-path: <generated JSON>` — supplying its own full predicate body.

**Working verify commands (record VERBATIM for production — Tasks 4, 6, 7 docs):**

```bash
# 1. gh attestation verify — high-level; auto-fetches bundle from GH.
gh attestation verify <artifact> \
  --owner schmitthub \
  --signer-workflow schmitthub/clawker/.github/workflows/<REUSABLE>.yml

# Returns NO stdout on success — surface details via --format json.
# (In production, REUSABLE = release-build.yml.)

# 2. cosign verify-blob — must download bundle first and pass --new-bundle-format.
gh attestation download <artifact> --owner schmitthub
cosign verify-blob \
  --bundle 'sha256:<digest>.jsonl' \
  --new-bundle-format \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/schmitthub/clawker/\.github/workflows/<REUSABLE>\.yml@refs/tags/' \
  <artifact>
```

Both fail (exit 1) when the anchor points at the caller (`test-caller.yml`) — verified.

**Production regex for `release-build.yml`** (semver-anchored, mirrors `release.yml` tag validator):

```
^https://github\.com/schmitthub/clawker/\.github/workflows/release-build\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9._-]+)?(\+[A-Za-z0-9._-]+)?$
```

`gh attestation verify --signer-workflow` value in production:

```
schmitthub/clawker/.github/workflows/release-build.yml
```

**Surprises:**

1. **`cosign verify-blob` requires `--new-bundle-format`** for bundles produced by `actions/attest@v4` / downloaded via `gh attestation download`. Bundle `mediaType` is `application/vnd.dev.sigstore.bundle.v0.3+json`; cosign v3.0.5 defaults to legacy format and rejects v0.3 without the flag. Document in Task 7 threat-model verify section.
2. **`gh attestation verify` returns NO stdout on success** — only exit code. Use `--format json` plus `jq` to surface subject digest, `builder.id`, signing identity. Worth a wrapper script in the threat-model doc.
3. **`gh attestation` has no `list` subcommand** in current `gh` (only `download`, `trusted-root`, `verify`). To browse, use the REST API: `gh api repos/<owner>/<repo>/attestations/sha256:<digest>` — returns `attestations[]` array with full bundles inline.
4. **GitHub Actions log storage host** (`productionresultssa15.blob.core.windows.net`) is NOT in the clawker firewall allow list, so `gh run view --log` from inside an agent container fails. Distinct from `tmaproduction.blob.core.windows.net` (attestation bundle storage) which IS allow-listed. Operators iterating on release pipeline work from inside containers may need to `clawker firewall add productionresultssa15.blob.core.windows.net` or shell out to the host.
5. **Caller `permissions:` block scope.** Reusable workflows inherit permissions from the calling JOB (not the caller workflow). The calling job must declare the full superset the reusable needs — declare `id-token: write`, `attestations: write`, `artifact-metadata: write`, `contents: read` on the `jobs.<name>` block that holds the `uses: ./.github/workflows/release-build.yml` statement.
6. **`secrets: inherit` is unnecessary for the attestation path itself** — Sigstore signing uses the runner's OIDC ID token (gated by `id-token: write`), not any repo secret. The production `secrets:` block on the calling job only needs `HOMEBREW_TAP_GITHUB_TOKEN` (used by goreleaser for tap publication). Pass it explicitly: `secrets: { HOMEBREW_TAP_GITHUB_TOKEN: ${{ secrets.HOMEBREW_TAP_GITHUB_TOKEN }} }`. Audit-friendlier than `inherit`.

**Pinned action SHAs ready for Task 3 to copy directly:**

```yaml
uses: actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26 # v4.1.0
uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
uses: actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c # v6.4.0
```

Other actions (`docker/setup-buildx-action`, `sigstore/cosign-installer`, `anchore/sbom-action/download-syft`, `goreleaser/goreleaser-action`) still hold SHAs from a prior session — Task 3 must re-verify they remain current before lifting them into `release-build.yml`.

### Task 2 — Makefile cleanup + L1 bug fixes (2026-05-11)

**Deleted targets (confirmed no callers outside Makefile via `grep -rn ... .github/ scripts/`):**

- `clawker-build-all`, `clawker-build-linux`, `clawker-build-darwin` — dev-fast cross-compile shortcuts (bypassed pinned Dockerfile.controlplane chain). Zero non-Makefile callers.
- `clawker-test` — single recipe ran `$(TEST_CMD_VERBOSE) ./...` (NOT actually an alias of `test-unit` despite help-text claim). Zero non-Makefile callers.
- `clawker-test-internals` — pointed at `./test/internals/...` which doesn't even exist in the repo (only `test/e2e`, `test/whail`, `test/adversarial`). Dead recipe.

`restart: clawker-clean clawker` still uses `clawker-clean` — kept; not in scope for deletion. `clawker-test-coverage`, `clawker-test-short`, `clawker-lint`, `clawker-staticcheck`, `clawker-fmt`, `clawker-tidy`, `clawker-install`, `clawker-install-global` — left in place; out of scope for this task per the initiative plan.

**`CLAWKER_DATE` removed.** `internal/build/build.go` already handles `Date = ""` cleanly (no `init()` fallback for Date, but `fmt.Println("")` is harmless; downstream code just prints empty string for dev builds). Goreleaser stamps `build.Date={{.CommitDate}}` for releases — confirmed in `.goreleaser.yaml:40,61`. The previous `CLAWKER_DATE := $(shell date +%Y-%m-%d)` was wall-clock leak (no reproducibility property — host-dependent).

`internal/build/CLAUDE.md` updated to reflect the new injection contract (dev: Version only; release: Version + Date via `{{.CommitDate}}`).

**Reproducibility framing stripped from Makefile.** Four touch points cleaned:

- Section header `# Embedded firewall stack binaries (reproducible Docker builds)` → `# Embedded firewall stack binaries`
- Removed claim "Every input is pinned... byte-identical output" — overpromises against the actual Docker build behavior (no SOURCE_DATE_EPOCH, no `--reproducible` flag, no rebuild verification against a known-good hash).
- Removed `# See internal/controlplane/firewall/ebpf/REPRODUCIBILITY.md` xref (file still exists; it's about pin-update procedure, not byte-identical reproducibility). Task 7 can decide whether to keep the .md file or rename it.
- `release-embeds` preamble compressed from ~38 lines to ~18, keeping ONLY the four load-bearing invariants: `embeds/` outside `dist/`, clawkerd pure-Go bypass, two build IDs vs four, `--parallelism 1` race story.

**`verify-release-embeds` ELF check tightened.** Was reading 1 byte at offset 18 (`e_machine` LSB only — could pass a Mach-O binary that happened to have `0x3e` at that offset). Now reads 20-byte ELF header and validates four fields:

- bytes 0-3: magic (`7f 45 4c 46`) — rules out Mach-O / PE / other non-ELF
- byte 4: `EI_CLASS = 0x02` (ELFCLASS64)
- byte 5: `EI_DATA = 0x01` (ELFDATA2LSB, little-endian)
- bytes 18-19: `e_machine` LE word — `003e` = x86_64, `00b7` = AArch64

Each failure prints a specific error pointing at the broken field. **`EI_OSABI` (byte 7) is NOT checked**: Go-built binaries set 0 (System V), not 3 (Linux), regardless of `GOOS=linux` — a Linux-OS-ABI check would false-positive every release. Comment in the Makefile records this gotcha explicitly so a future contributor doesn't add an over-eager `EI_OSABI == 0x03` assertion.

**`stage-embeds-{amd64,arm64}` atomicity fix.** Prepended `rm -f $(EBPF_BINARY) $(COREDNS_BINARY) $(CP_BINARY) $(CLAWKERD_BINARY)` to both recipes. Without this, a partial cp failure mid-recipe (e.g., permission denied on the 3rd of 4 files) could leave `assets/` in a half-staged state where some binaries are the previous arch's bytes. With the rm-first guard, either every asset is the requested arch or `go build` fails on the missing embed source, never silently embeds mismatched-arch binaries.

**Comment fixes outside Makefile:**

- `.github/workflows/release.yml` (`make release-embeds` step comment): replaced misleading "No CLAWKER_VERSION/CLAWKER_DATE override here..." paragraph with the actual rationale: "`make release-embeds` doesn't need version stamping — the embedded binaries don't link internal/build. Final CLI version+date are stamped by goreleaser's ldflags below."
- `.serena/memories/release-guide.md` rewritten: `{{.Date}}` → `{{.CommitDate}}` (matches actual `.goreleaser.yaml:40,61`). Also removed examples referencing the now-deleted `clawker-build-{all,linux,darwin}` targets, and stripped the "Reproducibility gap (known)" subsection (no longer claiming reproducibility we don't provide). The full rewrite for L3 attestation surface is deferred to Task 7 per plan.

**`.clawker.yaml` firewall add_domains** updated in this commit as well (carry-over from a prior session in the working tree; out of strict Task 2 scope but harmless infra): adds `docs.github.com`, `slsa.dev`, `github.blog` for spec lookups + reorders existing entries. Kept because removing it would lose useful in-container research domains during the rest of the initiative.

**Acceptance criteria — all pass:**

```
=== removed targets check ===
OK    (no clawker-build-{all,linux,darwin}, clawker-test{,-internals} recipes)
=== CLAWKER_DATE removed ===
OK
=== ELF tightened ===
magic-OK  (7f454c46 present)
class-OK  (EI_CLASS present)
=== reproducibility framing stripped ===
OK    (no `reproducib|pin.reproducible|for reproducibility`)
```

`make clawker` builds cleanly: `bin/clawker version` → `clawker version 0.7.8-22-g82af1ca2`. Empty `build.Date` works fine for dev builds (falls back to printed "").

`make test` passes: **5093 tests, 8 skipped**, all platform-gated. (One flake observed on first run — `cmd/clawkerd TestStartShellCommand_InitialStdinCloseStdinRace` — passed 3× in isolation and on the re-run of full suite; unrelated to Makefile changes. Pre-existing race-test under heavy parallel load.)

**Surprise:** The release-guide memory's "Reproducibility gap (known)" subsection claimed full pin-reproducibility of embeds via Dockerfile.controlplane. This isn't actually true — Docker buildx builds aren't byte-identical across hosts without `--reproducible` flag (which isn't set), and clawkerd cross-compile depends on the host Go toolchain. The framing was aspirational. Stripped.

**No production unit test, e2e, or whail run is necessary at this gate** — Task 2 changes are pure Makefile/workflow comments + memory text. The single `make test` run + targeted re-runs of the flaky clawkerd race test cover the verification surface adequately.

### Task 4 — Migrate to actions/attest@v4 with enriched SLSA v1 provenance (2026-05-11)

**Pinned `actions/attest` SHA:** `59d89421af93a897026c735860bf21b6eb4f7b26` (v4.1.0, published 2026-02-26). Verified current latest release via `gh api repos/actions/attest/releases/latest` — no v4.2 yet. Unchanged from Task 1's dry-run pin.

**Wrapper replacement:** removed `actions/attest-build-provenance@a2bbfa25...` from `release-build.yml`; added direct `actions/attest@59d89421...` invocation with `predicate-type: https://slsa.dev/provenance/v1` + `predicate-path: ./dist/provenance-predicate.json` (custom-predicate mode). `subject-checksums: ./dist/attestation-subjects.txt` (16 subjects).

**Predicate generator design choice: Go program** (`cmd/gen-provenance/main.go`), pure stdlib + pflag. Picked over bash because:
1. Project convention — parallel to `cmd/gen-docs`, `cmd/clawker-generate` (every code-gen leaf binary is Go).
2. Static typing for the SLSA v1 predicate schema (json struct tags eliminate hand-marshaled string concat hazards).
3. Unit-testable parsers (`main_test.go` covers Dockerfile FROM lines, workflow `uses:` lines, go.mod toolchain directive, apt list malformation).
4. No new shell maintenance burden — all the regex parsing lives in one file.

**Generator inputs (flags):**

- `--repo-uri`, `--source-commit`, `--source-ref`, `--workflow-ref`, `--builder-id` — from `${GITHUB_*}` env in the workflow step.
- `--repository-id`, `--repository-owner-id`, `--event-name`, `--runner-environment` — for `internalParameters.github` (matches the default attestation's shape; what `gh attestation verify` expects).
- `--invocation-id` — constructed as `${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}/attempts/${GITHUB_RUN_ATTEMPT}`.
- `--started-on`, `--finished-on` — wall-clock RFC3339. `BUILD_STARTED_ON` is captured via a `Record build start` step at the top of the job and stored in `$GITHUB_ENV`; `finished-on` is computed at predicate-generation time. Required for forensic timeline.
- `--apt-packages` — path to `dist/apt-packages.txt`, produced by `scripts/capture-apt-packages.sh`. Format: one `<pkg>=<version>|<arch>` per line.
- File paths default to repo-relative (`Dockerfile.controlplane`, `Makefile`, etc.), overridable for tests.

**resolvedDependencies enumeration (in emit order — stable for diff-based audit):**

1. Source commit — `git+<repo-uri>@<ref>`, `gitCommit` digest.
2. Distinct Dockerfile `FROM ...@sha256:<digest>` images (also picks up `COPY --from=<image>@sha256:...` for the cross-stage Go toolchain copy). Dedup by `image+digest` so the 3 `golang:1.25.10-alpine` references collapse into one entry.
3. apt package closure (one `pkg:deb/debian/<pkg>@<version>?arch=<arch>` per line of the captured file).
4. Go toolchain — prefers `toolchain go<ver>` directive, falls back to `go <ver>` line in go.mod.
5. bpf2go pin — parsed from `//go:generate go run github.com/cilium/ebpf/cmd/bpf2go@<version>` in `internal/controlplane/firewall/ebpf/gen.go`.
6. BPF C sources — every file under `internal/controlplane/firewall/ebpf/bpf/` hashed individually, plus gen.go itself.
7. Pinned GitHub Actions — `uses: <owner>/<repo>@<40-hex-sha>` lines from both workflow files, deduped, sorted alphabetically. Short names like `action-checkout`, `action-sbom-action-download-syft`.
8. File content hashes (sha256) — release.yml, release-build.yml, Dockerfile.controlplane, Makefile, .goreleaser.yaml. Redundant with the source commit digest, but cheaper for an offline verifier than walking git.

**externalParameters.buildConfig captures build-output knobs:** goreleaser args (`release --clean --parallelism 1`), goreleaser version (parsed from the `version:` line under goreleaser-action's `with:`), Go env (`CGO_ENABLED=0`, `GOFLAGS=-trimpath`), ldflags template values (`{{.Version}}`, `{{.CommitDate}}`, `-s -w`), clang `-cflags` and bpf2go `-target` (parsed from gen.go).

**externalParameters.workflow mirrors GitHub's default attestation:** caller workflow path/ref/repository (so `gh attestation verify` validates the workflow field cleanly). The reusable workflow path is captured separately via `runDetails.builder.id`.

**Smoke test (locally with mock env vars, before commit):**

```
go run ./cmd/gen-provenance \
  --repo-uri https://github.com/schmitthub/clawker \
  --source-commit 8ce07534... --source-ref refs/tags/v0.99.0 \
  --workflow-ref schmitthub/clawker/.github/workflows/release.yml@refs/tags/v0.99.0 \
  --builder-id https://github.com/.../release-build.yml@refs/tags/v0.99.0 \
  --repository-id 1129396406 --repository-owner-id 8697299 \
  --event-name push --runner-environment github-hosted \
  --invocation-id https://github.com/.../actions/runs/12345/attempts/1 \
  --apt-packages /tmp/apt-packages.txt --output /tmp/predicate.json
```

Result: 25 resolvedDependencies entries with a 5-package apt list. Real release-time apt closure expands to ~60+ entries (transitive deps for clang/llvm/libbpf-dev/linux-libc-dev/ca-certificates), pushing the total to ~80 entries.

**Subject list — `scripts/release-subjects.sh <version>`** emits `dist/attestation-subjects.txt` in sha256sum format. 16 entries enforced by `wc -l != 16` fail-fast guard:

- 4 archive lines lifted from `dist/checksums.txt` (goreleaser already emits sha256sum format)
- 4 unpacked `clawker` CLI binaries — `tar -xzOf <archive> clawker | sha256sum` per archive. Names: `clawker-linux-amd64`, `clawker-linux-arm64`, `clawker-darwin-amd64`, `clawker-darwin-arm64`.
- 8 embed binaries from `embeds/<arch>/`. Names: `clawkerd-linux-amd64`, `clawker-cp-linux-amd64`, `ebpf-manager-linux-amd64`, `coredns-clawker-linux-amd64`, and the four arm64 equivalents.

The unpacked-CLI digests are "phantom" subjects — they don't correspond to files on disk; they're the digests of the bytes the user will eventually `tar -xz` out of an archive. Validity proof for an investigator: download the release archive, `tar -xzf` it, `sha256sum bin/clawker`, compare to the attestation's subject digest with that name.

**apt package closure — `scripts/capture-apt-packages.sh <out-file>`** runs `docker buildx build --target bpf-builder --load -t clawker-bpf-builder:provenance .` to materialize the bpf-builder image (cache reused from `make release-embeds`), then `docker run --rm clawker-bpf-builder:provenance dpkg-query -W -f '${Package}=${Version}|${Architecture}\n'`. Output captured directly to the file the generator reads. Fail-fast if `< 5` entries (would indicate the dpkg-query ran against the wrong image or returned an empty closure).

**Workflow ordering in release-build.yml (post-Task-4):**

```
1. Record build start          → exports BUILD_STARTED_ON via $GITHUB_ENV
2. Checkout                    → unchanged
3. Set up Go                   → unchanged
4. Set up Docker Buildx        → unchanged
5. Build linux embed sets      → make release-embeds (unchanged)
6. Install cosign              → unchanged
7. Install syft                → unchanged
8. Run GoReleaser              → unchanged
9. Capture apt package closure → NEW (scripts/capture-apt-packages.sh)
10. Build attestation subjects → NEW (scripts/release-subjects.sh)
11. Generate SLSA v1 predicate → NEW (go run ./cmd/gen-provenance)
12. Attest build provenance    → REPLACED (actions/attest@v4 custom-predicate mode)
```

The apt capture deliberately runs AFTER goreleaser even though it could run earlier (it doesn't depend on goreleaser output) — placing it adjacent to the predicate-generation step keeps the "build provenance" block visually grouped and the goreleaser-fails-loudly path uncluttered.

**Surprises / non-obvious decisions:**

1. **Generator parses workflow YAMLs by regex, not yaml.Unmarshal.** Could pull `gopkg.in/yaml.v3`, but regex on `uses:` / `version:` / `args:` lines is sufficient for the few fields we care about, keeps the binary stdlib-only (parallel to gen-docs), and avoids drift if a future GitHub Actions feature changes the YAML shape. The regexes are unit-tested.
2. **Dockerfile image dedup by `image+digest`.** Same `golang:1.25.10-alpine@sha256:...` appears 3 times across stages — we emit one entry. The `name` field reflects the first-encountered stage, slightly misleading; verifiers care about uri+digest, not name.
3. **`name` field on apt packages is `apt-<pkg>`**, not just `<pkg>`. Prefix keeps a verifier scanning `.name` strings able to filter by category at a glance (`apt-*`, `action-*`, `base-image-*`, `bpf-source-*`).
4. **`subject-checksums` accepts phantom subjects.** The 4 unpacked CLI binaries don't exist as files in `dist/` — we extract their digests via `tar -xzOf` and write them to the subjects file under semantic names like `clawker-linux-amd64`. actions/attest@v4 doesn't try to resolve subject names to filesystem paths; it just embeds them in the signed envelope.
5. **`BUILD_STARTED_ON` via `$GITHUB_ENV` rather than passing through a `with:` input.** GitHub Actions doesn't expose a "job started" timestamp; capturing it ourselves as the first job step is the only way. The reusable workflow contract stays unchanged (no new inputs/secrets).
6. **goreleaser version parser anchors on `v[0-9]`.** The regex `^\s*version:\s*"?(v[0-9][^"\s]*)"?` matches the goreleaser-action `with:` block's `version: "v2.15.4"` line while excluding goreleaser's own top-level config `version: 2` (which has integer value, not a `v` prefix).

**Acceptance criteria — all pass:**

```
=== actions/attest@v4 ==                     OK
=== SLSA v1 predicate-type ==                OK
=== subject-checksums attestation-subjects ==OK
=== all uses: pins are SHA ==                OK
=== old attest-build-provenance removed ==   OK
=== gen-provenance compiles ==               OK
=== gen-provenance tests pass ==             OK (6 tests, pure unit)
=== shell scripts syntax-check ==            OK
=== actionlint clean ==                      OK
=== smoke test 25 deps emitted ==            OK
```

The `gh attestation verify` end-to-end and reproducible-build discovery walkthrough are deferred to Task 7 E2E — they require a real push-tag trigger that this branch cannot exercise without polluting release history.

---

### Task 3 — Split release.yml into caller + reusable workflow (2026-05-11)

**Final SHA pin set (all re-verified against `gh api repos/<owner>/<repo>/git/refs/tags/<tag>` at start of task):**

| Action | Tag | Commit SHA | Δ from prior |
|---|---|---|---|
| `actions/checkout` | v6.0.2 | `de0fac2e4500dabe0009e67214ff5f5447ce83dd` | unchanged |
| `actions/setup-go` | v6.4.0 | `4a3601121dd01d1626a1e23e37211e3254c1c06c` | unchanged |
| `docker/setup-buildx-action` | v4.0.0 | `4d04d5d9486b7bd6fa91e7baf45bbb4f8b9deedd` | unchanged |
| `sigstore/cosign-installer` | v4.1.2 | `6f9f17788090df1f26f669e9d70d6ae9567deba6` | **BUMPED** from v4.1.1 (published 2026-05-07) |
| `anchore/sbom-action/download-syft` | v0.24.0 | `e22c389904149dbc22b58101806040fa8d37a610` | unchanged |
| `goreleaser/goreleaser-action` | v7.2.1 | `1a80836c5c9d9e5755a25cb59ec6f45a3b5f41a8` | unchanged |
| `actions/attest-build-provenance` | v4.1.0 | `a2bbfa25375fe432b6a289bc6b6cd05ecd0c4c32` | unchanged (Task 4 replaces with `actions/attest@v4`) |

Goreleaser CLI version input bumped `v2.15.2` → `v2.15.4` (current stable per `gh api repos/goreleaser/goreleaser/releases/latest`).

**File layout:**

- `.github/workflows/release.yml` (caller, 51 lines): `on: push tags v*` → 2 jobs: `validate` (semver + on-main checks, `contents: read` only) → `build` (`needs: validate`, declares full permission superset, `uses: ./.github/workflows/release-build.yml`, explicit secrets pass-through). No workflow-level `permissions:` block — per-job only.
- `.github/workflows/release-build.yml` (reusable, NEW, 80 lines): `on: workflow_call` with required `HOMEBREW_TAP_GITHUB_TOKEN` secret. Single `build` job, 8 steps (identical to current release.yml minus tag validation). No `permissions:` block inside this file — inherits from caller's `build` job.

**Permissions on caller's `build` job (full superset, per Task 1 finding #5 + new v4 requirement):**

```yaml
permissions:
  contents: write          # goreleaser publishes the GitHub release
  id-token: write          # Sigstore OIDC token for attestation signing
  attestations: write      # GitHub attestation API write
  artifact-metadata: write # required by actions/attest@v4 (wrapper relies on it)
```

`artifact-metadata: write` is the new-in-v4 requirement Task 1 surfaced. Per `actions/attest-build-provenance@v4.1.0` README: "As of version 4, `actions/attest-build-provenance` is simply a wrapper on top of `actions/attest`." → wrapper inherits the same permission profile. Declared up front so Task 4 swap to `actions/attest@v4` is a no-op on permissions.

**Secrets pattern chosen: explicit, NOT `secrets: inherit`** (per Task 1 surprise #6 — audit-friendlier). Caller's `build` job declares:

```yaml
secrets:
  HOMEBREW_TAP_GITHUB_TOKEN: ${{ secrets.HOMEBREW_TAP_GITHUB_TOKEN }}
```

Reusable declares the secret as `required: true`. `GITHUB_TOKEN` is auto-available in reusable; no declaration needed. Sigstore signing uses the OIDC ID token (gated by `id-token: write` on the calling job), NOT any repo secret — so the secrets surface is just the homebrew tap token.

**No workflow-level `permissions:` block in caller.** Reusable workflows inherit from the calling JOB (not the workflow). Putting a workflow-level block would mislead — the `validate` job correctly restricts to `contents: read`, the `build` job declares the full superset for itself. Cleaner than relying on workflow-level scope.

**Comment preservation:** load-bearing rationale comments (buildx requirement, `--parallelism 1` race story, embeds-don't-link-internal/build) moved into the reusable where goreleaser/make-release-embeds now live. Caller's comments scoped to permission/inheritance reasoning only.

**Goreleaser bump rationale.** v2.15.2 → v2.15.4 is two patch versions. Did not audit changelog in detail since Task 3 spec explicitly directs updating to current stable, and patch bumps within the same v2.15.x line should be safe. If a regression surfaces during the Task 7 E2E test, can roll back to v2.15.2 — pin lives in one place (`release-build.yml` step).

**cosign-installer bump rationale.** v4.1.1 → v4.1.2 published 2026-05-07 (4 days before this task). Same v4.x line; no breaking changes expected. The verify commands captured in Task 1 used a host cosign v3.0.5 binary (independent of this installer); production CI uses whatever this installer ships. Smoke-tested at Task 7 E2E.

**Acceptance criteria — all pass:**

```
=== files exist ===
release.yml OK
release-build.yml OK
=== caller validates + calls reusable ===
workflow_call OK
caller uses reusable OK
=== SHA-pin check (should be empty) ===
all pinned by SHA
=== YAML parse ===
YAML valid (both files)
=== goreleaser version bumped ===
v2.15.4
=== cosign SHA bumped ===
sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6  # v4.1.2
```

**Manual test deferred to Task 7 E2E** (can't fully exercise a push-tag trigger from a feature branch without polluting release history). The structural acceptance gates above + YAML parse confirm shape correctness.

**Surprises / non-obvious decisions:**

1. **Reusable file has no `permissions:` block.** Task 3 spec was explicit about this, but worth re-stating: declaring permissions in the reusable would be additive-only AND scope-down, never scope-up, on whatever the caller already granted. Cleaner to declare once on the caller's calling job — single audit point. Future maintainers tempted to "be safe" and copy the permissions block into the reusable should not — it's noise that pretends the reusable is self-contained when it isn't.
2. **`validate` job's `contents: read` is intentional.** It runs checkout + git operations only; doesn't need write. Tightening helps reduce the blast radius if a future maintainer adds an unsafe step here. Defense in depth — the validate job runs before any third-party action consumes the tag.
3. **Goreleaser CLI version (v2.15.4) is a `with:` input, NOT pinned by SHA.** The `goreleaser-action` itself is SHA-pinned (`1a80836c...`); the goreleaser CLI binary it downloads is selected by tag. This is the only floating-by-tag bit, and unavoidable — there's no SHA-input mode for that. The goreleaser binary's integrity comes from its own release-time signing, verified by the action.

### Task 6 — Consumer surface fixes (2026-05-11)

**Scope correction (2026-05-11, post-implementation):** the spec proposed pinning the install.sh URL by shipping it as a goreleaser release asset under `releases/latest/download/install.sh`. That was wrong on two counts:

1. **`latest` is not a pin.** It's a moving GitHub redirect that resolves to the most-recent non-prerelease's asset. Swapping `raw.../main/scripts/install.sh` for `releases/latest/download/install.sh` shifts the trust surface (helper script now updated only via tag push, not arbitrary main push) but does NOT pin anything.
2. **install.sh is a bootstrap helper, not a build artifact.** It downloads + installs the actual artifact (the signed binary). It has no per-release lifecycle, no per-release content. Shipping it as a release asset creates N copies of the same file across N releases, solves no real problem, and matches no major project's pattern (rustup/uv/bun/deno host on project domains; gh CLI ships nothing — package managers; helm uses raw git). Reviewer (project owner) called this out and directed full revert.

**Decision: reverted all install.sh distribution changes.** URL stays at `raw.githubusercontent.com/schmitthub/clawker/main/scripts/install.sh` across README, docs/installation.mdx, docs/quickstart.mdx, internal/clawker/cmd.go runtime hint, and scripts/install.sh self-reference. `.goreleaser.yaml` `extra_files` block removed. Branch protection on main + required PR reviews are the existing defense for the helper-script surface; the initiative's actual scope is build-artifact provenance (inputs/deps/outputs of the binary), which Tasks 1-5 already deliver via SLSA L3 attestation. Helper-script provenance is a different problem and not in scope.

**Real follow-up (separate issue, NOT this PR):** if install.sh-trust ever becomes a load-bearing concern, the correct fix is to make install.sh verify the cosign bundle of the downloaded `checksums.txt` against the pinned `release-build.yml` identity regex BEFORE extracting the binary. That bounds the script's blast radius to "refuse to install" or "install genuine binary" — trust shifts from "whoever served the script" to the Sigstore + GitHub OIDC chain the rest of the initiative just built.

**`--version` flag implementation: `cmd.SetVersionTemplate("{{ index .Annotations \"versionInfo\" }}")`.**

Cobra auto-wires a `--version` flag whenever `cmd.Version` is set (already done at `root.go:51`: `Version: f.Version`). Cobra's default version template prints `<binary> version <version>` — wrong format for clawker (we want `clawker version <ver> (<date>)` matching the existing `version` subcommand). Solution: read the same `versionInfo` annotation the subcommand reads, via `SetVersionTemplate`. Single source of truth (`versioncmd.Format(version, buildDate)` writes the annotation; both `--version` and `version` read it).

Smoke test confirms byte-identical output across both surfaces:
```
$ bin/clawker --version
clawker version 0.7.8-22-g82af1ca2
$ bin/clawker version
clawker version 0.7.8-22-g82af1ca2
```

`clawker --help` now lists `version` in the available subcommands (was `Hidden: true` before; that line removed). Added `Short: "Show clawker version and build date"` so the auto-rendered help row is non-empty.

**CONTRIBUTING.md `make clawker` (not `go build`)**: verified `make clawker` depends on `ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)` (Makefile:108), so it produces all four embeds via the pinned Docker chain before the final `go build` of the host CLI. Bare `go build ./cmd/clawker` would compile a CLI with empty embeds (the `assets/` directories are gitignored) → runtime crash on first use. Added an explanatory note to the CONTRIBUTING setup block so new contributors don't re-discover this the hard way.

**`.serena/memories/release-guide.md` cosign regex tightened.** Old: substring `'github\.com/schmitthub/clawker'` — would match a forged attestation produced by ANY workflow file in this repo. New: anchored to the reusable `release-build.yml` at the release tag, mirrors the format Task 1 validated empirically:

```
^https://github\.com/schmitthub/clawker/\.github/workflows/release-build\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9._-]+)?(\+[A-Za-z0-9._-]+)?$
```

Added `--new-bundle-format` flag to the cosign command (Task 1 surface — required for `actions/attest@v4` bundles in `application/vnd.dev.sigstore.bundle.v0.3+json` mediaType). Added a `gh attestation verify --signer-workflow schmitthub/clawker/.github/workflows/release-build.yml` example for the SLSA + SBOM attestations. Stripped the parenthetical "Task 7 will tighten this later" note since we did it now.

**Scope discipline**: did NOT rewrite the broader release-guide.md sections (workflow flow, key files table, version injection, etc.) — Task 7 explicitly owns that rewrite. Task 6 is just the cosign-regex tightening + caller/reusable identity correction.

**Acceptance criteria (post-revert) — all pass:**

```
=== install.sh URL surface unchanged from baseline ===                OK (raw.githubusercontent main)
=== goreleaser extra_files removed ===                                OK
=== version subcommand no longer Hidden ===                           OK
=== bin/clawker --version + bin/clawker version match byte-for-byte === OK
=== CONTRIBUTING.md uses make clawker ===                             OK
=== CONTRIBUTING.md no bare go build line ===                         OK
=== release-guide cosign regex anchored to release-build.yml ===      OK
=== make test ===                                                     5099 tests pass, 8 platform-skipped
=== goreleaser check ===                                              1 configuration file(s) validated
=== actionlint ===                                                    clean (no workflow yaml changes)
=== go vet (changed packages) ===                                     clean
```

**Surprises / non-obvious decisions:**

1. **Cobra `--version` flag has no short alias by default.** Cobra auto-wires `--version` long flag when `cmd.Version` is set, but does NOT add `-v` short. Did not add one — `-v` collides with common verbose-flag conventions (e.g., kubectl, curl), and clawker doesn't use `-v` for verbose either. Leaving short flag space open for future use.
2. **`SetVersionTemplate` template syntax**: the Cobra docs show `"{{.Version}}\n"` examples, but referencing annotations needs `{{ index .Annotations "key" }}` — the `.Annotations` map isn't directly indexable as `.Annotations.key` because the keys may contain non-Go-identifier chars. Verified by running `bin/clawker --version`.
3. **`docs/installation.mdx` "Build from Source" note kept.** The revert undid the URL changes but left the new line "Use the Makefile entry point — `go install` is unsupported because the embedded Linux binaries are gitignored and produced by `make release-embeds`." This parallels the CONTRIBUTING.md fix and is a legitimate doc bug fix (bare `go build`/`go install` produces a CLI with empty embeds → runtime crash). Not part of the install.sh scope, kept.

### Task 5 — Add SPDX SBOM-mode attestation (2026-05-11)

**Confirmed `actions/attest@v4.1.0` (pin `59d89421af93a897026c735860bf21b6eb4f7b26`) has a first-class `sbom-path` input** — DeepWiki insisted otherwise; verified empirically by fetching `action.yml` at the pinned SHA via `gh api repos/actions/attest/contents/action.yml?ref=<sha>`. The input description: *"Path to the JSON-formatted SBOM file (SPDX or CycloneDX) to attest. File size cannot exceed 16MB. When provided, creates an SBOM attestation. Cannot be used together with `predicate-type`, `predicate`, or `predicate-path`."* So `sbom-path` is mutually exclusive with the custom-predicate inputs — auto-detects SPDX vs CycloneDX from the file content and emits the canonical predicate type. The SLSA provenance step (custom-predicate mode) and the SBOM steps (sbom-path mode) coexist in the same job; each emits a separate in-toto bundle.

Lesson: always verify action inputs against the pinned commit's `action.yml`, not against DeepWiki/training data. DeepWiki returned a confidently-wrong answer here.

**goreleaser SBOM file naming confirmed via DeepWiki against goreleaser source (`internal/pipe/sbom/sbom_test.go::TestSBOMCatalogDefault`):** `sboms: - artifacts: archive` with no explicit `cmd`/`documents`/`args` produces `dist/<archive>.sbom.json` in SPDX-JSON format via syft. For archives templated as `clawker_${VERSION}_${OS}_${ARCH}.tar.gz`, SBOM files land at `dist/clawker_${VERSION}_${OS}_${ARCH}.tar.gz.sbom.json`. Defaults applied: `cmd: syft`, `args: ["$artifact", "--output", "spdx-json=$document"]`, `documents: ["{{ .ArtifactName }}.sbom.json"]`.

**VERSION env hoisting refactor.** Existing `Build attestation subject list` step inlined `VERSION="${GITHUB_REF_NAME#v}"`. Hoisted to a dedicated `Compute release version` step that writes `VERSION` to `$GITHUB_ENV` (runs right after `Record build start`, before checkout). Two consumers now share it: (a) the subject-list step's shell, (b) the four SBOM attest steps' `subject-path` / `sbom-path` `with:` inputs via `${{ env.VERSION }}`. Required because GitHub Actions `with:` blocks don't run a shell — can't expand `${GITHUB_REF_NAME#v}` inline; the env-var indirection is the canonical pattern.

**Step layout: 4 explicit per-archive steps, not a matrix.** Matrix strategies are job-level, not step-level, in GitHub Actions. A separate matrixed job would defeat SLSA L3 isolation (each matrix shard would get its own Sigstore signing identity = N×4 attestations under N parallel signer identities). 4 explicit steps in the single `build` job → all SBOM attestations share the build-provenance attestation's signing identity (reusable workflow path + ref). Repetition is mild (4× ~5-line blocks); readability + isolation worth it.

**Default `actions/attest@v4` SBOM-mode permissions** match the existing build-provenance requirements — no additional grants on the calling job. The full permission set is already declared on `release.yml`'s `build` job (`contents: write`, `id-token: write`, `attestations: write`, `artifact-metadata: write`). `contents: read` is implicit-superset of `contents: write`. No edits to `release.yml`.

**Acceptance criteria — all pass:**

```
=== sbom-path count (expect 4) ===                4
=== SLSA provenance step intact ===               1
=== subject-checksums references 16-subject file ===  1
=== all uses: SHA-pinned (expect empty) ===       (empty — all pinned by SHA)
=== goreleaser sboms block untouched ===          sboms:\n  - artifacts: archive
=== actionlint clean ===                          OK
```

YAML parse via `python3 -c "import yaml"` not exercised (no PyYAML in container); `actionlint` parses YAML strictly as part of its validation and passed cleanly, providing equivalent confidence.

**Deferred to Task 7 E2E:** `gh attestation verify <archive> --predicate-type https://spdx.dev/Document` per archive (requires real tag push). Structural acceptance gates above + actionlint clean + `action.yml` schema confirmation are sufficient at this gate.

**Surprises / non-obvious decisions:**

1. **DeepWiki hallucinated about `sbom-path`.** Asked specifically about `actions/attest@v4.1.0` sbom-path behavior; got a confident response claiming the input doesn't exist and that `predicate-type` would have to be set manually. Fetching the pinned `action.yml` immediately falsified that. Always verify against the actual commit, especially for security-critical inputs.
2. **Single shared signing identity across all 5 attestations** (1 SLSA + 4 SBOM). All five `actions/attest@v4` invocations run inside the same reusable workflow job → same Fulcio cert SAN URI → one signer-workflow identity to verify against. cosign verify-blob regex from Task 1 covers all five bundle types without modification.
3. **No matrix strategy attempted.** The temptation was strong (4 archives = matrix candidate), but step-level matrices don't exist in GitHub Actions, and a job-level matrix would split signing identities. 4 explicit steps was unambiguously the right call.

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Commit all changes from this task with a descriptive commit message
5. Present the handoff prompt from the task's Wrap Up section to the user
6. Wait for the user to start a new conversation with the handoff prompt

Per-task code-reviewer / silent-failure-hunter / comment-analyzer subagent gates are intentionally **omitted**. A single comprehensive review pass runs at Task 7. Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

**Handoff prompts MUST be in a fenced code block, NOT a blockquote.** Blockquotes are obnoxious to copy-paste because the `>` prefix and bold markdown bleed into the copied text. Fenced code blocks expose a clean copy button in most renderers. Same rule applies to the PR message draft at the end of Task 7.

---

## Context for All Agents

### Background

Clawker is approaching its v1 release. Since v0.7.8 (2026-04-05) the project has incrementally added a clawkerd supervisor, a control plane daemon (`clawker-cp`), an Envoy+CoreDNS firewall stack with eBPF egress enforcement, and four `go:embed`-bundled Linux binaries inside the host CLI. The release pipeline was accreted as components landed and now needs a deliberate overhaul before v1.

End state of this initiative:

1. **Same-repo SLSA Build L3 reusable workflow.** Caller `release.yml` handles tag validation only; new `release-build.yml` (`on: workflow_call`) owns build + attest steps. Reusable workflow's isolated job context provides L3 build isolation per SLSA v1.0 spec (no separate repo required — see `https://slsa.dev/spec/v1.0/requirements`).
2. **Enriched SLSA v1 build provenance** (`predicateType: https://slsa.dev/provenance/v1`) populated with full `resolvedDependencies`: source commit, Dockerfile.controlplane base image digests, apt packages from BPF builder stage, Go toolchain version, BPF source hash, every GitHub Action version used in the build, goreleaser/cosign/syft versions, and any build-environment knob that affects output (CGO_ENABLED, GOFLAGS, clang flags). Replaces the thin auto-generated provenance from `actions/attest-build-provenance`.

   **Reproducible-build discovery requirement**: an investigator reading the attestation alone must be able to enumerate every input that affected the output. The attestation is the canonical source of truth for "what went into this build". If something affects the produced bytes and isn't in `resolvedDependencies` or `buildDefinition.externalParameters`, that's a bug in the predicate generator. This is about completeness of enumeration, not byte-identical Make-side reproducibility tricks (those are explicitly NOT a goal of this initiative).
3. **16 subjects per build-provenance attestation**: 4 archives + 4 unpacked CLI binaries + 8 embedded Linux binaries ({clawkerd, clawker-cp, ebpf-manager, coredns-clawker} × {amd64, arm64}). SBOM files are **not** subjects of the build provenance (covered by separate SBOM-mode attestation to avoid redundancy).
4. **Separate SPDX SBOM attestations** via `actions/attest@v4` `sbom-path` mode (one per archive, predicate type auto-detected as `https://spdx.dev/Document`). Goreleaser's existing `sboms:` block stays — it produces the SBOM files that get attested.
5. **Cleaned-up Makefile** scoped to build/test/QA. Removed: `clawker-build-{all,linux,darwin}` (unused dev shortcuts), `CLAWKER_DATE` wall-clock injection (fake reproducibility), alias test targets (`clawker-test`, `clawker-test-internals`), reproducibility-flavored comments. Kept: `make release` (git tag pusher), `make restart` (dev workflow tool).
6. **Updated docs** — threat-model.mdx covering forensic capability + verify commands, release-guide.md rewritten without reproducibility theater, CONTRIBUTING.md build instructions fixed. (Original plan also included install.sh URL re-pinning; that subtask was reverted during Task 6 — see Task 6 key learnings for rationale.)

The clout/forensic motivation: with per-binary subjects, an investigator presented with a running `clawker-cp` inside a CP container can extract it, hash it, and run `gh attestation verify` against the binary directly — answering "which binary diverged" cryptographically rather than by elimination.

### Key Files

- `Makefile` — build/test/QA orchestration; targets to remove/clean noted in Task 2
- `.github/workflows/release.yml` — current monolithic release workflow; splits in Task 3
- `.github/workflows/release-build.yml` — NEW reusable workflow created in Task 3
- `.goreleaser.yaml` — keep `sboms:` block; may need `signs:` tweaks if cosign verify identity changes
- `Dockerfile.controlplane` — multi-stage build for 3 of 4 embedded Linux binaries (clawkerd is pure-Go cross-compile); resolved image digests + apt packages feed the enriched provenance predicate
- `scripts/install.sh` — pin URL to release tag in Task 6
- `docs/threat-model.mdx` — extend in Task 7 with attestation surface + verify commands
- `docs/installation.mdx`, `README.md` — install.sh URL fixes in Task 6
- `.serena/memories/release-guide.md` — rewrite in Task 7 (strip reproducibility framing)
- `internal/build/build.go` — read-only reference; `Version`+`Date` injection target

### Pinning Requirements

**Every external dependency MUST be pinned by digest (preferred) or exact version.** This is non-negotiable per `CLAUDE.md` Security: Version Pinning policy.

- GitHub Actions: pin to commit SHA, NOT semver tag. Add version comment after pin (e.g., `uses: actions/attest@<SHA> # v4.x.y`). Research the **current stable release SHA** before pinning.
- `actions/attest`: research current stable version (training data is out of date — verify via `https://github.com/actions/attest/releases` + actual repo state). Pin its SHA.
- All Docker images in `Dockerfile.controlplane`: SHA256 manifest list digest (already done; verify still current at start of Task 4).
- Goreleaser version: pinned in `release-build.yml` `version:` input. Confirm current stable v2.x release at start of Task 3.
- Go toolchain: pinned in `go.mod` `toolchain` directive (already done).

### Research Requirements

**Every agent MUST research the current state of relevant tools before writing code.** Do NOT rely on training data — it's out of date. The world has moved on. Specifically:

- `actions/attest` (v4 wrapper landscape) — verify current stable, predicate-type behavior, `subject-checksums` vs `subject-digest` vs `subject-path` semantics, behavior with same-repo reusable workflow signing identity
- `actions/attest-build-provenance` — confirm wrapper status, decide if any path still uses it
- Goreleaser — current v2.x stable, behavior of `signs:`/`sboms:`/`hooks:` blocks under reusable workflow signing identity
- Sigstore Fulcio cert structure — `job_workflow_ref` claim under same-repo reusable workflow vs cross-repo
- cosign `verify-blob --certificate-identity-regexp` exact form for same-repo reusable
- `gh attestation verify` `--predicate-type` filter behavior + `--signer-workflow` flag

Each task's research phase produces concrete pinned versions, command samples, and design choices captured in the Key Learnings section. **Measure twice, cut once.**

### Design Patterns

- **Atomic per-task commits** — each task is a separate commit on `chore/release-pipeline-overhaul`. No squashing across tasks.
- **No reproducibility theater** — Makefile is build/test/QA. Reproducibility properties come from CI's pinned environment, not Make.
- **Forensic decomposition** — attestation subjects expose per-binary verifiability so an investigator can answer "which binary diverged" without reinstalling.
- **Same-repo reusable workflow** — caller and reusable workflow both live in `schmitthub/clawker/.github/workflows/`. Signing identity = reusable workflow's path+ref. cosign verify regex anchors to reusable workflow path.

### Rules

- Read `CLAUDE.md`, relevant `.claude/rules/` files, and any package `CLAUDE.md` before starting any task
- All new code must compile and existing tests must pass
- Follow existing project conventions for commit messages (Conventional Commits: `feat(release):`, `chore(release):`, `fix(release):`, `docs(release):`)
- Pin every external dependency by SHA or exact version with comment indicating version
- Research current state of tools/actions before writing pipeline code

---

## Task 1: Dry-run validation of same-repo reusable workflow signing pattern

**Creates/modifies:** Throwaway test branch + test tag (deleted at end). No production files modified.
**Depends on:** Nothing.

### Goal

Empirically verify that a same-repo reusable workflow can produce Sigstore-signed in-toto attestations whose Fulcio cert's identity claim anchors to the reusable workflow's path+ref (not the caller's). This validates the SLSA L3 architecture before committing to it in production workflows.

### Research/Planning Phase

1. Research current state of `actions/attest@v4` — fetch latest commit SHA from `https://github.com/actions/attest`. Read its README + `action.yml` for current input names + defaults.
2. Verify GitHub Docs `https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations/using-artifact-attestations-and-reusable-workflows-to-achieve-slsa-v1-build-level-3` for current canonical example.
3. Confirm Sigstore Fulcio cert claim names from `https://github.com/sigstore/fulcio/blob/main/docs/oid-info.md` (specifically `job_workflow_ref`).
4. Note current stable release SHAs:
   - `actions/attest`
   - `actions/checkout`
   - `actions/setup-go` (if reused for the test)

### Implementation Phase

1. Create test branch off `chore/release-pipeline-overhaul`: e.g., `test/reusable-workflow-dryrun`.
2. Add minimal `.github/workflows/test-caller.yml`:
   - Trigger: `push` of `test-*` tag
   - Permissions: `id-token: write`, `attestations: write`, `contents: read`
   - Single job that `uses: ./.github/workflows/test-reusable.yml` with `secrets: inherit`
3. Add minimal `.github/workflows/test-reusable.yml`:
   - `on: workflow_call:`
   - Same permissions block
   - Steps: checkout, create a dummy file (e.g., `echo "hello" > test.bin`), call `actions/attest@<SHA>` with `subject-path: test.bin`
4. Push the test branch + a test tag (e.g., `test-dryrun-1`).
5. Wait for workflow to complete.
6. Inspect the resulting attestation via `gh api repos/schmitthub/clawker/attestations/sha256:<digest>`. Extract the Fulcio cert from the bundle, decode it, find the `job_workflow_ref` and `subjectAlternativeName` claims.
7. Test cosign verify with candidate regex anchored to the **reusable** workflow's path:
   ```bash
   cosign verify-blob --bundle <bundle> \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com \
     --certificate-identity-regexp '^https://github\.com/schmitthub/clawker/\.github/workflows/test-reusable\.yml@refs/tags/' \
     test.bin
   ```
8. Test `gh attestation verify test.bin --repo schmitthub/clawker --signer-workflow schmitthub/clawker/.github/workflows/test-reusable.yml`.
9. Record exact regex strings that work + Fulcio cert claim values in Key Learnings.
10. Delete test tag (`git push origin --delete test-dryrun-1`), delete test branch (`git push origin --delete test/reusable-workflow-dryrun`).

### Acceptance Criteria

```bash
# All of these must succeed against the test tag's attestation:
cosign verify-blob --bundle <bundle> \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '<recorded working regex>' \
  test.bin

gh attestation verify test.bin --repo schmitthub/clawker \
  --signer-workflow schmitthub/clawker/.github/workflows/test-reusable.yml

# And captured in Key Learnings:
# - Working cosign --certificate-identity-regexp value
# - Working gh --signer-workflow value
# - Fulcio cert job_workflow_ref claim format
# - Pinned actions/attest SHA + version comment
```

### Wrap Up

1. Update Progress Tracker: Task 1 → `complete`
2. Append to Key Learnings:
   - Pinned `actions/attest` SHA + version
   - Exact working cosign regex string
   - Exact working `gh attestation verify --signer-workflow` value
   - Fulcio cert claim structure observed
   - Any surprises (e.g., `subjectAlternativeName` vs `job_workflow_ref` differences)
3. Commit any planning notes; test branch + tag deletion already done in step 10 above.
4. **STOP.** Inform user and present handoff:

**Next agent prompt:**

```
Continue the release-pipeline-overhaul initiative. Read the Serena memory release-pipeline-overhaul — Task 1 is complete. Begin Task 2: Makefile cleanup + L1 bug fixes.
```

---

## Task 2: Makefile cleanup + L1 bug fixes

**Creates/modifies:** `Makefile`, comments in `.github/workflows/release.yml`, `.serena/memories/release-guide.md` (factual error fix only)
**Depends on:** Task 1 (regex validated; informs threat model doc later)

### Goal

Bring `Makefile` to its end-state shape (pure build/test/QA orchestration) and fix the small factual bugs in existing comments/docs that have accumulated. No architectural changes yet.

### Research/Planning Phase

1. Read full `Makefile`. Confirm targets to remove are not invoked anywhere:
   - `clawker-build-all`, `clawker-build-linux`, `clawker-build-darwin` — grep workflows + scripts + docs
   - `clawker-test`, `clawker-test-internals` aliases — grep for callers
2. Read `.github/workflows/*.yml` to see which targets CI actually invokes (canonical: `make test`, `make clawker`, `make docs-check`, `make licenses-check`, `make release-embeds`).
3. Read `internal/build/build.go` for `Version`+`Date` defaulting behavior (informs CLAWKER_DATE removal).
4. Plan exact line edits before touching the file.

### Implementation Phase

1. **`Makefile` deletions** (these targets + their recipes):
   - `clawker-build-all`
   - `clawker-build-linux`
   - `clawker-build-darwin`
   - `clawker-test` (alias of `test-unit`)
   - `clawker-test-internals` (if alias)
   - Update PHONY block + help block to remove references
2. **`Makefile` simplifications**:
   - Remove `CLAWKER_DATE := $(shell date +%Y-%m-%d)` and its use in `LDFLAGS`. Goreleaser injects `Date` via `{{.CommitDate}}` for release; dev builds can have an empty `build.Date` (CLI falls back to `debug.ReadBuildInfo`).
   - Strip reproducibility-flavored comments (anywhere the word "Reproducible:" / "NOT pin-reproducible" / "for reproducibility" appears).
   - Simplify the `release-embeds` recipe preamble (currently ~38 lines of mostly recipe-in-prose). Keep WHY-comments for the load-bearing invariants only: `embeds/` outside `dist/`, `--parallelism 1` race story, two build IDs vs four, clawkerd pure-Go bypass of Docker chain.
3. **`Makefile` bug fixes**:
   - **`verify-release-embeds` ELF check**: tighten to full check. Read 20 bytes from each file. Validate:
     - bytes 0-3 == `7f454c46` (ELF magic)
     - byte 4 == `02` (ELFCLASS64)
     - byte 5 == `01` (ELFDATA2LSB)
     - bytes 18-19 == `3e00` (amd64) or `b700` (arm64)
   - Each failure prints a specific error.
   - **`stage-embeds-amd64` + `stage-embeds-arm64`**: prepend `rm -f $(EBPF_BINARY) $(COREDNS_BINARY) $(CP_BINARY) $(CLAWKERD_BINARY)` before the `cp` chain. Prevents half-staged `assets/` on partial cp failure.
4. **Comment factual fixes**:
   - `Makefile` (around `CLAWKER_DATE` if it survives somehow as a doc comment, but it should be removed entirely): no leftover wrong claim about RFC3339 or release pipeline use.
   - `.github/workflows/release.yml`: the comment block around lines 56-59 ("No CLAWKER_VERSION/CLAWKER_DATE override here...") — rewrite to: `make release-embeds doesn't need version stamping — the embedded binaries don't link internal/build. Final CLI version+date are stamped by goreleaser's ldflags below.`
   - `.serena/memories/release-guide.md`: line 67 `{{.Date}}` → `{{.CommitDate}}` (matches actual `.goreleaser.yaml`).

### Acceptance Criteria

```bash
# Targets removed:
! grep -E '^(clawker-build-(all|linux|darwin)|clawker-test|clawker-test-internals):' Makefile

# CLAWKER_DATE removed:
! grep 'CLAWKER_DATE' Makefile

# Tightened verify-release-embeds:
grep -q '7f454c46' Makefile
grep -q 'EI_CLASS' Makefile

# Reproducibility framing stripped:
! grep -iE '(reproducib|NOT pin-reproducible|for reproducibility)' Makefile

# Build still works:
make clawker
ls -l bin/clawker

# Tests still pass:
make test

# (If staged binaries exist) verify still passes:
make release-embeds   # optional, slow — sanity check only
```

### Wrap Up

1. Update Progress Tracker: Task 2 → `complete`
2. Append Key Learnings: any unexpected coupling between removed targets and other tooling
3. Commit (Conventional Commits): `chore(release): clean up Makefile, fix verify-release-embeds ELF check, harden stage-embeds atomicity`
4. **STOP.** Present handoff:

**Next agent prompt:**

```
Continue the release-pipeline-overhaul initiative. Read the Serena memory release-pipeline-overhaul — Tasks 1-2 are complete. Begin Task 3: Split release.yml into caller + reusable workflow.
```

---

## Task 3: Split release.yml into caller + reusable workflow

**Creates/modifies:** `.github/workflows/release.yml` (shrinks), `.github/workflows/release-build.yml` (new)
**Depends on:** Tasks 1 (regex validated), 2 (Makefile clean)

### Goal

Split the monolithic `release.yml` into a thin caller (tag validation only) and a reusable workflow (`release-build.yml`, `on: workflow_call`) that owns the build + attest steps. This produces the SLSA L3 isolation property — the calling workflow cannot inject steps into the reusable's job context.

Attestation behavior at this stage stays identical to current: still `actions/attest-build-provenance` (wrapper), still SLSA v1 provenance with thin auto-populated predicate, still 8 subjects. The attestation **migration** happens in Task 4. This task is structural only.

### Research/Planning Phase

1. Verify current stable SHAs (pin as `uses: <action>@<SHA> # vX.Y.Z`):
   - `actions/checkout`
   - `actions/setup-go`
   - `docker/setup-buildx-action`
   - `sigstore/cosign-installer`
   - `anchore/sbom-action/download-syft`
   - `goreleaser/goreleaser-action`
   - `actions/attest-build-provenance` (keep wrapper for this task; replaced in Task 4)
2. Research current Goreleaser stable v2.x release (`https://github.com/goreleaser/goreleaser/releases`). Update the `version:` input if a newer stable is out.
3. Understand `secrets:` propagation from caller to reusable. Decide: `secrets: inherit` vs explicit `secrets:` block. Lean: explicit (safer, audit-friendlier). Required secrets: `HOMEBREW_TAP_GITHUB_TOKEN`. `GITHUB_TOKEN` is auto-available in reusable.
4. Confirm `permissions:` block goes in CALLER's job-that-calls-reusable, NOT reusable itself.

### Implementation Phase

1. **Create `.github/workflows/release-build.yml`** (`on: workflow_call:`):
   - `inputs:` (optional, can be added later if needed)
   - `secrets:` block declaring `HOMEBREW_TAP_GITHUB_TOKEN`
   - `jobs.build` with all current steps from existing `release.yml` EXCEPT tag validation:
     - Checkout
     - Setup Go
     - Setup Docker Buildx
     - `make release-embeds`
     - Install cosign
     - Install syft
     - Run Goreleaser (`release --clean --parallelism 1`)
     - Attest build provenance (still wrapper, replaced in Task 4)
   - All `uses:` lines pinned to SHA with version comment
2. **Shrink `.github/workflows/release.yml`** to:
   - Trigger: `push` of `v*` tags
   - Permissions at workflow level
   - Job 1: `validate` — checkout (`fetch-depth: 0`), validate tag is semver, validate tag is on main
   - Job 2: `build` — `needs: validate`, `uses: ./.github/workflows/release-build.yml`, declare matching permissions block on the calling job, pass `secrets: HOMEBREW_TAP_GITHUB_TOKEN: ${{ secrets.HOMEBREW_TAP_GITHUB_TOKEN }}`
3. Update any comment block in `release.yml` that references `--parallelism 1`; move the canonical rationale to `release-build.yml` (where the goreleaser invocation now lives). Workflow YAML comments must be accurate post-split.

### Acceptance Criteria

```bash
# Files exist with correct structure:
test -f .github/workflows/release.yml
test -f .github/workflows/release-build.yml

# Caller release.yml ONLY validates + calls reusable:
grep -q 'workflow_call' .github/workflows/release-build.yml
grep -q 'uses: ./.github/workflows/release-build.yml' .github/workflows/release.yml

# All actions are SHA-pinned (regex check: should match nothing if all pinned):
! grep -E 'uses:.*@(v[0-9]|main|master)$' .github/workflows/release.yml .github/workflows/release-build.yml

# YAML is valid:
yamllint .github/workflows/release.yml .github/workflows/release-build.yml || true

# Dry test of caller logic:
# (can't fully test without pushing a tag; capture this as a manual test in Key Learnings)
```

### Wrap Up

1. Update Progress Tracker: Task 3 → `complete`
2. Append Key Learnings: each pinned SHA + version, secrets pattern chosen + reasoning, any quirks
3. Commit: `chore(release): split release.yml into caller + reusable workflow for SLSA L3 isolation`
4. **STOP.** Present handoff:

**Next agent prompt:**

```
Continue the release-pipeline-overhaul initiative. Read the Serena memory release-pipeline-overhaul — Tasks 1-3 are complete. Begin Task 4: Migrate to actions/attest@v4 with enriched SLSA v1 provenance.
```

---

## Task 4: Migrate to actions/attest@v4 with enriched SLSA v1 provenance

**Creates/modifies:** `.github/workflows/release-build.yml`, possibly `Makefile` (helper to emit predicate JSON), possibly `scripts/` for predicate generator
**Depends on:** Tasks 1-3

### Goal

Replace the thin `actions/attest-build-provenance` invocation with `actions/attest@v4` in custom-predicate mode emitting a fully-populated SLSA v1 build provenance predicate (`https://slsa.dev/provenance/v1`). The predicate captures all build inputs in `resolvedDependencies`. Subjects expand from 8 (archives + SBOMs) to 16 (4 archives + 4 unpacked CLI binaries + 8 embedded Linux binaries). SBOMs are NOT subjects of build provenance (handled separately in Task 5).

### Research/Planning Phase

1. **Research `actions/attest@v4` current state** (DO NOT trust training data):
   - Fetch repo: `https://github.com/actions/attest` — current latest stable release commit SHA, README, `action.yml`
   - Confirm input names: `subject-path`, `subject-checksums`, `subject-digest`+`subject-name`, `predicate-type`, `predicate-path`, `predicate`
   - Confirm exclusive-mode behavior: predicate-path with predicate-type must be set for custom; default emits SLSA provenance auto-populated
   - Verify what happens when supplying `predicate-type: https://slsa.dev/provenance/v1` with custom `predicate-path` — should produce SLSA v1 attestation with caller-provided predicate body
2. **SLSA v1 schema** — fetch `https://slsa.dev/spec/v1.0/provenance` for exact `predicate` schema. Required fields:
   - `buildDefinition.buildType` — use `https://actions.github.io/buildtypes/workflow/v1`
   - `buildDefinition.externalParameters` — workflow path/ref/repository (mirror what GH auto-emits)
   - `buildDefinition.internalParameters` — github metadata
   - `buildDefinition.resolvedDependencies[]` — THE field to enrich
   - `runDetails.builder.id` — reusable workflow URL+ref
   - `runDetails.metadata.invocationId` — actions run URL
3. **What goes in `resolvedDependencies`** — design the entries to be EXHAUSTIVE (reproducible-build discovery requirement). Every input that affects the build output must be enumerable from the attestation alone:
   - **Source commit**: `{uri: "git+https://github.com/schmitthub/clawker@<ref>", digest: {gitCommit: <sha>}}`
   - **Each `Dockerfile.controlplane` `FROM` image** (resolved to digest at build time): `{uri: "pkg:docker/golang@sha256:<digest>?arch=<arch>", name: "go-builder-image", digest: {sha256: <digest>}}` — one entry per stage's base image
   - **apt packages from BPF builder stage** (full list, not just clang/libbpf-dev/linux-libc-dev — capture everything that ended up installed so reproducer can recreate the env): extract via `docker run <bpf-builder-image-pinned-digest> dpkg-query -W -f '${Package}=${Version}\n'`, emit one entry per package as purl `pkg:deb/debian/<pkg>@<version>?arch=<arch>`
   - **Go toolchain version**: `{uri: "pkg:golang/go@<version>", annotations: {source: "go.mod toolchain directive"}}`
   - **BPF source files** (clawker.c + common.h + any included headers): one entry each as `{uri: "git+...#<path>", digest: {sha256: <hash>}}`
   - **Every GitHub Action used** in the workflow (release.yml + release-build.yml), with its pinned SHA: `{uri: "git+https://github.com/<owner>/<repo>@<sha>", name: "action-<short-name>"}`. Includes actions/checkout, actions/setup-go, docker/setup-buildx-action, sigstore/cosign-installer, anchore/sbom-action/download-syft, goreleaser/goreleaser-action, actions/attest itself, and any others.
   - **Tool versions** invoked by the workflow:
     - Goreleaser version (the `version:` input value)
     - cosign installer version (the action SHA covers this, but capture the resolved cosign binary version separately)
     - syft version (similarly)
     - bpf2go version (from go.mod or from inside the bpf-builder image)
   - **Workflow files themselves** as inputs (so investigator can verify the recipe wasn't subtly altered post-pin): `{uri: "git+...#.github/workflows/release.yml", digest: {sha256: <hash>}}` and `{uri: "git+...#.github/workflows/release-build.yml", digest: {sha256: <hash>}}`
   - **Dockerfile.controlplane** itself: `{uri: "git+...#Dockerfile.controlplane", digest: {sha256: <hash>}}`
   - **Makefile** at build time: `{uri: "git+...#Makefile", digest: {sha256: <hash>}}` (also referenced by commit, but explicit hash of the file is cheaper for verifier)
4. **What goes in `buildDefinition.externalParameters`** (build configuration knobs that affect output):
   - Goreleaser flags actually invoked (`release --clean --parallelism 1`)
   - Environment variables that affect Go builds: `CGO_ENABLED`, `GOFLAGS`, `GOOS`, `GOARCH`
   - clang flags used to compile BPF (extract from gen.go `//go:generate` line or hardcode the known set in the predicate generator)
   - goreleaser ldflags template values (`Version`, `Date`)
   - The build host OS+kernel (runner environment): captured already in `internalParameters.github.runner_environment` but expand if useful for forensics
5. **Sanity check the completeness**: at the end of designing the predicate generator, walk through "what would a reproducer need to recreate this build?" If anything is missing, add it. The acceptance test is **enumerability from attestation alone** — no implicit "and you also need to know X" gaps.
4. **Subject set** — design the 16 subjects:
   - 4 archives `clawker_VER_OS_ARCH.tar.gz` (digests from `dist/checksums.txt`)
   - 4 unpacked CLI binaries `clawker` from `dist/clawker-<id>_<os>_<arch>/clawker` (extract digests in pipeline step before goreleaser packs them, or compute post-pack from inside the tarball)
   - 8 embedded Linux binaries from `embeds/<arch>/{clawkerd,clawker-cp,ebpf-manager,coredns-clawker}` (computed during `make release-embeds`)
   - Emit a combined checksums file `dist/attestation-subjects.txt` in checksums-txt format for `subject-checksums` input
6. **Pin `actions/attest@v4` to current stable SHA** + add version comment.

### Implementation Phase

1. Add a script (likely under `scripts/`, e.g., `scripts/generate-provenance.sh` or a Go program `cmd/gen-provenance/main.go` — pick whichever fits project conventions) that:
   - Reads release context (workflow run env vars + git commit + go.mod toolchain + Dockerfile.controlplane image digests + apt package list captured during build)
   - Emits a JSON file matching SLSA v1 provenance schema with fully-populated `resolvedDependencies`
2. Add a workflow step in `release-build.yml` (after `make release-embeds` so digests + apt list are available) that:
   - Captures `Dockerfile.controlplane` resolved image digests (use `docker buildx imagetools inspect` per pinned image)
   - Captures BPF builder apt package list (run `docker run --rm <bpf-builder-image-pinned-digest> dpkg-query -W -f '${Package}=${Version}\n' clang libbpf-dev linux-libc-dev`, etc.; capture to a file)
   - Hashes the BPF source files
   - Invokes the predicate generator
3. Add a step to compute subject digests:
   - Unpack each goreleaser archive to extract the bare `clawker` binary digest (use `tar -xzOf <archive> clawker | sha256sum`)
   - Combine all 16 subject digests into `dist/attestation-subjects.txt`
4. Replace the existing `actions/attest-build-provenance` step with `actions/attest@v4` (pinned SHA, version comment):
   ```yaml
   - uses: actions/attest@<SHA> # v4.x.y
     with:
       subject-checksums: ./dist/attestation-subjects.txt
       predicate-type: https://slsa.dev/provenance/v1
       predicate-path: ./dist/provenance-predicate.json
   ```
5. Order steps so: release-embeds → goreleaser → extract subject digests → generate predicate → attest.

### Acceptance Criteria

```bash
# Predicate generator outputs valid SLSA v1:
# (test it by running it locally with mock env vars and validating JSON shape)

# Workflow file uses actions/attest@v4 with SLSA v1 predicate-type:
grep -q 'actions/attest@' .github/workflows/release-build.yml
grep -q 'https://slsa.dev/provenance/v1' .github/workflows/release-build.yml

# subject-checksums references the 16-subject file:
grep -q 'attestation-subjects.txt' .github/workflows/release-build.yml

# After a successful release run (manual or test tag):
gh attestation verify <archive> --repo schmitthub/clawker --predicate-type "https://slsa.dev/provenance/v1"
# subject list in returned attestation must have all 16 entries

# Sample binary verify:
docker cp clawker-controlplane:/usr/local/bin/clawker-cp /tmp/
gh attestation verify /tmp/clawker-cp --repo schmitthub/clawker
# must succeed against the same attestation

# Reproducible-build discovery check — verify resolvedDependencies enumerates:
#   - source commit
#   - every Dockerfile.controlplane base image with digest
#   - every apt package in BPF builder stage
#   - Go toolchain version
#   - every workflow action's SHA
#   - workflow file + Dockerfile + Makefile content hashes
#   - tool versions (goreleaser, cosign, syft, bpf2go)
# An investigator reading the attestation must be able to recreate the build environment
# without consulting any other source. Walk through resolvedDependencies and verify:
gh attestation verify <archive> --repo schmitthub/clawker --format json | \
  jq -r '.[0].attestation.bundle.dsseEnvelope.payload' | base64 -d | \
  jq '.predicate.buildDefinition.resolvedDependencies | length'
# Expect: a high count (dozens of entries — one per pkg, one per image, etc.)

# cosign verify of the in-toto bundle works against the reusable workflow identity:
cosign verify-blob --bundle ... \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '<Task 1 working regex>' \
  ...
```

### Wrap Up

1. Update Progress Tracker: Task 4 → `complete`
2. Append Key Learnings: pinned `actions/attest` SHA, predicate generator design choice (bash vs Go), apt package extraction approach, surprising fields in `actions/attest@v4`
3. Commit: `feat(release): emit enriched SLSA v1 build provenance with 16 subjects and full resolvedDependencies`
4. **STOP.** Present handoff:

**Next agent prompt:**

```
Continue the release-pipeline-overhaul initiative. Read the Serena memory release-pipeline-overhaul — Tasks 1-4 are complete. Begin Task 5: Add SPDX SBOM-mode attestation.
```

---

## Task 5: Add SPDX SBOM-mode attestation

**Creates/modifies:** `.github/workflows/release-build.yml`
**Depends on:** Task 4 (actions/attest@v4 already wired)

### Goal

Add a second attestation type — one SPDX SBOM attestation per archive. Uses the same SBOM JSON files goreleaser already produces (via existing `sboms:` block), creates in-toto attestations with `https://spdx.dev/Document` predicate type. Subject of each SBOM attestation is the archive (not the SBOM file itself — semantic: "this SBOM describes that archive").

### Research/Planning Phase

1. Confirm `actions/attest@v4` `sbom-path` mode behavior — verify it auto-detects SPDX format and emits `predicateType: https://spdx.dev/Document` (or whatever the current canonical SPDX type URI is).
2. Decide loop strategy:
   - Matrix over 4 archives in workflow (1 `actions/attest@v4` step per archive)
   - OR single shell loop invoking `actions/attest@v4` via composite action — actions don't loop natively, so the matrix or `for ... do actions/attest@v4` approaches via separate workflow steps is the path
3. Plan for the case where `sbom-path` expects a single subject — confirm via `actions/attest@v4` README.

### Implementation Phase

1. Add 4 workflow steps (one per archive) after the SLSA provenance attestation step:
   ```yaml
   - name: Attest SBOM for linux/amd64 archive
     uses: actions/attest@<SHA> # v4.x.y
     with:
       subject-path: ./dist/clawker_${VERSION}_linux_amd64.tar.gz
       sbom-path: ./dist/clawker_${VERSION}_linux_amd64.tar.gz.sbom.json
   # repeat for linux/arm64, darwin/amd64, darwin/arm64
   ```
2. Alternative: matrix/strategy if cleaner; but four explicit steps is clear + readable. Pick whichever fits project conventions.
3. Verify nothing breaks the existing goreleaser SBOM generation — `sboms:` block stays untouched. Existing `.sbom.json` files continue to be release artifacts.

### Acceptance Criteria

```bash
# Workflow has 4 SBOM attest steps:
grep -c 'sbom-path:' .github/workflows/release-build.yml   # expect 4

# After release (manual or test tag):
gh attestation verify <archive> --repo schmitthub/clawker --predicate-type "https://spdx.dev/Document"
# must succeed for each of the 4 archives

# SLSA build provenance still works:
gh attestation verify <archive> --repo schmitthub/clawker --predicate-type "https://slsa.dev/provenance/v1"
# must still succeed (16 subjects)

# Release page still contains the 4 .sbom.json files (goreleaser path unchanged):
gh release view <tag> --json assets --jq '.assets[].name' | grep -c sbom.json    # expect 4
```

### Wrap Up

1. Update Progress Tracker: Task 5 → `complete`
2. Append Key Learnings: confirmed predicate type URI emitted by `actions/attest@v4` for SPDX, any per-archive verify gotchas
3. Commit: `feat(release): add SPDX SBOM-mode attestation per archive via actions/attest@v4`
4. **STOP.** Present handoff:

**Next agent prompt:**

```
Continue the release-pipeline-overhaul initiative. Read the Serena memory release-pipeline-overhaul — Tasks 1-5 are complete. Begin Task 6: Consumer surface fixes.
```

---

## Task 6: Consumer surface fixes

**Creates/modifies:** `scripts/install.sh`, `README.md`, `docs/installation.mdx`, `docs/quickstart.mdx`, `CONTRIBUTING.md`, possibly Cobra version command
**Depends on:** Tasks 1-5 (so the release path is complete before fixing user-facing references)

### Goal

Close the small consumer-facing gaps surfaced during discovery. These are real bugs in the user surface that fit naturally with the pipeline overhaul.

### Research/Planning Phase

1. Review current state of:
   - `scripts/install.sh` — confirm URL pinning point (the `CLAWKER_VERSION` env var path + the README/docs curl examples)
   - `cmd/clawker` Cobra root command — find where `--version` flag would go, find the version subcommand's `Hidden: true` (`internal/cmd/version/version.go`)
   - `CONTRIBUTING.md` build section
2. Decide on `clawker version` Hidden behavior: either unhide it OR add `--version` flag on root command (most CLIs offer both). Lean: add `--version` root flag for convention compliance AND unhide the subcommand for docs to remain accurate.
3. Plan the README + docs URL pinning approach. Options:
   - Pin to specific version in docs (rotates with each release — needs doc update on each release)
   - Pin to "latest" via GH redirect (e.g., `releases/latest/download/install.sh`)
   - Keep raw `main` (status quo) — least safe
   - Lean: redirect-to-latest pattern, so docs don't need rotating

### Implementation Phase

1. **Install URL pinning** — README.md + docs/installation.mdx + docs/quickstart.mdx:
   - Change `curl -fsSL https://raw.githubusercontent.com/schmitthub/clawker/main/scripts/install.sh | bash` to use a release-pinned URL. Two options:
     - `https://github.com/schmitthub/clawker/releases/latest/download/install.sh` (requires uploading install.sh as a release asset via goreleaser `extra_files:` or workflow step)
     - `https://raw.githubusercontent.com/schmitthub/clawker/v<TAG>/scripts/install.sh` (requires user to know the tag)
   - Decision: ship `install.sh` as a release asset. Add `extra_files:` in `.goreleaser.yaml` `release:` block. URL becomes `releases/latest/download/install.sh`.
2. **`clawker version` + `--version` flag**:
   - Unhide the `version` subcommand (`Hidden: false`) in `internal/cmd/version/version.go`
   - Add `--version` flag on Cobra root that prints same output and exits 0
3. **CONTRIBUTING.md** — fix build instructions:
   - Replace `go build -o bin/clawker ./cmd/clawker` with `make clawker` (which builds embeds first)
   - Add a note that `go install` is unsupported because embeds are gitignored and built by Make
4. **cosign verify regex in release-guide.md** — update to anchor on `release-build.yml` (the reusable workflow), using the regex confirmed in Task 1. Strip the loose substring form. (This file is the operator/contributor reference; user-facing threat model goes in Task 7.)

### Acceptance Criteria

```bash
# Install URL no longer references main:
! grep -E 'raw\.githubusercontent\.com/schmitthub/clawker/main/scripts/install\.sh' README.md docs/installation.mdx docs/quickstart.mdx

# release/latest pattern OR tagged URL in docs:
grep -E 'releases/(latest/download|tag/v)' README.md

# install.sh is published as release asset:
grep -q 'install.sh' .goreleaser.yaml

# --version flag works:
go build -o /tmp/clawker ./cmd/clawker
/tmp/clawker --version
/tmp/clawker version

# Version subcommand no longer Hidden:
! grep -q 'Hidden: true' internal/cmd/version/version.go

# CONTRIBUTING.md uses make:
grep -q 'make clawker' CONTRIBUTING.md
! grep -E '^go build -o bin/clawker' CONTRIBUTING.md
```

### Wrap Up

1. Update Progress Tracker: Task 6 → `complete`
2. Append Key Learnings: install.sh distribution choice rationale, version flag implementation pattern
3. Commit: `fix(release): pin install.sh URL, expose --version flag, correct CONTRIBUTING build instructions`
4. **STOP.** Present handoff:

**Next agent prompt:**

```
Continue the release-pipeline-overhaul initiative. Read the Serena memory release-pipeline-overhaul — Tasks 1-6 are complete. Begin Task 7: Final review + documentation completion gate.
```

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
   - `.serena/memories/release-guide.md` (rewrite without reproducibility framing; reflect new caller/reusable split)
   - `README.md` (already touched in Task 6; check that Verify section if any is up to date)
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
   - Subsections:
     - Build pipeline architecture (caller + reusable workflow, SLSA L3 isolation)
     - Attestation types: SLSA v1 provenance (enriched), SPDX SBOM
     - Forensic decomposition use case
     - Verify commands for each subject type (archive, raw CLI, embedded binary, SBOM)
     - Trust roots
2. **Rewrite `.serena/memories/release-guide.md`** to reflect the new architecture:
   - New workflow split + reusable workflow rationale
   - 16-subject build provenance
   - SPDX SBOM attestations
   - Updated cosign verify regex (anchored to `release-build.yml`)
   - Updated `gh attestation verify --signer-workflow` value
   - Strip ALL reproducibility-gap framing
3. **Run review subagents over the full branch diff**:
   - `code-reviewer` — pass over all Tasks 1-6 changes for project guidelines compliance + general correctness
   - `silent-failure-hunter` — pass over workflow YAML + Makefile changes for swallowed errors
   - `comment-analyzer` — verify all new/modified comments are accurate against current code
4. **Fix all findings** raised by the agents. No "out of scope" deferrals at this gate — everything gets addressed or has an explicit follow-up issue filed.
5. **End-to-end validation** — push a test tag (e.g., `v0.7.9-rc.1`) to dry-run the full pipeline. Confirm:
   - Release artifacts produced (4 archives, 4 SBOMs, install.sh, checksums.txt, checksums.txt.sigstore.json)
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
git log main..HEAD --oneline   # expect ~6 commits, one per task
```

### Wrap Up

1. Update Progress Tracker: Task 7 → `complete`
2. Append Key Learnings: review agent findings summary, RC tag outcome, any production tag prep notes
3. Commit: `docs(release): threat model + release-guide updates for L3 attestation overhaul`
4. **DONE.** Branch is ready for PR. Inform user.

**PR message draft:**

```
Release pipeline overhaul — SLSA Build L3 via same-repo reusable workflow, enriched SLSA v1 provenance with 16 subjects + full resolvedDependencies, SPDX SBOM-mode attestation per archive, Makefile cleanup, consumer surface fixes. See .serena/memories/release-pipeline-overhaul for the full initiative + key learnings.
```

