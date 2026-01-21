#!/usr/bin/env bash
# ralph-setup.sh - Setup a Ralph agent for autonomous work
#
# This script creates a container, starts Claude interactively for auth,
# then the user detaches (Ctrl+P, Ctrl+Q). The container stays running
# and tasks can be sent via: echo "task" | clawker exec -i --agent NAME -- claude --dangerously-skip-permissions -p
#
# Usage: ./scripts/ralph/ralph-setup.sh [agent_name]

set -e

AGENT_NAME="${1:-ralph}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

echo -e "${BLUE}+------------------------------------------------------------+${NC}"
echo -e "${BLUE}|              RALPH AGENT SETUP                             |${NC}"
echo -e "${BLUE}+------------------------------------------------------------+${NC}"
echo ""
echo -e "${YELLOW}Agent name:${NC} $AGENT_NAME"
echo ""

# Check if agent already exists
CONTAINER_STATE=$(clawker container inspect --agent "$AGENT_NAME" --format '{{.State.Status}}' 2>/dev/null || echo "not_found")

if [[ "$CONTAINER_STATE" != "not_found" ]]; then
    CONTAINER_NAME=$(clawker container inspect --agent "$AGENT_NAME" --format '{{.Name}}' 2>/dev/null | sed 's|^/||')

    echo -e "${CYAN}Agent already exists: $CONTAINER_NAME${NC}"
    echo -e "${CYAN}State: $CONTAINER_STATE${NC}"
    echo ""

    if [[ "$CONTAINER_STATE" == "running" ]]; then
        echo -e "${GREEN}Agent is already running and ready for tasks.${NC}"
        echo ""
        echo "To start the Ralph loop:"
        echo "  ./scripts/ralph/ralph-loop.sh 1 $AGENT_NAME"
        echo ""
        echo "To attach interactively:"
        echo "  clawker attach --agent $AGENT_NAME"
        exit 0
    fi

    echo -e "${YELLOW}Would you like to:${NC}"
    echo "  1. Start existing agent"
    echo "  2. Remove and recreate agent"
    echo "  3. Exit"
    echo ""
    read -p "Choice [1/2/3]: " -n 1 -r
    echo ""

    case $REPLY in
        1)
            echo -e "${YELLOW}Starting agent interactively...${NC}"
            echo -e "${CYAN}Detach with Ctrl+P, Ctrl+Q when ready.${NC}"
            echo ""
            clawker start -a -i --agent "$AGENT_NAME"
            echo ""
            echo -e "${GREEN}Agent started.${NC}"
            echo ""
            echo "To start the Ralph loop:"
            echo "  ./scripts/ralph/ralph-loop.sh 1 $AGENT_NAME"
            exit 0
            ;;
        2)
            echo -e "${YELLOW}Removing existing agent...${NC}"
            clawker container rm -f --agent "$AGENT_NAME" 2>/dev/null || true
            ;;
        *)
            echo "Exiting."
            exit 0
            ;;
    esac
fi

echo "This will:"
echo "  1. Start Claude interactively for authentication"
echo "  2. You detach with Ctrl+P, Ctrl+Q (keeps container running)"
echo "  3. Tasks are sent via: echo 'task' | clawker exec -i --agent $AGENT_NAME -- claude --dangerously-skip-permissions -p"
echo ""
echo -e "${YELLOW}IMPORTANT for subscription users:${NC}"
echo "  - Complete browser authentication when prompted"
echo "  - Accept the terms of use"
echo "  - Detach with Ctrl+P, Ctrl+Q (NOT Ctrl+C) to keep container running"
echo ""
echo -e "${CYAN}After setup, run:${NC}"
echo "  ./scripts/ralph/ralph-loop.sh 1 $AGENT_NAME"
echo ""
read -p "Press Enter to continue (Ctrl+C to cancel)..."

echo ""
echo -e "${YELLOW}Starting Claude interactively...${NC}"
echo -e "${CYAN}Detach with Ctrl+P, Ctrl+Q when authentication is complete.${NC}"
echo ""

# Start container with claude running interactively
# User authenticates, then detaches - container stays running
# Tasks can then be exec'd as separate claude -p processes
clawker run -it --agent "$AGENT_NAME" -- --dangerously-skip-permissions

# This message shows after user detaches or exits
echo ""
echo -e "${GREEN}+------------------------------------------------------------+${NC}"
echo -e "${GREEN}|              SETUP COMPLETE                                |${NC}"
echo -e "${GREEN}+------------------------------------------------------------+${NC}"
echo ""

# Check if container is still running (user detached) or stopped (user exited)
CONTAINER_STATE=$(clawker container inspect --agent "$AGENT_NAME" --format '{{.State.Status}}' 2>/dev/null || echo "not_found")

if [[ "$CONTAINER_STATE" == "running" ]]; then
    echo -e "${GREEN}Agent '$AGENT_NAME' is running and ready for tasks.${NC}"
    echo ""
    echo "Start the Ralph loop:"
    echo "  ./scripts/ralph/ralph-loop.sh 1 $AGENT_NAME"
else
    echo -e "${YELLOW}Container stopped. To use for autonomous work:${NC}"
    echo "  clawker start -a -i --agent $AGENT_NAME"
    echo "  # Then detach with Ctrl+P, Ctrl+Q"
    echo ""
    echo "Then start the Ralph loop:"
    echo "  ./scripts/ralph/ralph-loop.sh 1 $AGENT_NAME"
fi
