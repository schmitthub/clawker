#!/usr/bin/env bash
#
# check-claude-freshness.sh — Compare CLAUDE.md/rules timestamps against Go source files.
# Advisory by default; use --strict to exit 1 on warnings.
#
set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
FRESHNESS_DAYS="${FRESHNESS_DAYS:-30}"
STRICT=false
NO_COLOR=false

# ── Parse flags ───────────────────────────────────────────────────────────────
usage() {
    cat <<'EOF'
Usage: check-claude-freshness.sh [OPTIONS]

Compare CLAUDE.md and rules file timestamps against Go source files.

Options:
  --strict      Exit 1 if any warnings are found
  --no-color    Disable colored output
  --days=N      Set freshness threshold (default: 30, or $FRESHNESS_DAYS)
  --help        Show this help message
EOF
    exit 0
}

for arg in "$@"; do
    case "$arg" in
        --strict)    STRICT=true ;;
        --no-color)  NO_COLOR=true ;;
        --days=*)    FRESHNESS_DAYS="${arg#--days=}" ;;
        --help)      usage ;;
        *)           echo "Unknown option: $arg" >&2; exit 1 ;;
    esac
done

# ── Colors ────────────────────────────────────────────────────────────────────
if [[ "$NO_COLOR" == "true" ]] || [[ ! -t 1 ]]; then
    RED="" GREEN="" YELLOW="" BOLD="" RESET=""
else
    RED='\033[0;31m' GREEN='\033[0;32m' YELLOW='\033[0;33m'
    BOLD='\033[1m' RESET='\033[0m'
fi

# ── Platform-portable date formatting ─────────────────────────────────────────
format_date() {
    local ts="$1"
    if [[ "$(uname)" == "Darwin" ]]; then
        date -r "$ts" "+%Y-%m-%d"
    else
        date -d "@$ts" "+%Y-%m-%d"
    fi
}

days_between() {
    local older="$1" newer="$2"
    echo $(( (newer - older) / 86400 ))
}

# ── Git timestamp helper ─────────────────────────────────────────────────────
# Returns Unix epoch of most recent commit touching the given path(s).
# Returns 0 if no commits found.
git_timestamp() {
    local ts
    ts=$(git log --format=%at -1 -- "$@" 2>/dev/null || echo "0")
    echo "${ts:-0}"
}

# Find newest .go file commit under a directory (or repo-wide if ".")
newest_go_in() {
    local dir="$1" glob
    if [[ "$dir" == "." ]]; then
        glob="*.go"
    else
        glob="${dir}/*.go"
    fi
    local newest_file="" newest_ts=0

    while IFS= read -r gofile; do
        local ts
        ts=$(git_timestamp "$gofile")
        if (( ts > newest_ts )); then
            newest_ts=$ts
            newest_file=$gofile
        fi
    done < <(git ls-files "$glob" 2>/dev/null)

    echo "$newest_ts $newest_file"
}

# ── Main ──────────────────────────────────────────────────────────────────────
cd "$(git rev-parse --show-toplevel)"

CHECKED=0
WARNINGS=0
THRESHOLD_SECS=$(( FRESHNESS_DAYS * 86400 ))

check_doc() {
    local doc_path="$1" go_dir="$2"
    CHECKED=$((CHECKED + 1))

    local doc_ts
    doc_ts=$(git_timestamp "$doc_path")
    if [[ "$doc_ts" == "0" ]]; then
        echo -e "${BOLD}${doc_path}${RESET}"
        echo "  Status:        SKIPPED (not committed)"
        echo ""
        return
    fi

    local result
    result=$(newest_go_in "$go_dir")
    local go_ts go_file
    go_ts="${result%% *}"
    go_file="${result#* }"

    if [[ "$go_ts" == "0" ]] || [[ -z "$go_file" ]]; then
        echo -e "${BOLD}${doc_path}${RESET}"
        echo "  Doc updated:   $(format_date "$doc_ts")"
        echo "  Newest Go:     (none found in ${go_dir}/)"
        echo -e "  Status:        ${GREEN}OK${RESET} (no Go files)"
        echo ""
        return
    fi

    local behind
    behind=$(days_between "$doc_ts" "$go_ts")

    echo -e "${BOLD}${doc_path}${RESET}"
    echo "  Doc updated:   $(format_date "$doc_ts")"
    echo "  Newest Go:     ${go_file} ($(format_date "$go_ts"))"

    if (( behind > FRESHNESS_DAYS )); then
        WARNINGS=$((WARNINGS + 1))
        echo -e "  Status:        ${YELLOW}WARNING${RESET} - ${behind} days behind (threshold: ${FRESHNESS_DAYS})"
    else
        if (( behind < 0 )); then behind=0; fi
        echo -e "  Status:        ${GREEN}OK${RESET} (${behind} days behind)"
    fi
    echo ""
}

# ── Check root CLAUDE.md ─────────────────────────────────────────────────────
echo -e "${BOLD}=== Claude Documentation Freshness Check ===${RESET}"
echo ""

if [[ -f "CLAUDE.md" ]]; then
    check_doc "CLAUDE.md" "."
fi

# ── Check internal/*/CLAUDE.md ────────────────────────────────────────────────
while IFS= read -r doc; do
    dir=$(dirname "$doc")
    check_doc "$doc" "$dir"
done < <(git ls-files 'internal/*/CLAUDE.md' 'internal/*/*/CLAUDE.md' 'pkg/*/CLAUDE.md' 2>/dev/null)

# ── Check .claude/rules/*.md with paths: frontmatter ─────────────────────────
while IFS= read -r rule; do
    # Extract paths from frontmatter: paths: ["glob1", "glob2"]
    paths_line=$(head -5 "$rule" | grep '^paths:' || true)
    if [[ -z "$paths_line" ]]; then
        continue
    fi

    CHECKED=$((CHECKED + 1))
    local_doc_ts=$(git_timestamp "$rule")
    if [[ "$local_doc_ts" == "0" ]]; then
        echo -e "${BOLD}${rule}${RESET}"
        echo "  Status:        SKIPPED (not committed)"
        echo ""
        continue
    fi

    # Parse glob patterns from paths: ["glob1", "glob2"]
    globs=$(echo "$paths_line" | sed 's/^paths: *\[//;s/\].*$//;s/"//g;s/,/ /g')

    newest_ts=0
    newest_file=""
    for glob in $globs; do
        while IFS= read -r gofile; do
            ts=$(git_timestamp "$gofile")
            if (( ts > newest_ts )); then
                newest_ts=$ts
                newest_file=$gofile
            fi
        done < <(git ls-files "$glob" -- '*.go' 2>/dev/null || git ls-files "${glob}/**/*.go" 2>/dev/null || true)
    done

    echo -e "${BOLD}${rule}${RESET}"
    echo "  Doc updated:   $(format_date "$local_doc_ts")"

    if [[ "$newest_ts" == "0" ]] || [[ -z "$newest_file" ]]; then
        echo "  Newest Go:     (none found for paths: ${globs})"
        echo -e "  Status:        ${GREEN}OK${RESET} (no Go files)"
    else
        behind=$(days_between "$local_doc_ts" "$newest_ts")
        echo "  Newest Go:     ${newest_file} ($(format_date "$newest_ts"))"
        if (( behind > FRESHNESS_DAYS )); then
            WARNINGS=$((WARNINGS + 1))
            echo -e "  Status:        ${YELLOW}WARNING${RESET} - ${behind} days behind (threshold: ${FRESHNESS_DAYS})"
        else
            if (( behind < 0 )); then behind=0; fi
            echo -e "  Status:        ${GREEN}OK${RESET} (${behind} days behind)"
        fi
    fi
    echo ""
done < <(git ls-files '.claude/rules/*.md' 2>/dev/null)

# ── Summary ───────────────────────────────────────────────────────────────────
echo "---"
if (( WARNINGS > 0 )); then
    echo -e "Checked ${CHECKED} files, ${YELLOW}${WARNINGS} warning(s)${RESET} (threshold: ${FRESHNESS_DAYS} days)"
else
    echo -e "Checked ${CHECKED} files, ${GREEN}0 warnings${RESET} (threshold: ${FRESHNESS_DAYS} days)"
fi

if [[ "$STRICT" == "true" ]] && (( WARNINGS > 0 )); then
    exit 1
fi
exit 0
