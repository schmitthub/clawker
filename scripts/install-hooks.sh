#!/usr/bin/env bash
#
# install-hooks.sh — Install advisory pre-commit freshness check for Claude docs.
#
set -euo pipefail

HOOK_MARKER="# clawker-freshness-check"

# ── Verify git repo ──────────────────────────────────────────────────────────
REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
    echo "Error: not inside a git repository." >&2
    exit 1
}

HOOK_DIR=$(git rev-parse --git-path hooks 2>/dev/null)
HOOK_FILE="${HOOK_DIR}/pre-commit"
SCRIPT_PATH="scripts/check-claude-freshness.sh"

# ── Check script exists ──────────────────────────────────────────────────────
if [[ ! -f "${REPO_ROOT}/${SCRIPT_PATH}" ]]; then
    echo "Error: ${SCRIPT_PATH} not found in repo root." >&2
    exit 1
fi

# ── Build hook snippet ───────────────────────────────────────────────────────
HOOK_SNIPPET=$(cat <<'HOOKEOF'

# clawker-freshness-check
# Advisory freshness check — never blocks commits
if git diff --cached --name-only --diff-filter=ACM -- '*.go' | grep -q '.'; then
    echo ""
    echo "=== Claude Doc Freshness (advisory) ==="
    bash scripts/check-claude-freshness.sh --no-color 2>/dev/null | head -50 || true
    echo ""
fi
HOOKEOF
)

# ── Check for existing hook ──────────────────────────────────────────────────
if [[ -f "$HOOK_FILE" ]]; then
    if grep -q "$HOOK_MARKER" "$HOOK_FILE"; then
        echo "Freshness hook already installed in ${HOOK_FILE}. Skipping."
        exit 0
    fi
    echo "Existing pre-commit hook found. Appending freshness check."
    echo "$HOOK_SNIPPET" >> "$HOOK_FILE"
else
    echo "Creating pre-commit hook."
    cat > "$HOOK_FILE" <<'SHEBANG'
#!/usr/bin/env bash
SHEBANG
    echo "$HOOK_SNIPPET" >> "$HOOK_FILE"
fi

chmod +x "$HOOK_FILE"
echo "Done. Freshness check installed in ${HOOK_FILE}."
