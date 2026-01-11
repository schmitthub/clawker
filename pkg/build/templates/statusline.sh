#!/bin/bash

# Claude Code Status Line
# Design: Minimal monochrome matching Claude Code's UI aesthetic
# Mostly white/gray text on dark background with subtle orange accents

# JSON input strucure:
# {
#   "hook_event_name": "Status",
#   "session_id": "abc123...",
#   "transcript_path": "/path/to/transcript.json",
#   "cwd": "/current/working/directory",
#   "model": {
#     "id": "claude-opus-4-1",
#     "display_name": "Opus"
#   },
#   "workspace": {
#     "current_dir": "/current/working/directory",
#     "project_dir": "/original/project/directory"
#   },
#   "version": "1.0.80",
#   "output_style": {
#     "name": "default"
#   },
#   "cost": {
#     "total_cost_usd": 0.01234,
#     "total_duration_ms": 45000,
#     "total_api_duration_ms": 2300,
#     "total_lines_added": 156,
#     "total_lines_removed": 23
#   },
#   "context_window": {
#     "total_input_tokens": 15234,
#     "total_output_tokens": 4521,
#     "context_window_size": 200000,
#     "current_usage": {
#       "input_tokens": 8500,
#       "output_tokens": 1200,
#       "cache_creation_input_tokens": 5000,
#       "cache_read_input_tokens": 2000
#     }
#   }
# }

input=$(cat)

# Colors - minimal, matching Claude Code UI
# Orange accent: \033[38;5;214m (Claude orange for key items)
# Dim gray: \033[2m (for less important info)
# White: \033[0m (default terminal white)
# Reset: \033[0m
ORANGE='\033[38;5;214m'
GRAY='\033[2m'
DARK_GRAY='\033[90m'
WHITE='\033[1m'
RED='\033[31m' # Bright Red for alerts
GREEN='\x1b[38;5;42m'
YELLOW='\x1b[38;5;226m'
NC='\033[0m' # No Color

# ICONS
GIT=''  # Git branch icon (requires a powerline font)

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

MODEL=$(get_model_name)
USAGE=$(get_context_window_usage)
CONTEXT_SIZE=$(get_context_window_size)

# Context window calculation
ctx=''
if [ "$USAGE" != "null" ]; then
    # Calculate current context usage
    CURRENT_TOKENS=$(echo "$USAGE" | jq '.input_tokens + .cache_creation_input_tokens + .cache_read_input_tokens')
    PERCENT_USED=$((CURRENT_TOKENS * 100 / CONTEXT_SIZE))


    if [ "$PERCENT_USED" -lt 40 ]; then
        ctx=$(printf "${GREEN}%d%%${NC}" "$PERCENT_USED")
    elif [ "$PERCENT_USED" -lt 60 ]; then
        ctx=$(printf "${YELLOW}%d%%${NC}" "$PERCENT_USED")
    elif [ "$PERCENT_USED" -lt 70 ]; then
        ctx=$(printf "${RED}%d%%${NC}" "$PERCENT_USED")
    else
        ctx=$(printf "${ORANGE}%d%%${NC}" "$PERCENT_USED")
    fi
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


# Build status line prefix from env vars
PROJECT="${CLAUCKER_PROJECT:-claucker}"
AGENT="${CLAUCKER_AGENT:-}"
if [ -n "$AGENT" ]; then
    output="${ORANGE}${PROJECT}:${AGENT}${NC}"
else
    output="${ORANGE}${PROJECT}${NC}"
fi

# Directory - white, bold
output+=$(printf " ${DARK_GRAY}/${WHITE}%s${NC}" "$(basename "$DIR")")

# Git branch - dim gray
if [ -n "$branch" ]; then
    output+=$(printf " ${GRAY}%s${NC}" "${GIT} $branch")
fi

# Model - dim gray, subtle
output+=$(printf " ${ORANGE}%s${NC}" "$MODEL")
# Output style - only if not default, dim
if [ "$STYLE" != "null" ] && [ "$STYLE" != "default" ]; then
    output+=$(printf " ${GRAY}[%s]${NC}" "$STYLE")
fi

# Context window - orange accent when > 10%
if [ -n "$ctx" ]; then
    output+=$(printf " %s" "$ctx")
fi

# Vim mode - orange accent
if [ -n "$vim" ]; then
    output+=$(printf " ${ORANGE}%s${NC}" "$vim")
fi

echo "$output"
