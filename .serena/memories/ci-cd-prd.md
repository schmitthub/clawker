# Clawker CI/CD — Product Requirements Document

**Author:** Andrew
**Status:** Implemented
**Last Updated:** 2026-02-11

---

## 1. Overview

This document defines the CI/CD pipeline for Clawker, a Go-based CLI tool for containerized development environments. The pipeline enforces security, quality, and correctness gates across the development lifecycle using GitHub Actions, with tag-triggered releases via GoReleaser following a trunk-based development model.

### 1.1 Goals

- Block merges of insecure, low-quality, or broken code via required PR checks
- Provide fast feedback on PRs (target: security + quality + unit tests < 5 minutes)
- Validate merged code on `main` with the full suite including integration tests
- Automate binary builds and GitHub Releases on version tags
- Use open-source tooling exclusively for all pipeline stages

### 1.2 Branching Model

Trunk-based development on `main`. Short-lived feature branches merge via squash-merge PR. No direct commits to `main`. Releases are cut by pushing a semver tag (e.g., `v1.2.3`) to `main`.

---

## 2. Pipeline Architecture

```
PR into main (open/update)               Push to main                   Tag Push (vX.Y.Z)
┌──────────────────────────┐     ┌───────────────────────────┐   ┌───────────────────────┐
│  Security Scan           │     │  Security Scan            │   │  GoReleaser Build     │
│  ├─ Semgrep (SAST)       │     │  ├─ Semgrep (SAST)        │   │  ├─ Cross-compile     │
│  ├─ Gitleaks (secrets)   │     │  ├─ Gitleaks (secrets)     │   │  ├─ Checksums         │
│  └─ govulncheck (SCA)   │     │  └─ govulncheck (SCA)     │   │  ├─ SBOM (syft)       │
│                          │     │                           │   │  ├─ Signing (cosign)  │
│  Quality Gate            │     │  Quality Gate             │   │  └─ GitHub Release    │
│  ├─ golangci-lint        │     │  ├─ golangci-lint         │   │                       │
│  └─ Coverage reporting   │     │  └─ Coverage reporting    │   └───────────────────────┘
│                          │     │                           │
│  Unit Tests              │     │  Unit Tests               │
│  └─ go test ./...        │     │  └─ go test ./...         │
│                          │     │                           │
│  ❌ Failure → block PR   │     │  Integration Tests        │
└──────────────────────────┘     │  ├─ Container lifecycle   │
                                 │  ├─ Git worktree tests    │
                                 │  ├─ Host proxy features   │
                                 │  └─ CLI smoke tests       │
                                 │                           │
                                 │  ⚠️  Failure → breaks     │
                                 │     main, fix immediately │
                                 └───────────────────────────┘
```

---

## 3. Stage 1 — PR Checks (Security, Quality, Tests)

**Trigger:** `pull_request` events (`opened`, `synchronize`, `reopened`) targeting `main`
**Requirement:** All jobs must pass before the PR is merge-eligible. Enforced via GitHub branch protection required status checks.

### 3.1 Security Scan

| Tool | Purpose | Configuration |
|------|---------|---------------|
| **Semgrep** | SAST — find bugs, anti-patterns, and security issues in Go code | Use `p/golang` and `p/security-audit` rulesets. Consider adding `p/docker` for Dockerfile scanning. Run via `returntocorp/semgrep-action`. |
| **Gitleaks** | Secrets detection — catch committed credentials, tokens, keys | Run via `gitleaks/gitleaks-action`. Default rules cover common secret patterns (AWS keys, GitHub tokens, private keys, etc.). Add a `.gitleaks.toml` later if false positives arise. |
| **govulncheck** | SCA — known vulnerabilities in Go dependencies | Run via `golang/govulncheck-action`. Operates on the call graph, so it only flags vulnerabilities in code paths you actually use — significantly fewer false positives than generic SCA. |

**Recommendation:** Prefer `govulncheck` over tools like Trivy or Snyk for Go dependency scanning. Generic SCA tools flag every CVE in any transitive dependency regardless of reachability. `govulncheck` uses Go's call graph analysis to only report vulnerabilities in functions your code actually invokes, which dramatically reduces noise. It's maintained by the Go security team and pulls from the official Go vulnerability database.

### 3.2 Quality Gate

**Primary tool: `golangci-lint`**

`golangci-lint` is the Go ecosystem's standard meta-linter. Rather than running `gofmt`, `staticcheck`, `govet`, etc. as separate pipeline steps, configure them all as linters within a single `.golangci.yml`. This gives you one cache, one invocation, and one config file.

**Recommended `.golangci.yml` baseline:**

```yaml
run:
  timeout: 5m

linters:
  enable:
    # Defaults (already on, but explicit is better)
    - errcheck
    - gosimple
    - govet
    - ineffassign
    - staticcheck
    - unused

    # Formatting
    - gofmt
    - goimports

    # Bug detection
    - bodyclose        # unclosed HTTP response bodies
    - nilerr           # returning nil when err != nil
    - exportloopref    # loop variable capture bugs

    # Style & maintainability
    - misspell
    - unconvert        # unnecessary type conversions
    - unparam          # unused function parameters
    - prealloc         # slice preallocation hints

    # Security-adjacent
    - gosec            # security-oriented checks

linters-settings:
  govet:
    enable-all: true
  gofmt:
    simplify: true
  staticcheck:
    checks: ["all"]

issues:
  max-issues-per-linter: 0
  max-same-issues: 0
```

**Coverage:** Run `go test -coverprofile=coverage.out ./...` and report coverage in CI output. Skip enforcing a hard threshold initially — instead, post coverage as a PR comment (via `codecov/codecov-action` or a simple script parsing `go tool cover -func`) so it's visible. Once you have a few weeks of data, set a floor at whatever the natural baseline is and ratchet upward from there.

**Recommendation:** Enable linters incrementally. Start with the defaults plus `gofmt` and `gosec`, then enable additional linters one at a time, fixing existing violations or adding `//nolint` directives with justification comments. Avoid the temptation to enable everything at once — it creates a wall of noise that gets ignored.

### 3.3 Unit Tests

```
go test -race -count=1 -coverprofile=coverage.out ./...
```

- `-race` enables the race detector. Non-negotiable for concurrent Go code.
- `-count=1` disables test caching in CI to ensure tests always run.
- Use the existing Makefile target for unit tests (e.g., `make test-unit`) so CI and local development stay in sync. The Makefile should exclude the integration test directory.

**Recommendation:** Use build tags to separate test tiers rather than directory conventions. This is the idiomatic Go approach and integrates cleanly with `go test` filtering:
  - No tag or `//go:build unit` → runs in PR checks
  - `//go:build integration` → runs in merge queue only

> **Note:** Integration tests currently live in a root-level package and are run via Makefile targets. The CI workflows should invoke the same Makefile targets (e.g., `make test-unit`, `make test-integration`) rather than duplicating `go test` invocations. This keeps CI and local development in sync. If the Makefile targets don't yet cleanly separate unit from integration tests, that's a prerequisite task before CI implementation. Build tags can be adopted later as a refinement, but directory + Makefile separation works fine.

---

## 4. Stage 2 — Push to Main (Full Suite)

**Trigger:** `push` to `main` branch
**Purpose:** Run the full suite — all PR checks plus integration tests — on every commit that lands on `main`. This validates that merged code works end-to-end.

### 4.1 Trade-offs

This approach runs integration tests post-merge, meaning a failure won't block the merge itself — the code is already on `main`. The trade-off is simplicity: no merge queue ceremony, no temporary merge commits, no extra GitHub configuration. In practice, since PR checks (security, quality, unit tests) catch the vast majority of issues, integration failures on `main` should be rare.

**When integration tests fail on `main`:** Treat it as a "stop the line" event. Fix forward immediately or revert the offending commit. Consider setting up a Slack/Discord notification on `main` workflow failures so the team knows fast.

### 4.2 Integration Test Scope

Integration tests exercise Clawker's container orchestration, host integration, and end-to-end CLI behavior. These are slower and require Docker, which is why they're excluded from PR checks to keep feedback fast.

| Test Category | What It Covers |
|---------------|----------------|
| Container lifecycle | Build, start, exec, stop, remove containers via Docker SDK |
| Git worktree integration | Worktree creation, branch isolation, cleanup |
| Host proxy features | SSH agent forwarding, OAuth callback proxying |
| CLI smoke tests | Key user commands produce expected output and exit codes |

**Runner requirements:** These tests need Docker-in-Docker or a privileged runner. Use `ubuntu-latest` with Docker pre-installed, or a self-hosted runner if DinD proves flaky on GitHub-hosted runners.

### 4.3 Workflow Structure

The push-to-main workflow runs all PR checks (security, quality, unit tests) plus integration tests in parallel. This provides redundant validation of the merged code and catches any issues that slipped through due to a stale PR base.

```yaml
on:
  push:
    branches: [main]

jobs:
  security:
    # ... same as PR checks
  quality:
    # ... same as PR checks
  unit-tests:
    # ... same as PR checks
  integration:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: make test-integration  # Use existing Makefile target
```

**Recommendation:** Use a shared reusable workflow (`.github/workflows/checks.yml`) for the security, quality, and unit test jobs so both the PR and push-to-main workflows call the same definitions. This avoids duplication and drift between the two triggers.

---

## 5. Stage 3 — Release Pipeline

**Trigger:** Tag push matching `v*` pattern (e.g., `v1.0.0`, `v1.2.3-rc.1`)
**Precondition:** Tag must point to a commit on `main`.

### 5.1 Release Flow

```
Developer tags a commit on main:
  $ git tag v1.2.0
  $ git push origin v1.2.0
        │
        ▼
  GitHub Actions triggered (on push tag v*)
        │
        ▼
  GoReleaser builds:
  ├─ Cross-compiled binaries (linux/darwin/windows × amd64/arm64)
  ├─ SHA256 checksums
  ├─ SBOM via Syft (SPDX or CycloneDX format)
  ├─ Signatures via Cosign (keyless, OIDC-backed)
  └─ GitHub Release with changelog, binaries, and attestations
```

### 5.2 GoReleaser Configuration Notes

- Use `goreleaser/goreleaser-action` in the workflow
- Set `GITHUB_TOKEN` via `secrets.GITHUB_TOKEN` (automatic)
- Enable `--clean` flag to avoid stale artifacts

**Target platforms:**
```yaml
builds:
  - goos:
      - linux
      - darwin
    goarch:
      - amd64
      - arm64
```

**Changelog:** Since all PRs get squash-merged into `main`, the changelog will be a clean list of squash commit messages. GoReleaser can generate the changelog automatically from commits between the previous tag and the current one. Configure `changelog.sort` and `changelog.filters` to exclude noise like CI-only changes:

```yaml
changelog:
  sort: asc
  filters:
    exclude:
      - "^ci:"
      - "^docs:"
      - "^chore:"
```

This filtering uses commit message prefixes. You don't need to adopt formal "conventional commits" (a spec where every commit follows `type(scope): description`) — but if your squash merge titles naturally start with words like `fix:`, `feat:`, `ci:`, the filters will work well. If not, you can skip filtering entirely and just get a flat list of all commits since the last tag, which is fine for a solo/small-team project.

### 5.3 SBOM and Signing (Recommended)

This is increasingly becoming table stakes for open-source CLI tools.

**SBOM:** GoReleaser integrates with Syft natively. Add to `.goreleaser.yaml`:
```yaml
sboms:
  - artifacts: archive
```

**Signing:** Use Cosign in keyless mode (backed by Sigstore/Fulcio OIDC). This signs artifacts using the GitHub Actions OIDC identity — no key management required.
```yaml
signs:
  - cmd: cosign
    artifacts: checksum
    args:
      - "sign-blob"
      - "--yes"
      - "${artifact}"
      - "--output-signature=${signature}"
```

### 5.4 Tag Validation

Add a pre-check step in the release workflow that verifies:
- The tag follows semver (`v` prefix + `MAJOR.MINOR.PATCH`, optional pre-release suffix)
- The tagged commit exists on `main` (prevents accidental releases from feature branches)
- `go build` succeeds before invoking GoReleaser (fail fast)

```yaml
- name: Validate tag is on main
  run: |
    git fetch origin main
    if ! git merge-base --is-ancestor ${{ github.sha }} origin/main; then
      echo "::error::Tag must point to a commit on main"
      exit 1
    fi
```

---

## 6. GitHub Repository Configuration

### 6.1 Branch Protection Rules (main)

| Setting | Value |
|---------|-------|
| Require pull request reviews | ≥ 1 approval |
| Require status checks to pass | `security`, `quality`, `unit-tests` |
| Allow squash merging | Enabled (only merge strategy allowed) |
| Allow merge commits | Disabled |
| Allow rebase merging | Disabled |
| Restrict pushes | Only via PR (no direct pushes to main, except tag pushes) |
| Allow force pushes | Never |

### 6.2 Required Status Checks

These must be configured to match the `jobs.<job_id>` names in your PR workflow:

- `security` — Semgrep + Gitleaks + govulncheck
- `quality` — golangci-lint + coverage reporting
- `unit-tests` — `go test` without integration tag

The push-to-main workflow (`integration` job) is not a required status check since it runs post-merge. Configure failure notifications (Slack, email, or GitHub Actions failure alerts) so the team is immediately aware of `main` breakage.

### 6.3 Notifications

Set up workflow failure notifications for the push-to-main workflow. Options:
- GitHub's built-in email notifications for failed Actions runs
- A Slack webhook step that fires on `failure()` in the integration job
- GitHub's "Watch" settings per-repository (Actions → Only failures)

---

## 7. Workflow File Structure

```
.github/
├── workflows/
│   ├── checks.yml             # Reusable workflow: security, quality, unit tests
│   ├── pr.yml                 # Calls checks.yml on PRs into main
│   ├── main.yml               # Calls checks.yml + integration tests on push to main
│   └── release.yml            # GoReleaser on tag push
├── .golangci.yml              # Linter config (can also live at repo root)
.goreleaser.yaml               # Release build config
```

**Recommendation:** The shared `checks.yml` as a [reusable workflow](https://docs.github.com/en/actions/sharing-automations/reusing-workflows) keeps PR and push-to-main definitions in sync. Both `pr.yml` and `main.yml` call it with `uses: ./.github/workflows/checks.yml`. The `main.yml` workflow adds the integration test job alongside it.

---

## 8. Caching Strategy

| What | How |
|------|-----|
| Go modules | `actions/setup-go@v5` handles `~/go/pkg/mod` caching automatically via `go.sum` hash |
| Go build cache | Automatically cached by `actions/setup-go@v5` |
| golangci-lint cache | `golangci/golangci-lint-action` manages its own cache |
| Semgrep cache | `returntocorp/semgrep-action` caches rules |

No manual cache configuration should be necessary with current action versions.

---

## 9. Future Considerations

- **Merge queue for integration gating:** If integration failures on `main` become a recurring problem, GitHub Merge Queue can gate merges on integration test passage. Revisit if the "fix forward" approach proves insufficient.
- **Dependency update automation:** Dependabot or Renovate for automated `go.mod` PRs. Renovate is more configurable and supports grouping updates.
- **CODEOWNERS:** Define ownership for `internal/` subdirectories so the right reviewers are auto-assigned.
- **Nix/devcontainer for CI parity:** If local-vs-CI drift becomes a problem, consider a shared dev environment definition.
- **Self-hosted runners:** If integration tests become slow or require specific hardware (e.g., GPU for future features), move to self-hosted runners with ephemeral scaling.
- **Release channels:** Pre-release tags (`v1.2.0-rc.1`) can publish to a separate "pre-release" GitHub Release, letting early adopters test before promoting to stable.

---

## 10. Gotchas & Implementation Notes

- **Makefile targets must cleanly separate test tiers.** The CI workflows depend on distinct targets like `make test-unit` and `make test-integration`. If the existing Makefile doesn't have this separation, it needs to be added before CI implementation. Integration tests live in a root-level package and are separated by directory, not build tags.
- **Docker availability in CI.** Integration tests require Docker. GitHub-hosted `ubuntu-latest` runners have Docker pre-installed, but if tests use Docker Compose or need specific daemon configuration, verify that the runner environment supports it. DinD can be flaky — if it is, self-hosted runners are the fallback.
- **GoReleaser config may not exist yet.** If `.goreleaser.yaml` needs to be written from scratch, it must target `linux` and `darwin` only (`amd64` + `arm64`). No Windows builds.
- **Coverage baseline is unknown.** Do not set a coverage threshold on initial implementation. Report coverage only. A threshold can be introduced once there's enough data to set a realistic floor.
- **Notification channel for `main` breakage.** Integration test failures on `main` are post-merge and won't block anything. The workflow must include a failure notification step (Slack webhook, Discord, or email) so breakage is caught immediately. Decide on the channel before implementing the push-to-main workflow.
- **Squash merge commit messages drive the changelog.** GoReleaser generates release notes from commits between tags. Since all PRs are squash-merged, the quality of PR titles directly determines the quality of release changelogs. Consider establishing a PR title convention (even informal) if changelog readability matters.
- **`golangci-lint` is not yet tuned.** The `.golangci.yml` in this PRD is a recommended baseline. Enable linters incrementally — start with defaults plus `gofmt` and `gosec`, then add more one at a time. Enabling everything at once on an existing codebase will produce a wall of noise.