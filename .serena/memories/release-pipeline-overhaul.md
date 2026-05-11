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
| Task 2: Makefile cleanup + L1 bug fixes | `pending` | — |
| Task 3: Split release.yml into caller + reusable workflow | `pending` | — |
| Task 4: Migrate to actions/attest@v4 with enriched SLSA v1 provenance | `pending` | — |
| Task 5: Add SPDX SBOM-mode attestation | `pending` | — |
| Task 6: Consumer surface fixes | `pending` | — |
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
6. **Updated docs** — threat-model.mdx covering forensic capability + verify commands, release-guide.md rewritten without reproducibility theater, CONTRIBUTING.md build instructions fixed, install.sh URL pinned to release tag in README/docs.

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
