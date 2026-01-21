#!/usr/bin/env bash
# ralph-all.sh - Run all release automation tasks in sequence
#
# This script runs all 5 release automation tasks one after another.
# Each task must complete before the next one starts.
#
# Usage: ./scripts/ralph/ralph-all.sh [agent_name] [max_iterations_per_task]

set -e

AGENT_NAME="${1:-ralph}"
MAX_ITERATIONS="${2:-10}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Task names for display
declare -A TASKS
TASKS[1]="Create GoReleaser Configuration"
TASKS[2]="Create GitHub Actions Release Workflow"
TASKS[3]="Test Release Flow with Test Tag"
TASKS[4]="Update Documentation"
TASKS[5]="Create First Official Release"

echo -e "${BLUE}+============================================================+${NC}"
echo -e "${BLUE}|     RALPH WIGGUM LOOP - FULL AUTOMATION SEQUENCE          |${NC}"
echo -e "${BLUE}+============================================================+${NC}"
echo ""
echo -e "${YELLOW}Agent:${NC} $AGENT_NAME"
echo -e "${YELLOW}Max iterations per task:${NC} $MAX_ITERATIONS"
echo ""
echo -e "${CYAN}Tasks to run:${NC}"
for i in 1 2 3 4 5; do
    echo "  $i. ${TASKS[$i]}"
done
echo ""

# Check if agent exists
if ! clawker container ls -a --format '{{.Names}}' 2>/dev/null | grep -q "clawker\..*\.$AGENT_NAME$"; then
    echo -e "${RED}Error: Agent '$AGENT_NAME' not found.${NC}"
    echo ""
    echo "Set up the agent first:"
    echo "  ./scripts/ralph/ralph-setup.sh $AGENT_NAME"
    exit 1
fi

# Confirm before starting
read -p "Start full automation sequence? [y/N] " -n 1 -r
echo ""
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Aborted."
    exit 0
fi

START_TIME=$(date +%s)
FAILED_TASK=""

for TASK in 1 2 3 4 5; do
    echo ""
    echo -e "${BLUE}+============================================================+${NC}"
    echo -e "${BLUE}|  TASK $TASK: ${TASKS[$TASK]}${NC}"
    echo -e "${BLUE}+============================================================+${NC}"
    echo ""

    if ! "$SCRIPT_DIR/ralph-loop.sh" "$TASK" "$AGENT_NAME" "$MAX_ITERATIONS"; then
        FAILED_TASK="$TASK"
        echo ""
        echo -e "${RED}Task $TASK failed. Stopping automation sequence.${NC}"
        break
    fi

    echo ""
    echo -e "${GREEN}Task $TASK completed successfully.${NC}"

    # Brief pause between tasks
    if [[ $TASK -lt 5 ]]; then
        echo -e "Waiting 3 seconds before next task..."
        sleep 3
    fi
done

END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))
MINUTES=$((DURATION / 60))
SECONDS=$((DURATION % 60))

echo ""
echo -e "${BLUE}+============================================================+${NC}"
if [[ -z "$FAILED_TASK" ]]; then
    echo -e "${GREEN}|              ALL TASKS COMPLETED SUCCESSFULLY             |${NC}"
    echo -e "${BLUE}+============================================================+${NC}"
    echo ""
    echo -e "${GREEN}Release automation pipeline is ready!${NC}"
    echo -e "Total time: ${MINUTES}m ${SECONDS}s"
    echo ""
    echo -e "${CYAN}Next steps:${NC}"
    echo "  1. Review the changes: git log --oneline -20"
    echo "  2. Check the workflow: cat .github/workflows/release.yml"
    echo "  3. Push and create a release tag: git push && git tag v0.1.0 && git push --tags"
else
    echo -e "${RED}|              AUTOMATION SEQUENCE FAILED                   |${NC}"
    echo -e "${BLUE}+============================================================+${NC}"
    echo ""
    echo -e "${RED}Failed at Task $FAILED_TASK: ${TASKS[$FAILED_TASK]}${NC}"
    echo -e "Total time before failure: ${MINUTES}m ${SECONDS}s"
    echo ""
    echo -e "${YELLOW}To resume:${NC}"
    echo "  ./scripts/ralph/ralph-loop.sh $FAILED_TASK $AGENT_NAME $MAX_ITERATIONS"
    echo ""
    echo -e "${YELLOW}Or continue from where you left off:${NC}"
    echo "  ./scripts/ralph/ralph-all.sh $AGENT_NAME $MAX_ITERATIONS"
    echo "  (completed tasks will be skipped)"
    exit 1
fi
