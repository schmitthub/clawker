#!/bin/bash

# Claude Code Status Line

# JSON input strucure:
# {
#   "session_id": "9e12a865-cb3e-4484-913c-d8fa330d04ba",
#   "transcript_path": "/Users/andrew/.claude/projects/-Users-andrew-Code-clawker/9e12a865-cb3e-4484-913c-d8fa330d04ba.jsonl",
#   "cwd": "/Users/andrew/Code/clawker",
#   "model": {
#     "id": "claude-opus-4-5-20251101",
#     "display_name": "Opus 4.5"
#   },
#   "workspace": {
#     "current_dir": "/Users/andrew/Code/clawker",
#     "project_dir": "/Users/andrew/Code/clawker"
#   },
#   "version": "2.1.7",
#   "output_style": {
#     "name": "default"
#   },
#   "cost": {
#     "total_cost_usd": 8.342332000000003,
#     "total_duration_ms": 36549424,
#     "total_api_duration_ms": 1094625,
#     "total_lines_added": 0,
#     "total_lines_removed": 0
#   },
#   "context_window": {
#     "total_input_tokens": 195938,
#     "total_output_tokens": 43480,
#     "context_window_size": 200000,
#     "current_usage": {
#       "input_tokens": 10,
#       "output_tokens": 42,
#       "cache_creation_input_tokens": 76444,
#       "cache_read_input_tokens": 14391
#     },
#     "used_percentage": 45,
#     "remaining_percentage": 55
#   },
#   "exceeds_200k_tokens": false
# }


input=$(cat)

# write input to a temp file for debugging
# echo "$input" >> /tmp/claude_status_input.json

# Colors - minimal, matching Claude Code UI
# Orange accent: \033[38;5;214m (Claude orange for key items)
# Dim gray: \033[2m (for less important info)
# White: \033[0m (default terminal white)
# Reset: \033[0m
DARK_GRAY='\033[90m'
ORANGE='\033[38;5;214m'
GRAY='\033[2m'
WHITE='\033[1m'
RED='\033[31m' # Bright Red for alerts
GREEN='\x1b[38;5;42m'
YELLOW='\x1b[38;5;226m'
NC='\033[0m' # No Color

# ICONS
BULLET_POINT='•'
GIT=''  # Git branch icon (requires a powerline font)
DISK_LOW='⛀'  # Single disk slice icon
DISK_LOW_FULL='⛂'  # Single disk slice full icon
DISK_MEDIUM='⛁'  # Double disk slice icon
DISK_HIGH='⛃'  # Triple disk slice icon
DOT='⏺'

# Helper functions for common extractions
get_model_name() { echo "$input" | jq -r '.model.display_name'; }
get_current_dir() { echo "$input" | jq -r '.workspace.current_dir'; }
get_project_dir() { echo "$input" | jq -r '.workspace.project_dir'; }
get_version() { echo "$input" | jq -r '.version'; }
get_cost() { echo "$input" | jq -r '.cost.total_cost_usd'; }
get_duration() { echo "$input" | jq -r '.cost.total_duration_ms'; }
get_lines_added() { echo "$input" | jq -r '.cost.total_lines_added'; }
get_lines_removed() { echo "$input" | jq -r '.cost.total_lines_removed'; }
get_context_window_size() { echo "$input" | jq -r '.context_window.context_window_size'; }
get_context_window_usage() { echo "$input" | jq '.context_window.current_usage'; }
get_output_style() { echo "$input" | jq -r '.output_style.name'; }


# Context window calculation
USAGE=$(get_context_window_usage)
CONTEXT_SIZE=$(get_context_window_size)
ctx=''
if [ "$USAGE" != "null" ]; then
    # Calculate current context usage
    CURRENT_TOKENS=$(echo "$USAGE" | jq '.input_tokens + .cache_creation_input_tokens + .cache_read_input_tokens')
    PERCENT_USED=$((CURRENT_TOKENS * 100 / CONTEXT_SIZE))


    if [ "$PERCENT_USED" -lt 30 ]; then
        ctx=$(printf "${GREEN}${DISK_LOW} %d%%${NC}" "$PERCENT_USED")
    elif [ "$PERCENT_USED" -lt 40 ]; then
        ctx=$(printf "${GREEN}${DISK_LOW_FULL} %d%%${NC}" "$PERCENT_USED")
    elif [ "$PERCENT_USED" -lt 50 ]; then
        ctx=$(printf "${GREEN}${DISK_LOW_FULL} %d%%${NC}" "$PERCENT_USED")
    elif [ "$PERCENT_USED" -lt 60 ]; then
        ctx=$(printf "${YELLOW}${DISK_MEDIUM} %d%%${NC}" "$PERCENT_USED")
    elif [ "$PERCENT_USED" -lt 70 ]; then
        ctx=$(printf "${YELLOW}${DISK_HIGH} %d%%${NC}" "$PERCENT_USED")
    else
        ctx=$(printf "${RED}${DISK_HIGH} %d%%${NC}" "$PERCENT_USED")
    fi
fi

# Lines Added
LINES_ADDED=$(get_lines_added)
LINES_REMOVED=$(get_lines_removed)
lines=''
if [ "$LINES_ADDED" != "null" ] && [ "$LINES_REMOVED" != "null" ]; then
    lines=$(printf "${GREEN}+%d${NC} ${RED}-%d${NC}" "$LINES_ADDED" "$LINES_REMOVED")
fi


# Extract data from JSON input
DIR=$(get_current_dir)
MODEL=$(get_model_name)
STYLE=$(get_output_style)

# Git branch (with no-optional-locks to avoid lock conflicts)
cd "$DIR" 2>/dev/null
branch=$(git --no-optional-locks rev-parse --abbrev-ref HEAD 2>/dev/null)

# Vim mode (if enabled)
vim=$(echo "$input" | jq -r '.vim.mode // empty')


# Build status line - plain text with minimal color
output=""

# Output style - only if not default, dim
if [ "$STYLE" != "null" ] && [ "$STYLE" != "default" ]; then
    output+=$(printf " ${GRAY}[%s]${NC}" "$STYLE")
fi

# Clawker info
output=$(printf "${GRAY}Clawker v%s |${NC}" "${CLAWKER_VERSION:-0.1.0}")

# Version - dim gray
VERSION=$(get_version)
output+=$(printf " ${DARK_GRAY}cc: v%s${NC}" "$VERSION")

# Model - orange
output+=$(printf " ${ORANGE}%s${NC}" "$MODEL")

# Context window - orange accent when > 10%
if [ -n "$ctx" ]; then
    output+=$(printf " %s" "$ctx")
else
    output+=$(printf " ${GRAY}%s${NC}" "${DISK_LOW} ?%")
fi

# separator icon - dim gray
output+=$(printf " ${GRAY}%s${NC} " ">")

PROJECT="${CLAWKER_PROJECT:-clawker}"
AGENT="${CLAWKER_AGENT:-}"
if [ -n "$AGENT" ]; then
    output+=$(printf " ${ORANGE}%s:%s${NC}" "$PROJECT" "$AGENT")
else
    output+=$(printf " ${ORANGE}%s${NC}" "$PROJECT")
fi

# Directory - white, bold
output+=$(printf " %s/" "$(basename "$DIR")")

# Git branch - dim gray
if [ -n "$branch" ]; then
    output+=$(printf " ${GRAY}%s${NC}" "${GIT}$branch")
fi

# Lines added/removed
if [ -n "$lines" ]; then
    output+=$(printf " %s" "$lines")
fi

# Vim mode - orange accent
if [ -n "$vim" ]; then
    output+=$(printf " ${ORANGE}%s${NC}" "$vim")
fi

echo "$output"
