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
NON_INTERACTIVE=false
WARN_LINES=()

# ── Parse flags ───────────────────────────────────────────────────────────────
usage() {
    cat <<'EOF'
Usage: check-claude-freshness.sh [OPTIONS]

Compare CLAUDE.md and rules file timestamps against Go source files.

Options:
  --strict            Exit 1 if any warnings are found
  --no-color          Disable colored output
  --non-interactive   Suppress normal output; only print warnings when exiting 1
  --days=N            Set freshness threshold (default: 30, or $FRESHNESS_DAYS)
  --help              Show this help message
EOF
    exit 0
}

for arg in "$@"; do
    case "$arg" in
        --strict)           STRICT=true ;;
        --no-color)         NO_COLOR=true ;;
        --non-interactive)  NON_INTERACTIVE=true ;;
        --days=*)           FRESHNESS_DAYS="${arg#--days=}" ;;
        --help)             usage ;;
        *)                  echo "Unknown option: $arg" >&2; exit 1 ;;
    esac
done

if [[ "$NON_INTERACTIVE" == "true" ]]; then
    NO_COLOR=true
fi

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
# Find newest .go file commit under a directory (or repo-wide if ".")
# Uses git log with :(glob) pathspec for reliable glob matching.
newest_go_in() {
    local dir="$1" pathspec
    if [[ "$dir" == "." ]]; then
        pathspec=":(glob)**/*.go"
    else
        pathspec=":(glob)${dir}/**/*.go"
    fi

    # Get the most recent commit touching any .go file under the path.
    # --diff-filter=ACMR excludes deleted files.
    local result
    result=$(git log --format="%at" --diff-filter=ACMR --name-only -1 -- "$pathspec" 2>/dev/null || true)
    if [[ -z "$result" ]]; then
        echo "0 "
        return
    fi

    local ts file
    ts=$(echo "$result" | head -1)
    # Filter to .go files only (git shows all files in the commit)
    file=$(echo "$result" | tail -n +2 | grep '\.go$' | head -1)
    echo "${ts:-0} ${file:-}"
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
        if [[ "$NON_INTERACTIVE" != "true" ]]; then
            echo -e "${BOLD}${doc_path}${RESET}"
            echo "  Status:        SKIPPED (not committed)"
            echo ""
        fi
        return
    fi

    local result
    result=$(newest_go_in "$go_dir")
    local go_ts go_file
    go_ts="${result%% *}"
    go_file="${result#* }"

    if [[ "$go_ts" == "0" ]] || [[ -z "$go_file" ]]; then
        if [[ "$NON_INTERACTIVE" != "true" ]]; then
            echo -e "${BOLD}${doc_path}${RESET}"
            echo "  Doc updated:   $(format_date "$doc_ts")"
            echo "  Newest Go:     (none found in ${go_dir}/)"
            echo -e "  Status:        ${GREEN}OK${RESET} (no Go files)"
            echo ""
        fi
        return
    fi

    local behind
    behind=$(days_between "$doc_ts" "$go_ts")

    if [[ "$NON_INTERACTIVE" != "true" ]]; then
        echo -e "${BOLD}${doc_path}${RESET}"
        echo "  Doc updated:   $(format_date "$doc_ts")"
        echo "  Newest Go:     ${go_file} ($(format_date "$go_ts"))"
    fi

    if (( behind > FRESHNESS_DAYS )); then
        WARNINGS=$((WARNINGS + 1))
        WARN_LINES+=("${doc_path}: ${behind} days behind (threshold: ${FRESHNESS_DAYS})")
        if [[ "$NON_INTERACTIVE" != "true" ]]; then
            echo -e "  Status:        ${YELLOW}WARNING${RESET} - ${behind} days behind (threshold: ${FRESHNESS_DAYS})"
        fi
    else
        if (( behind < 0 )); then behind=0; fi
        if [[ "$NON_INTERACTIVE" != "true" ]]; then
            echo -e "  Status:        ${GREEN}OK${RESET} (${behind} days behind)"
        fi
    fi
    if [[ "$NON_INTERACTIVE" != "true" ]]; then
        echo ""
    fi
}

# ── Check root CLAUDE.md ─────────────────────────────────────────────────────
if [[ "$NON_INTERACTIVE" != "true" ]]; then
    echo -e "${BOLD}=== Claude Documentation Freshness Check ===${RESET}"
    echo ""
fi

if [[ -f "CLAUDE.md" ]]; then
    check_doc "CLAUDE.md" "."
fi

# ── Check Lazy Loaded CLAUDE.md ────────────────────────────────────────────────
while IFS= read -r doc; do
    dir=$(dirname "$doc")
    check_doc "$doc" "$dir"
done < <(git ls-files 'internal/*/CLAUDE.md' 'internal/*/*/CLAUDE.md' 'pkg/*/CLAUDE.md' 'test/CLAUDE.md' 'test/*/CLAUDE.md' 'test/*/*/CLAUDE.md' 2>/dev/null)

# ── Check .claude/rules/*.md with paths: frontmatter ─────────────────────────
while IFS= read -r rule; do
    # Extract paths from frontmatter. Supports:
    #   paths: ["glob1", "glob2"]     (inline)
    #   paths:\n  - "glob1"\n  - "glob2"  (YAML array)
    globs=""
    # Read frontmatter (between --- markers)
    frontmatter=$(sed -n '/^---$/,/^---$/p' "$rule")
    if echo "$frontmatter" | grep -q '^paths:'; then
        # Inline format: paths: ["glob1", "glob2"]
        inline=$(echo "$frontmatter" | grep '^paths: *\[' || true)
        if [[ -n "$inline" ]]; then
            globs=$(echo "$inline" | sed 's/^paths: *\[//;s/\].*$//;s/"//g;s/,/ /g')
        else
            # Multi-line YAML array format
            globs=$(echo "$frontmatter" | sed -n '/^paths:/,/^[^ -]/p' | grep '^ *- ' | sed 's/^ *- *//;s/"//g' | tr '\n' ' ')
        fi
    fi

    if [[ -z "$globs" ]] || [[ "$globs" =~ ^[[:space:]]*$ ]]; then
        continue
    fi

    CHECKED=$((CHECKED + 1))
    local_doc_ts=$(git_timestamp "$rule")
    if [[ "$local_doc_ts" == "0" ]]; then
        if [[ "$NON_INTERACTIVE" != "true" ]]; then
            echo -e "${BOLD}${rule}${RESET}"
            echo "  Status:        SKIPPED (not committed)"
            echo ""
        fi
        continue
    fi

    newest_ts=0
    newest_file=""
    # Build pathspec args — disable globbing to prevent bash expansion of **
    pathspec_args=()
    set -f  # disable globbing
    for glob in $globs; do
        if [[ "$glob" == *.go ]]; then
            # Glob already targets .go files directly
            pathspec_args+=(":(glob)${glob}")
        else
            pathspec_args+=(":(glob)${glob}/*.go" ":(glob)${glob}/**/*.go")
        fi
    done
    set +f  # re-enable globbing

    local_result=$(git log --format="%at" --diff-filter=ACMR --name-only -1 -- "${pathspec_args[@]}" 2>/dev/null || true)
    if [[ -n "$local_result" ]]; then
        newest_ts=$(echo "$local_result" | head -1)
        newest_file=$(echo "$local_result" | tail -n +2 | grep '\.go$' | head -1)
    fi

    if [[ "$NON_INTERACTIVE" != "true" ]]; then
        echo -e "${BOLD}${rule}${RESET}"
        echo "  Doc updated:   $(format_date "$local_doc_ts")"
    fi

    if [[ "$newest_ts" == "0" ]] || [[ -z "$newest_file" ]]; then
        if [[ "$NON_INTERACTIVE" != "true" ]]; then
            echo "  Newest Go:     (none found for paths: ${globs})"
            echo -e "  Status:        ${GREEN}OK${RESET} (no Go files)"
        fi
    else
        behind=$(days_between "$local_doc_ts" "$newest_ts")
        if [[ "$NON_INTERACTIVE" != "true" ]]; then
            echo "  Newest Go:     ${newest_file} ($(format_date "$newest_ts"))"
        fi
        if (( behind > FRESHNESS_DAYS )); then
            WARNINGS=$((WARNINGS + 1))
            WARN_LINES+=("${rule}: ${behind} days behind (threshold: ${FRESHNESS_DAYS})")
            if [[ "$NON_INTERACTIVE" != "true" ]]; then
                echo -e "  Status:        ${YELLOW}WARNING${RESET} - ${behind} days behind (threshold: ${FRESHNESS_DAYS})"
            fi
        else
            if (( behind < 0 )); then behind=0; fi
            if [[ "$NON_INTERACTIVE" != "true" ]]; then
                echo -e "  Status:        ${GREEN}OK${RESET} (${behind} days behind)"
            fi
        fi
    fi
    if [[ "$NON_INTERACTIVE" != "true" ]]; then
        echo ""
    fi

done < <(git ls-files '.claude/rules/*.md' 2>/dev/null)

# ── Summary ───────────────────────────────────────────────────────────────────
if [[ "$NON_INTERACTIVE" == "true" ]]; then
    if [[ "$STRICT" == "true" ]] && (( WARNINGS > 0 )); then
        printf '%s\n' "${WARN_LINES[@]}"
        exit 1
    fi
    exit 0
fi

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
