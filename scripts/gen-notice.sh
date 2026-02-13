#!/usr/bin/env bash
#
# gen-notice.sh — Generate NOTICE file from go-licenses output.
# Installs go-licenses if not present, groups modules by license type.
#
# Usage: bash scripts/gen-notice.sh [output-file]
#   output-file  Path to write (default: NOTICE)
#
set -euo pipefail

NOTICE_FILE="${1:-NOTICE}"
MODULE_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$MODULE_ROOT"

# ── Install go-licenses if needed ────────────────────────────────────────────
if ! command -v go-licenses &>/dev/null; then
    echo "Installing go-licenses..." >&2
    go install github.com/google/go-licenses@latest
fi

# ── Collect license data ─────────────────────────────────────────────────────
echo "Running go-licenses report..." >&2

# Extract own module path to exclude from output.
OWN_MODULE=$(awk '/^module / { print $2; exit }' go.mod)

# go-licenses report outputs CSV: module,license_url,license_type
# Stderr may contain warnings about unrecognised licenses; capture stdout only.
# Filter out our own module.
RAW=$(go-licenses report ./... 2>/dev/null | grep -v "^${OWN_MODULE}," || true)

if [[ -z "$RAW" ]]; then
    echo "ERROR: go-licenses produced no output" >&2
    exit 1
fi

# ── Determine column width ───────────────────────────────────────────────────
# Find the longest module name so the license column aligns neatly.
MAX_LEN=$(echo "$RAW" | awk -F',' '
    $1 != "" && $3 != "" { if (length($1) > max) max = length($1) }
    END { print max }
')
# Pad to next multiple of 2, minimum 42
COL_WIDTH=$(( MAX_LEN + 2 ))
if (( COL_WIDTH < 42 )); then
    COL_WIDTH=42
fi

# ── Write NOTICE ─────────────────────────────────────────────────────────────
{
    cat <<'HEADER'
Clawker
Copyright (c) 2026 Andrew J Schmitt

This product includes software developed by third parties.
Below is a list of third-party dependencies and their licenses.

Generated from go.mod using go-licenses.
HEADER

    # Parse CSV → group by license → sort modules within each group.
    # Output is sorted by license name then module name for deterministic output.
    echo "$RAW" \
        | awk -F',' '$1 != "" && $3 != "" { print $3 "\t" $1 }' \
        | LC_ALL=C sort -t$'\t' -k1,1 -k2,2 \
        | awk -F'\t' -v col="$COL_WIDTH" '
            BEGIN { prev = "" }
            {
                license = $1
                module  = $2
                if (license != prev) {
                    printf "\n================================================================================\n"
                    printf "%s\n", license
                    printf "================================================================================\n\n"
                    prev = license
                }
                printf "%-" col "s %s\n", module, license
            }'
} > "$NOTICE_FILE"

echo "Generated $NOTICE_FILE ($(wc -l < "$NOTICE_FILE") lines)" >&2
