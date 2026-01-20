#!/usr/bin/env bash
# ralph-setup.sh - Setup a Ralph agent for autonomous work
#
# This script creates a new clawker agent with YOLO mode enabled
# and starts an interactive session for authentication.
#
# Subscription users must authenticate via browser before the agent
# can be used for autonomous/scripted work.
#
# Usage: ./scripts/ralph-setup.sh [agent_name]

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
if clawker container ls -a --format '{{.Names}}' 2>/dev/null | grep -q "clawker\..*\.$AGENT_NAME$"; then
    CONTAINER_NAME=$(clawker container ls -a --format '{{.Names}}' 2>/dev/null | grep "clawker\..*\.$AGENT_NAME$" | head -1)
    CONTAINER_STATE=$(clawker container inspect "$CONTAINER_NAME" --format '{{.State.Status}}' 2>/dev/null || echo "unknown")

    echo -e "${CYAN}Agent already exists: $CONTAINER_NAME${NC}"
    echo -e "${CYAN}State: $CONTAINER_STATE${NC}"
    echo ""

    if [[ "$CONTAINER_STATE" == "running" ]]; then
        echo -e "${GREEN}Agent is already running.${NC}"
        echo ""
        echo "To attach to it:"
        echo "  clawker attach --agent $AGENT_NAME"
        echo ""
        echo "To start the Ralph loop:"
        echo "  ./scripts/ralph-loop.sh 1 $AGENT_NAME"
        exit 0
    fi

    echo -e "${YELLOW}Would you like to:${NC}"
    echo "  1. Start and attach to existing agent"
    echo "  2. Remove and recreate agent"
    echo "  3. Exit"
    echo ""
    read -p "Choice [1/2/3]: " -n 1 -r
    echo ""

    case $REPLY in
        1)
            echo -e "${YELLOW}Starting agent...${NC}"
            clawker start -a -i --agent "$AGENT_NAME"
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
echo "  1. Create the agent container with YOLO mode enabled"
echo "  2. Start an interactive session for authentication"
echo ""
echo -e "${YELLOW}IMPORTANT for subscription users:${NC}"
echo "  - Complete browser authentication when prompted"
echo "  - Accept the terms of use"
echo "  - Then detach with Ctrl+P, Ctrl+Q (NOT Ctrl+C)"
echo ""
echo -e "${CYAN}After setup, run:${NC}"
echo "  ./scripts/ralph-loop.sh 1 $AGENT_NAME"
echo ""
read -p "Press Enter to continue (Ctrl+C to cancel)..."

echo ""
echo -e "${YELLOW}Creating and starting agent...${NC}"
echo -e "${CYAN}Detach with: Ctrl+P, Ctrl+Q${NC}"
echo ""

# Run the container interactively with YOLO mode
clawker run -it --agent "$AGENT_NAME" -- --dangerously-skip-permissions
