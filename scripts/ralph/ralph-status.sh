#!/usr/bin/env bash
# ralph-status.sh - Show progress of Ralph tasks
#
# Displays the current status of all release automation tasks,
# including completion status, iteration counts, and timestamps.
#
# Usage: ./scripts/ralph/ralph-status.sh

PROMPT_DIR="${PROMPT_DIR:-.ralph}"
PROGRESS_FILE="$PROMPT_DIR/progress.json"
LOG_DIR="$PROMPT_DIR/logs"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
GRAY='\033[0;90m'
NC='\033[0m' # No Color

# Task names
declare -A TASKS
TASKS[1]="Create GoReleaser Configuration"
TASKS[2]="Create GitHub Actions Release Workflow"
TASKS[3]="Test Release Flow with Test Tag"
TASKS[4]="Update Documentation"
TASKS[5]="Create First Official Release"

echo -e "${BLUE}+------------------------------------------------------------+${NC}"
echo -e "${BLUE}|              RALPH LOOP - TASK PROGRESS                    |${NC}"
echo -e "${BLUE}+------------------------------------------------------------+${NC}"
echo ""

if [[ ! -f "$PROGRESS_FILE" ]]; then
    echo -e "${YELLOW}No progress file found at: $PROGRESS_FILE${NC}"
    echo ""
    echo "Run ralph-loop.sh to start:"
    echo "  ./scripts/ralph/ralph-setup.sh ralph"
    echo "  ./scripts/ralph/ralph-loop.sh 1 ralph"
    exit 0
fi

if ! command -v jq &> /dev/null; then
    echo -e "${YELLOW}jq not installed. Showing raw progress file:${NC}"
    echo ""
    cat "$PROGRESS_FILE"
    echo ""
    echo -e "${GRAY}Install jq for better formatting: brew install jq${NC}"
    exit 0
fi

# Parse progress file
AGENT=$(jq -r '.agent // "unknown"' "$PROGRESS_FILE")
STARTED=$(jq -r '.started_at // "unknown"' "$PROGRESS_FILE")

echo -e "${CYAN}Agent:${NC} $AGENT"
echo -e "${CYAN}Started:${NC} $STARTED"
echo ""

# Count stats
COMPLETED=0
IN_PROGRESS=0
PENDING=0
FAILED=0
TOTAL_ITERATIONS=0

for i in 1 2 3 4 5; do
    STATUS=$(jq -r --arg t "$i" '.tasks[$t].status // "pending"' "$PROGRESS_FILE")
    ITERS=$(jq -r --arg t "$i" '.tasks[$t].iterations // 0' "$PROGRESS_FILE")
    TOTAL_ITERATIONS=$((TOTAL_ITERATIONS + ITERS))

    case "$STATUS" in
        completed) COMPLETED=$((COMPLETED + 1)) ;;
        in_progress) IN_PROGRESS=$((IN_PROGRESS + 1)) ;;
        failed) FAILED=$((FAILED + 1)) ;;
        *) PENDING=$((PENDING + 1)) ;;
    esac
done

echo -e "${BLUE}Summary:${NC}"
echo -e "  ${GREEN}Completed:${NC} $COMPLETED/5"
echo -e "  ${YELLOW}In Progress:${NC} $IN_PROGRESS"
echo -e "  ${RED}Failed:${NC} $FAILED"
echo -e "  ${GRAY}Pending:${NC} $PENDING"
echo -e "  ${CYAN}Total Iterations:${NC} $TOTAL_ITERATIONS"
echo ""
echo -e "${BLUE}Tasks:${NC}"

for i in 1 2 3 4 5; do
    STATUS=$(jq -r --arg t "$i" '.tasks[$t].status // "pending"' "$PROGRESS_FILE")
    ITERS=$(jq -r --arg t "$i" '.tasks[$t].iterations // 0' "$PROGRESS_FILE")
    COMPLETED_AT=$(jq -r --arg t "$i" '.tasks[$t].completed_at // ""' "$PROGRESS_FILE")

    case "$STATUS" in
        completed)
            ICON="${GREEN}[OK]${NC}"
            STATUS_TEXT="${GREEN}completed${NC}"
            ;;
        in_progress)
            ICON="${YELLOW}[..]${NC}"
            STATUS_TEXT="${YELLOW}in progress${NC}"
            ;;
        failed)
            ICON="${RED}[!!]${NC}"
            STATUS_TEXT="${RED}failed${NC}"
            ;;
        *)
            ICON="${GRAY}[  ]${NC}"
            STATUS_TEXT="${GRAY}pending${NC}"
            ;;
    esac

    # Format output
    printf "  %b  %d. %-40s %b" "$ICON" "$i" "${TASKS[$i]}" "$STATUS_TEXT"

    if [[ $ITERS -gt 0 ]]; then
        printf " (%d iter)" "$ITERS"
    fi

    if [[ -n "$COMPLETED_AT" && "$COMPLETED_AT" != "null" ]]; then
        # Format timestamp (remove T and Z, show just time)
        TIME_PART=$(echo "$COMPLETED_AT" | sed 's/.*T\([^Z]*\)Z/\1/' | cut -d: -f1-2)
        printf " @ %s" "$TIME_PART"
    fi

    printf "\n"
done

echo ""

# Show recent log files
if [[ -d "$LOG_DIR" ]]; then
    RECENT_LOGS=$(ls -t "$LOG_DIR"/*.log 2>/dev/null | head -3)
    if [[ -n "$RECENT_LOGS" ]]; then
        echo -e "${BLUE}Recent logs:${NC}"
        for LOG in $RECENT_LOGS; do
            BASENAME=$(basename "$LOG")
            SIZE=$(ls -lh "$LOG" | awk '{print $5}')
            echo -e "  ${GRAY}$BASENAME${NC} ($SIZE)"
        done
        echo ""
    fi
fi

# Show next action
echo -e "${BLUE}Next action:${NC}"
if [[ $FAILED -gt 0 ]]; then
    # Find first failed task
    for i in 1 2 3 4 5; do
        STATUS=$(jq -r --arg t "$i" '.tasks[$t].status // "pending"' "$PROGRESS_FILE")
        if [[ "$STATUS" == "failed" ]]; then
            echo "  Retry failed task: ./scripts/ralph/ralph-loop.sh $i $AGENT --force"
            break
        fi
    done
elif [[ $IN_PROGRESS -gt 0 ]]; then
    # Find in-progress task
    for i in 1 2 3 4 5; do
        STATUS=$(jq -r --arg t "$i" '.tasks[$t].status // "pending"' "$PROGRESS_FILE")
        if [[ "$STATUS" == "in_progress" ]]; then
            echo "  Continue task: ./scripts/ralph/ralph-loop.sh $i $AGENT"
            break
        fi
    done
elif [[ $PENDING -gt 0 ]]; then
    # Find first pending task
    for i in 1 2 3 4 5; do
        STATUS=$(jq -r --arg t "$i" '.tasks[$t].status // "pending"' "$PROGRESS_FILE")
        if [[ "$STATUS" == "pending" ]]; then
            echo "  Start next task: ./scripts/ralph/ralph-loop.sh $i $AGENT"
            break
        fi
    done
else
    echo -e "  ${GREEN}All tasks completed!${NC}"
    echo "  Review changes: git log --oneline -20"
fi
