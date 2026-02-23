#!/bin/bash

# Claude Code Status Line (Clawker)
# JSON input schema: https://code.claude.com/docs/en/statusline

input=$(cat)

# --- Color support detection ---
# Levels: 0 = none, 1 = basic 16, 2 = 256, 3 = truecolor
COLOR_LEVEL=0

if [ -z "${NO_COLOR+x}" ] && [ "${TERM:-dumb}" != "dumb" ]; then
    COLOR_LEVEL=1
    case "${TERM}" in
        *-256color|*256color) COLOR_LEVEL=2 ;;
    esac
    case "${COLORTERM}" in
        truecolor|24bit) COLOR_LEVEL=3 ;;
    esac
fi

# --- Color definitions by tier ---
if [ "$COLOR_LEVEL" -ge 2 ]; then
    # 256-color (or truecolor — 256 escapes work everywhere truecolor does)
    DARK_GRAY='\033[90m'
    ORANGE='\033[38;5;214m'
    GRAY='\033[2m'
    WHITE='\033[1m'
    RED='\033[31m'
    GREEN='\033[38;5;42m'
    YELLOW='\033[38;5;226m'
    MUTED_RED='\033[38;5;88m'
    CYAN='\033[38;5;51m'
    NC='\033[0m'
elif [ "$COLOR_LEVEL" -ge 1 ]; then
    # Basic 16-color fallback
    DARK_GRAY='\033[90m'
    ORANGE='\033[33m'        # yellow as orange substitute
    GRAY='\033[2m'
    WHITE='\033[1m'
    RED='\033[31m'
    GREEN='\033[32m'
    YELLOW='\033[33m'
    MUTED_RED='\033[2;31m'   # dim red
    CYAN='\033[36m'
    NC='\033[0m'
else
    # No color
    DARK_GRAY=''
    ORANGE=''
    GRAY=''
    WHITE=''
    RED=''
    GREEN=''
    YELLOW=''
    MUTED_RED=''
    CYAN=''
    NC=''
fi

# Icons
GIT=$'\xee\x82\xa0'
FOLDER='📁'
DISK_LOW='⛀'
DISK_LOW_FULL='⛂'
DISK_MEDIUM='⛁'
DISK_HIGH='⛃'

# Usage bar caching
USAGE_CACHE="/tmp/claude-usage-cache.json"
USAGE_CACHE_MAX_AGE=300

# Helper functions
get_model_name() { echo "$input" | jq -r '.model.display_name'; }
get_current_dir() { echo "$input" | jq -r '.workspace.current_dir'; }
get_version() { echo "$input" | jq -r '.version'; }
get_cost() { echo "$input" | jq -r '.cost.total_cost_usd'; }
get_lines_added() { echo "$input" | jq -r '.cost.total_lines_added'; }
get_lines_removed() { echo "$input" | jq -r '.cost.total_lines_removed'; }
get_context_window_size() { echo "$input" | jq -r '.context_window.context_window_size'; }
get_context_window_usage() { echo "$input" | jq '.context_window.current_usage'; }
get_output_style() { echo "$input" | jq -r '.output_style.name'; }
get_total_input_tokens() { echo "$input" | jq -r '.context_window.total_input_tokens'; }
get_total_output_tokens() { echo "$input" | jq -r '.context_window.total_output_tokens'; }
get_used_percentage() { echo "$input" | jq -r '.context_window.used_percentage // 0' | cut -d. -f1; }
get_duration() { echo "$input" | jq -r '.cost.total_duration_ms // 0'; }

# Render a usage bar: render_bar <utilization_pct> <label>
render_bar() {
    local util="$1" label="$2" bar_width=10
    local pct=$(echo "$util" | awk '{printf "%d", $1}')
    local filled=$(echo "$util" | awk -v w="$bar_width" '{printf "%d", $1 / 100 * w + 0.5}')
    [ "$filled" -gt "$bar_width" ] && filled=$bar_width
    local empty=$((bar_width - filled))

    local color
    if [ "$pct" -lt 50 ]; then color="$GREEN"
    elif [ "$pct" -lt 80 ]; then color="$YELLOW"
    else color="$RED"; fi

    local bar=""
    for ((i = 0; i < filled; i++)); do bar+="█"; done
    for ((i = 0; i < empty; i++)); do bar+="░"; done
    printf "${GRAY}%s${NC} ${color}%s${NC} %d%%" "$label" "$bar" "$pct"
}

# Format time remaining from ISO 8601 resets_at timestamp
format_remaining() {
    local resets_at="$1" fallback="$2"
    [ -z "$resets_at" ] && echo "$fallback" && return

    local trimmed=$(echo "$resets_at" | sed 's/\.[0-9]*//; s/Z$//; s/[+-][0-9][0-9]:[0-9][0-9]$//')
    local reset_epoch
    if [ "$(uname)" = "Darwin" ]; then
        reset_epoch=$(date -u -j -f '%Y-%m-%dT%H:%M:%S' "$trimmed" '+%s' 2>/dev/null)
    else
        reset_epoch=$(date -d "$resets_at" '+%s' 2>/dev/null)
    fi
    [ -z "$reset_epoch" ] && echo "$fallback" && return

    local now=$(date '+%s')
    local diff=$(( reset_epoch - now ))
    [ "$diff" -le 0 ] && echo "$fallback" && return

    local days=$(( diff / 86400 )) hours=$(( (diff % 86400) / 3600 )) mins=$(( (diff % 3600) / 60 ))
    if [ "$days" -gt 0 ]; then echo "${days}d${hours}h"
    elif [ "$hours" -gt 0 ]; then echo "${hours}h${mins}m"
    elif [ "$mins" -gt 0 ]; then echo "${mins}m"
    else echo "<1m"; fi
}

# === Compute values ===

# Format token count: 1234567 → "1.2M", 45000 → "45k", 800 → "800"
fmt_tokens() {
    local n="$1"
    if [ "$n" -ge 1000000 ]; then echo "$n" | awk '{printf "%.1fM", $1/1000000}'
    elif [ "$n" -ge 1000 ]; then echo "$n" | awk '{printf "%.0fk", $1/1000}'
    else echo "$n"; fi
}

# Context: icon + percentage, color-coded by usage
PCT=$(get_used_percentage)
ctx=""
if [ "$PCT" -lt 30 ]; then
    ctx=$(printf "${GREEN}${DISK_LOW} %d%% ctx${NC}" "$PCT")
elif [ "$PCT" -lt 50 ]; then
    ctx=$(printf "${GREEN}${DISK_LOW_FULL} %d%% ctx${NC}" "$PCT")
elif [ "$PCT" -lt 70 ]; then
    ctx=$(printf "${YELLOW}${DISK_MEDIUM} %d%% ctx${NC}" "$PCT")
else
    ctx=$(printf "${RED}${DISK_HIGH} %d%% ctx${NC}" "$PCT")
fi

# Session tokens
TOTAL_IN=$(get_total_input_tokens)
TOTAL_OUT=$(get_total_output_tokens)
tokens=""
if [ "$TOTAL_IN" != "null" ] && [ "$TOTAL_OUT" != "null" ]; then
    tokens=$(printf "${DARK_GRAY}↑%s ↓%s${NC}" "$(fmt_tokens "$TOTAL_IN")" "$(fmt_tokens "$TOTAL_OUT")")
fi

# Cache hit ratio
USAGE=$(get_context_window_usage)
cache_stat=""
if [ "$USAGE" != "null" ]; then
    CACHE_READ=$(echo "$USAGE" | jq '.cache_read_input_tokens // 0')
    CACHE_CREATE=$(echo "$USAGE" | jq '.cache_creation_input_tokens // 0')
    CACHE_TOTAL=$((CACHE_READ + CACHE_CREATE))
    if [ "$CACHE_TOTAL" -gt 0 ]; then
        CACHE_PCT=$((CACHE_READ * 100 / CACHE_TOTAL))
        if [ "$CACHE_PCT" -ge 70 ]; then cache_color="$GREEN"
        elif [ "$CACHE_PCT" -ge 40 ]; then cache_color="$YELLOW"
        else cache_color="$RED"; fi
        cache_stat=$(printf "${cache_color}⚡%d%%${NC}" "$CACHE_PCT")
    fi
fi

# Session cost
COST=$(get_cost)
cost_display=""
if [ "$COST" != "null" ] && [ "$COST" != "0" ]; then
    cost_display=$(printf "${ORANGE}%s${NC}" "$(echo "$COST" | awk '{printf "$%.2f", $1}')")
fi

# Duration
DURATION_MS=$(get_duration)
duration_display=""
if [ "$DURATION_MS" != "null" ] && [ "$DURATION_MS" != "0" ]; then
    DURATION_SEC=$((DURATION_MS / 1000))
    MINS=$((DURATION_SEC / 60))
    SECS=$((DURATION_SEC % 60))
    if [ "$MINS" -gt 0 ]; then
        duration_display=$(printf "${DARK_GRAY}%dm%ds${NC}" "$MINS" "$SECS")
    else
        duration_display=$(printf "${DARK_GRAY}%ds${NC}" "$SECS")
    fi
fi

# Lines added/removed
LINES_ADDED=$(get_lines_added)
LINES_REMOVED=$(get_lines_removed)
lines=""
if [ "$LINES_ADDED" != "null" ] && [ "$LINES_REMOVED" != "null" ]; then
    lines=$(printf "${GREEN}+%d${NC} ${RED}-%d${NC}" "$LINES_ADDED" "$LINES_REMOVED")
fi

# Extract data
DIR=$(get_current_dir)
MODEL=$(get_model_name)
STYLE=$(get_output_style)
VERSION=$(get_version)

# Git branch
cd "$DIR" 2>/dev/null
branch=$(git --no-optional-locks rev-parse --abbrev-ref HEAD 2>/dev/null)

# Worktree detection: .git file = worktree, .git dir = regular repo
is_worktree=false
if [ -f "${DIR}/.git" ]; then
    is_worktree=true
fi

# Vim mode
vim=$(echo "$input" | jq -r '.vim.mode // empty')

# === Line 1: clawker identity + project / code info ===
line1=""

# Clawker identity
line1+=$(printf "${MUTED_RED}Clawker %s${NC} ${DARK_GRAY}|${NC}" "${CLAWKER_VERSION:-dev}")

# Output style (only if non-default)
if [ "$STYLE" != "null" ] && [ "$STYLE" != "default" ]; then
    line1+=$(printf " ${GRAY}[%s]${NC}" "$STYLE")
fi

# Project and agent — visually distinct
PROJECT="${CLAWKER_PROJECT:-}"
AGENT="${CLAWKER_AGENT:-}"
MODE="${CLAWKER_WORKSPACE_MODE:-}"
if [ -n "$PROJECT" ]; then
    line1+=$(printf " ${ORANGE}%s${NC}" "$PROJECT")
fi
if [ -n "$AGENT" ]; then
    line1+=$(printf " ${WHITE}@%s${NC}" "$AGENT")
fi

# Git branch (+ cyan worktree indicator)
if [ -n "$branch" ]; then
    if $is_worktree; then
        line1+=$(printf " ${CYAN}%s+%s(wt)${NC}" "${GIT}" "$branch")
    else
        line1+=$(printf " ${GRAY}%s${NC}" "${GIT}$branch")
    fi
fi

# Pipe before host mount
line1+=$(printf " ${DARK_GRAY}|${NC}")

# Host mount: 📁 Code/clawker/ [bind]
HOST_SRC="${CLAWKER_WORKSPACE_SOURCE:-}"
mode_tag=""
if [ "$MODE" = "snapshot" ]; then
    mode_tag="[snap]"
elif [ "$MODE" = "bind" ]; then
    mode_tag="[bind]"
fi
if [ -n "$HOST_SRC" ]; then
    short_src=$(echo "$HOST_SRC" | awk -F'/' '{if(NF>2) print $(NF-1)"/"$NF; else print $0}')
    line1+=$(printf " ${FOLDER} %s/ ${DARK_GRAY}%s${NC}" "$short_src" "$mode_tag")
elif [ -n "$mode_tag" ]; then
    line1+=$(printf " %s" "$mode_tag")
fi

# Vim mode
if [ -n "$vim" ]; then
    line1+=$(printf " ${ORANGE}%s${NC}" "$vim")
fi

# === Line 2: model + context bar | session stats ===
line2=""
SEP=$(printf " ${DARK_GRAY}|${NC} ")

# Section 1: Model + context icon
line2+=$(printf "${ORANGE}[%s]${NC} %s" "$MODEL" "$ctx")

# Section 3: Session stats (all secondary/dim)
stats=""
[ -n "$tokens" ] && stats+=" ${tokens}"
[ -n "$cache_stat" ] && stats+=" ${cache_stat}"
[ -n "$cost_display" ] && stats+=" ${cost_display}"
[ -n "$duration_display" ] && stats+=" ${duration_display}"
if [ -n "$stats" ]; then
    line2+="${SEP}${stats# }"  # trim leading space
fi

# Lines added/removed (end of line 2)
if [ -n "$lines" ]; then
    line2+=$(printf "${SEP}${DARK_GRAY}loc:${NC} %s" "$lines")
fi

echo "$line1"
echo "$line2"
