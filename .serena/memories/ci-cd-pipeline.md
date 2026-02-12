# CI/CD Pipeline

## Status: Implemented + PR Review Fixes (2026-02-12)

## Workflows

| File | Trigger | Purpose |
|------|---------|---------|
| `security.yml` | Reusable (workflow_call) | Semgrep SAST, Gitleaks secrets, govulncheck SCA; SARIF upload |
| `lint.yml` | Reusable (workflow_call) | golangci-lint with SARIF upload |
| `test.yml` | Reusable (workflow_call) | Unit tests via `make test-ci`, coverage artifact |
| `pr.yml` | PR to main | Orchestrates security + lint + test in parallel; cancel-in-progress |
| `main.yml` | Push to main | Same checks, no cancellation; placeholder for future integration tests |
| `release.yml` | Tag push (v*) | Semver + main validation, cosign + syft install, GoReleaser with SBOM + signing |

## Makefile Targets Added
- `test-unit` — alias for `test`
- `test-ci` — race detector, no cache, coverage profile (`coverage.out`)

## GoReleaser Additions
- `sboms: [{artifacts: archive}]` — SPDX SBOM per archive via syft
- `signs` — cosign keyless signing of checksums.txt via GitHub OIDC

## Required Branch Protection Status Checks
- `Security / Semgrep SAST`
- `Security / Gitleaks Secrets`
- `Security / govulncheck SCA`
- `Lint / golangci-lint`
- `Test / Unit Tests`

## CI/CD Permission & Compatibility Fixes (PR #112)
- Added `actions: read` permission to lint.yml, security.yml, pr.yml, main.yml (SARIF upload requires it)
- Added `pull-requests: read` to security.yml, pr.yml (gitleaks needs PR commits API)
- Upgraded `golangci/golangci-lint-action@v6` → `@v8` (v6 can't parse golangci-lint v2.x version strings)
- Replaced `--out-format=colored-line-number,sarif:golangci-lint.sarif` with `--output.sarif.path=golangci-lint.sarif` (v2 removed `--out-format`)
- `main.yml` intentionally omits `pull-requests: read` (push trigger, no PR API calls)

## PR Review Fixes Applied
- SARIF upload guards (`hashFiles()`) on all three upload steps (security.yml, lint.yml)
- Removed redundant `setup-go` from govulncheck job (action installs Go itself)
- Pinned golangci-lint to `v2.1.6` (was `latest`)
- Fixed semver regex to allow hyphens in prerelease + build metadata
- Quoted `${{ github.sha }}` in shell for defensive safety
- Guarded `go list` pipeline in Makefile (`UNIT_PKGS` variable + empty check)
- Coverage artifact uploaded on test failure (`if: always()`, `if-no-files-found: ignore`)
- Added Dependabot skip comment explaining the condition
- Improved future-jobs comment in main.yml

## Future Work
- SHA-pin all GitHub Actions (supply chain security, track separately)
- Pin cosign/syft to specific versions
- Fix `test-coverage` and `clawker-test` scope mismatch (pre-existing)
- Add `shell: bash` default to release.yml
- Integration test jobs in `main.yml`
- Coverage threshold enforcement
- Codecov integration
- `.gitleaks.toml` for false positive suppression
