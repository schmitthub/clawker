#!/usr/bin/env bash
# ralph-loop.sh - Run clawker agents in Ralph Wiggum loop
#
# The Ralph Wiggum pattern runs the same prompt in a loop until task completion.
# Each iteration sees the modified codebase from previous attempts, creating
# a self-correcting feedback loop through git history and file changes.
#
# Usage: ./scripts/ralph/ralph-loop.sh <task_number> [agent_name] [max_iterations] [--force]

set -e

# Configuration
TASK_NUMBER="${1:?Usage: $0 <task_number> [agent_name] [max_iterations] [--force]}"
AGENT_NAME="${2:-ralph}"
MAX_ITERATIONS="${3:-10}"
FORCE_RUN="${4:-}"
PROMPT_DIR="${PROMPT_DIR:-.ralph}"
LOG_DIR="${LOG_DIR:-.ralph/logs}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Task definitions (from release_automation_implementation memory)
declare -A TASKS
TASKS[1]="Create GoReleaser Configuration"
TASKS[2]="Create GitHub Actions Release Workflow"
TASKS[3]="Test Release Flow with Test Tag"
TASKS[4]="Update Documentation"
TASKS[5]="Create First Official Release"

# Validate task number
if [[ ! ${TASKS[$TASK_NUMBER]+_} ]]; then
    echo -e "${RED}Error: Invalid task number. Valid tasks: 1-5${NC}"
    for i in "${!TASKS[@]}"; do
        echo "  $i: ${TASKS[$i]}"
    done | sort -n
    exit 1
fi

TASK_NAME="${TASKS[$TASK_NUMBER]}"

# Setup directories
mkdir -p "$PROMPT_DIR" "$LOG_DIR"

# Progress file for tracking
PROGRESS_FILE="$PROMPT_DIR/progress.json"

# Function to update progress
update_progress() {
    local status="$1"
    local iterations="$2"

    # Initialize if doesn't exist
    if [[ ! -f "$PROGRESS_FILE" ]]; then
        echo '{"tasks":{},"agent":"'"$AGENT_NAME"'","started_at":"'"$(date -u +%Y-%m-%dT%H:%M:%SZ)"'"}' > "$PROGRESS_FILE"
    fi

    # Update task status using jq (or python if jq not available)
    if command -v jq &> /dev/null; then
        local completed_at="null"
        if [[ "$status" == "completed" ]]; then
            completed_at="\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
        fi
        jq --arg task "$TASK_NUMBER" \
           --arg status "$status" \
           --argjson iter "$iterations" \
           --argjson time "$completed_at" \
           '.tasks[$task] = {status: $status, iterations: $iter, completed_at: $time}' \
           "$PROGRESS_FILE" > "$PROGRESS_FILE.tmp" && mv "$PROGRESS_FILE.tmp" "$PROGRESS_FILE"
    fi
}

# Function to check if task already completed
check_completed() {
    if [[ "$FORCE_RUN" == "--force" ]]; then
        return 0
    fi

    if [[ -f "$PROGRESS_FILE" ]] && command -v jq &> /dev/null; then
        local status
        status=$(jq -r --arg task "$TASK_NUMBER" '.tasks[$task].status // "pending"' "$PROGRESS_FILE")
        if [[ "$status" == "completed" ]]; then
            echo -e "${GREEN}Task $TASK_NUMBER already completed. Skipping.${NC}"
            echo -e "Use --force to re-run: $0 $TASK_NUMBER $AGENT_NAME $MAX_ITERATIONS --force"
            exit 0
        fi
    fi
}

# Check if task already completed
check_completed

# Generate prompt file
PROMPT_FILE="$PROMPT_DIR/task-${TASK_NUMBER}.md"
cat > "$PROMPT_FILE" << 'PROMPT_EOF'
I am implementing the clawker release automation pipeline. Read the Serena memory `release_automation_implementation` for full context and task breakdown.

My assigned task is: __TASK_PLACEHOLDER__

Follow the instructions in the memory exactly. When you complete this specific task successfully, output:
<promise>DONE</promise>

If you encounter blockers, document them clearly and do NOT output the DONE promise.

IMPORTANT:
- Read the memory first to understand the full context
- Follow acceptance criteria exactly
- Verify all criteria pass before outputting DONE
- If a previous attempt failed, review what went wrong and try a different approach
- Check git status and recent commits to understand what was already done
PROMPT_EOF

# Replace placeholder with actual task (portable sed)
if [[ "$OSTYPE" == "darwin"* ]]; then
    sed -i '' "s/__TASK_PLACEHOLDER__/${TASK_NUMBER} - ${TASK_NAME}/" "$PROMPT_FILE"
else
    sed -i "s/__TASK_PLACEHOLDER__/${TASK_NUMBER} - ${TASK_NAME}/" "$PROMPT_FILE"
fi

echo -e "${BLUE}+------------------------------------------------------------+${NC}"
echo -e "${BLUE}|          RALPH WIGGUM LOOP - CLAWKER EDITION              |${NC}"
echo -e "${BLUE}+------------------------------------------------------------+${NC}"
echo ""
echo -e "${YELLOW}Task:${NC} $TASK_NUMBER - $TASK_NAME"
echo -e "${YELLOW}Agent:${NC} $AGENT_NAME"
echo -e "${YELLOW}Max Iterations:${NC} $MAX_ITERATIONS"
echo -e "${YELLOW}Prompt File:${NC} $PROMPT_FILE"
echo ""

# Check if agent container exists by trying to inspect it
# clawker container inspect --agent uses the naming convention clawker.<project>.<agent>
CONTAINER_STATE=$(clawker container inspect --agent "$AGENT_NAME" --format '{{.State.Status}}' 2>/dev/null || echo "not_found")

if [[ "$CONTAINER_STATE" == "not_found" ]]; then
    echo -e "${YELLOW}Agent container not found.${NC}"
    echo -e "${RED}NOTE: You must set up the agent first!${NC}"
    echo ""
    echo "Run the setup script:"
    echo "  ./scripts/ralph/ralph-setup.sh $AGENT_NAME"
    echo ""
    echo "This creates a worker container and handles authentication."
    exit 1
fi

# Get container name from inspect (using Name field which includes the leading slash)
CONTAINER_NAME=$(clawker container inspect --agent "$AGENT_NAME" --format '{{.Name}}' 2>/dev/null | sed 's|^/||')
echo -e "${CYAN}Container:${NC} $CONTAINER_NAME"

# Start the container if not running
if [[ "$CONTAINER_STATE" != "running" ]]; then
    echo -e "${YELLOW}Agent is not running.${NC}"
    echo -e "${RED}The agent must be running with Claude active to accept tasks.${NC}"
    echo ""
    echo "Start the agent interactively first:"
    echo "  clawker start -a -i --agent $AGENT_NAME"
    echo "  # Authenticate if needed, then detach with Ctrl+P, Ctrl+Q"
    echo ""
    echo "Or run setup:"
    echo "  ./scripts/ralph/ralph-setup.sh $AGENT_NAME"
    exit 1
fi

# Mark task as in progress
update_progress "in_progress" 0

# Main loop
ITERATION=0
DONE=false

while [[ $ITERATION -lt $MAX_ITERATIONS ]] && [[ "$DONE" != "true" ]]; do
    ITERATION=$((ITERATION + 1))
    TIMESTAMP=$(date +"%Y-%m-%d_%H-%M-%S")
    LOG_FILE="$LOG_DIR/task-${TASK_NUMBER}_iter-${ITERATION}_${TIMESTAMP}.log"

    echo ""
    echo -e "${BLUE}------------------------------------------------------------${NC}"
    echo -e "${GREEN}Iteration $ITERATION of $MAX_ITERATIONS${NC}"
    echo -e "${BLUE}------------------------------------------------------------${NC}"
    echo ""

    # Update progress
    update_progress "in_progress" "$ITERATION"

    # Run Claude Code in the container with the prompt
    echo -e "${YELLOW}Running Claude Code...${NC}"
    echo -e "${CYAN}Log file: $LOG_FILE${NC}"
    echo ""

    # Execute claude with the prompt, capture output
    set +e
    OUTPUT=$(cat "$PROMPT_FILE" | clawker exec -i --agent "$AGENT_NAME" claude -p 2>&1 --dangerously-skip-permissions | tee "$LOG_FILE")
    EXIT_CODE=$?
    set -e

    # Check for completion marker
    if echo "$OUTPUT" | grep -q "<promise>DONE</promise>"; then
        DONE=true
        update_progress "completed" "$ITERATION"

        echo ""
        echo -e "${GREEN}+------------------------------------------------------------+${NC}"
        echo -e "${GREEN}|                    TASK COMPLETED!                         |${NC}"
        echo -e "${GREEN}+------------------------------------------------------------+${NC}"
        echo ""
        echo -e "${GREEN}Task $TASK_NUMBER completed in $ITERATION iteration(s)${NC}"
        echo -e "Log: $LOG_FILE"
    else
        echo ""
        echo -e "${YELLOW}Task not yet complete. Checking for errors...${NC}"

        # Check for common error patterns
        if echo "$OUTPUT" | grep -qi "error\|failed\|exception"; then
            echo -e "${RED}Errors detected in output. Review log: $LOG_FILE${NC}"
        fi

        # Brief pause between iterations
        if [[ $ITERATION -lt $MAX_ITERATIONS ]]; then
            echo -e "Waiting 5 seconds before next iteration..."
            sleep 5
        fi
    fi
done

if [[ "$DONE" != "true" ]]; then
    update_progress "failed" "$ITERATION"

    echo ""
    echo -e "${RED}+------------------------------------------------------------+${NC}"
    echo -e "${RED}|            MAX ITERATIONS REACHED - TASK INCOMPLETE        |${NC}"
    echo -e "${RED}+------------------------------------------------------------+${NC}"
    echo ""
    echo -e "${RED}Task did not complete after $MAX_ITERATIONS iterations${NC}"
    echo -e "Review logs in: $LOG_DIR"
    echo ""
    echo -e "${YELLOW}Troubleshooting:${NC}"
    echo "  1. Check the latest log file for errors"
    echo "  2. Attach to the agent: clawker attach --agent $AGENT_NAME"
    echo "  3. Re-run with more iterations: $0 $TASK_NUMBER $AGENT_NAME 20"
    exit 1
fi

echo ""
echo -e "${GREEN}Next steps:${NC}"
if [[ $TASK_NUMBER -lt 5 ]]; then
    NEXT_TASK=$((TASK_NUMBER + 1))
    echo "  Run next task: ./scripts/ralph/ralph-loop.sh $NEXT_TASK $AGENT_NAME"
else
    echo "  All tasks complete! Release automation is ready."
    echo "  Review the changes with: git log --oneline -10"
fi
