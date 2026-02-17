#!/usr/bin/env bash
#
# install-hooks.sh — Install pre-commit hooks for all CI quality gates.
#
# Usage: bash scripts/install-hooks.sh
#
set -euo pipefail

# ── Verify git repo ──────────────────────────────────────────────────────────
REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
    echo "Error: not inside a git repository." >&2
    exit 1
}

# ── Check pre-commit is installed ────────────────────────────────────────────
if ! command -v pre-commit >/dev/null 2>&1; then
    echo "Error: pre-commit is not installed." >&2
    echo "" >&2
    echo "Install with one of:" >&2
    echo "  brew install pre-commit" >&2
    echo "  pip install pre-commit" >&2
    echo "  pipx install pre-commit" >&2
    exit 1
fi

# ── Check optional tool binaries ─────────────────────────────────────────────
MISSING=()
command -v gitleaks    >/dev/null 2>&1 || MISSING+=("gitleaks    — brew install gitleaks")
command -v semgrep     >/dev/null 2>&1 || MISSING+=("semgrep     — pip install semgrep")
command -v govulncheck >/dev/null 2>&1 || MISSING+=("govulncheck — go install golang.org/x/vuln/cmd/govulncheck@latest")
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
pre-commit install

echo ""
echo "Pre-commit hooks installed. They will run automatically on 'git commit'."
echo ""
echo "Useful commands:"
echo "  pre-commit run --all-files          Run all hooks against entire repo"
echo "  pre-commit run gitleaks --all-files  Run a single hook"
echo "  make pre-commit                      Alias for run --all-files"
echo "  git commit --no-verify               Skip hooks (emergency only)"
