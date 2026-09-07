#!/usr/bin/env bash
#
# install-hooks.sh — Install prek git hooks for all CI quality gates.
#
# Usage: bash scripts/install-hooks.sh
#
set -euo pipefail

# ── Verify git repo ──────────────────────────────────────────────────────────
REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
    echo "Error: not inside a git repository." >&2
    exit 1
}

# ── Check prek is installed ──────────────────────────────────────────────────
if ! command -v prek >/dev/null 2>&1; then
    echo "Error: prek is not installed." >&2
    echo "" >&2
    echo "Install with one of:" >&2
    echo "  uv tool install prek" >&2
    echo "  brew install prek" >&2
    echo "  cargo install --locked prek" >&2
    exit 1
fi

# ── Check optional tool binaries ─────────────────────────────────────────────
MISSING=()
command -v gitleaks    >/dev/null 2>&1 || MISSING+=("gitleaks    — brew install gitleaks")
command -v semgrep     >/dev/null 2>&1 || MISSING+=("semgrep     — pip install semgrep")
command -v govulncheck >/dev/null 2>&1 || MISSING+=("govulncheck — go install golang.org/x/vuln/cmd/govulncheck@v1.7.0")
command -v golangci-lint >/dev/null 2>&1 || MISSING+=("golangci-lint — brew install golangci-lint")

if [[ ${#MISSING[@]} -gt 0 ]]; then
    echo "Warning: some hook binaries are not installed. Those hooks will fail until installed:" >&2
    for m in "${MISSING[@]}"; do
        echo "  $m" >&2
    done
    echo "" >&2
fi

# ── Install hooks ────────────────────────────────────────────────────────────
cd "$REPO_ROOT"
# --force replaces any hook script left behind by another manager (pre-commit).
prek install --force

echo ""
echo "prek hooks installed. They will run automatically on 'git commit'."
echo ""
echo "Useful commands:"
echo "  prek run --all-files            Run all hooks against entire repo"
echo "  prek run gitleaks --all-files   Run a single hook"
echo "  make pre-commit                 Alias for run --all-files"
echo "  git commit --no-verify          Skip hooks (emergency only)"
