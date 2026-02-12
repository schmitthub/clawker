#!/bin/bash
# Docker resource leak monitor for clawker integration tests
# Usage: ./scripts/clawker-leak-monitor.sh [poll_interval_seconds]

INTERVAL=${1:-1}
VIOLATION_FILE="/tmp/clawker-leak-violation.txt"
rm -f "$VIOLATION_FILE"

check_resources() {
    local timestamp=$(date +%H:%M:%S)

    # Count containers (all states)
    local containers=$(docker ps -a --filter "name=clawker" --format '{{.ID}}' 2>/dev/null | wc -l | tr -d ' ')
    # Also check for copy containers
    local copy_containers=$(docker ps -a --filter "name=clawker-copy" --format '{{.ID}}' 2>/dev/null | wc -l | tr -d ' ')

    # Count volumes
    local volumes=$(docker volume ls --filter "name=clawker" --format '{{.Name}}' 2>/dev/null | wc -l | tr -d ' ')

    # Check for unlabeled clawker volumes (THE ORIGINAL BUG)
    local all_clawker_vols=$(docker volume ls --filter "name=clawker" --format '{{.Name}}' 2>/dev/null)
    local labeled_vols=$(docker volume ls --filter "name=clawker" --filter "label=dev.clawker.managed=true" --format '{{.Name}}' 2>/dev/null)
    local unlabeled_count=0
    if [ -n "$all_clawker_vols" ]; then
        for vol in $all_clawker_vols; do
            if ! echo "$labeled_vols" | grep -q "^${vol}$"; then
                unlabeled_count=$((unlabeled_count + 1))
                echo "[$timestamp] CRITICAL: Unlabeled volume: $vol" | tee -a "$VIOLATION_FILE"
                # Get volume details
                docker volume inspect "$vol" 2>/dev/null | head -20 >> "$VIOLATION_FILE"
            fi
        done
    fi

    # Check for containers missing dev.clawker.test=true
    # Uses label filter (atomic) then confirms with inspect to avoid TOCTOU race
    local all_clawker_ctrs=$(docker ps -a --filter "name=clawker" --format '{{.ID}}' 2>/dev/null)
    local test_labeled_ctrs=$(docker ps -a --filter "name=clawker" --filter "label=dev.clawker.test=true" --format '{{.ID}}' 2>/dev/null)
    local containers_no_test=0
    for id in $all_clawker_ctrs; do
        if [ -n "$id" ] && ! echo "$test_labeled_ctrs" | grep -q "^${id}$"; then
            if docker inspect "$id" >/dev/null 2>&1; then
                containers_no_test=$((containers_no_test + 1))
            fi
        fi
    done

    # Check for containers missing dev.clawker.test.name
    local testname_labeled_ctrs=$(docker ps -a --filter "name=clawker" --filter "label=dev.clawker.test.name" --format '{{.ID}}' 2>/dev/null)
    local containers_no_testname=0
    for id in $all_clawker_ctrs; do
        if [ -n "$id" ] && ! echo "$testname_labeled_ctrs" | grep -q "^${id}$"; then
            if docker inspect "$id" >/dev/null 2>&1; then
                containers_no_testname=$((containers_no_testname + 1))
            fi
        fi
    done

    # Check volumes for test labels
    # Uses docker volume ls with label filters (atomic) to avoid TOCTOU race
    # where a volume is listed by name but deleted before inspect runs.
    local vols_no_test=0
    local vols_no_testname=0
    if [ -n "$all_clawker_vols" ]; then
        local test_labeled_vols=$(docker volume ls --filter "name=clawker" --filter "label=dev.clawker.test=true" --format '{{.Name}}' 2>/dev/null)
        for vol in $all_clawker_vols; do
            if ! echo "$test_labeled_vols" | grep -q "^${vol}$"; then
                # Volume exists but missing test label — verify it still exists before reporting
                if docker volume inspect "$vol" >/dev/null 2>&1; then
                    local has_test=$(docker volume inspect "$vol" --format '{{index .Labels "dev.clawker.test"}}' 2>/dev/null)
                    if [ "$has_test" != "true" ]; then
                        vols_no_test=$((vols_no_test + 1))
                        echo "[$timestamp] CRITICAL: Volume missing test label: $vol" | tee -a "$VIOLATION_FILE"
                    fi
                fi
                # else: volume was deleted between ls and inspect — not a violation
            fi
        done
        # Check test.name via the same race-safe pattern
        local testname_labeled_vols=$(docker volume ls --filter "name=clawker" --filter "label=dev.clawker.test.name" --format '{{.Name}}' 2>/dev/null)
        for vol in $all_clawker_vols; do
            if ! echo "$testname_labeled_vols" | grep -q "^${vol}$"; then
                if docker volume inspect "$vol" >/dev/null 2>&1; then
                    local has_testname=$(docker volume inspect "$vol" --format '{{index .Labels "dev.clawker.test.name"}}' 2>/dev/null)
                    if [ -z "$has_testname" ]; then
                        vols_no_testname=$((vols_no_testname + 1))
                        echo "[$timestamp] CRITICAL: Volume missing test.name label: $vol" | tee -a "$VIOLATION_FILE"
                    fi
                fi
            fi
        done
    fi

    # Report any violation inline
    if [ "$containers_no_test" -gt 0 ]; then
        echo "[$timestamp] VIOLATION: $containers_no_test container(s) missing dev.clawker.test=true" | tee -a "$VIOLATION_FILE"
        # Log which containers
        for id in $all_clawker_ctrs; do
            if [ -n "$id" ] && ! echo "$test_labeled_ctrs" | grep -q "^${id}$"; then
                if docker inspect "$id" >/dev/null 2>&1; then
                    local cname=$(docker inspect "$id" --format '{{.Name}}' 2>/dev/null | sed 's|^/||')
                    echo "[$timestamp]   container: $cname ($id)" | tee -a "$VIOLATION_FILE"
                fi
            fi
        done
    fi
    if [ "$containers_no_testname" -gt 0 ]; then
        echo "[$timestamp] VIOLATION: $containers_no_testname container(s) missing dev.clawker.test.name" | tee -a "$VIOLATION_FILE"
        for id in $all_clawker_ctrs; do
            if [ -n "$id" ] && ! echo "$testname_labeled_ctrs" | grep -q "^${id}$"; then
                if docker inspect "$id" >/dev/null 2>&1; then
                    local cname=$(docker inspect "$id" --format '{{.Name}}' 2>/dev/null | sed 's|^/||')
                    echo "[$timestamp]   container: $cname ($id)" | tee -a "$VIOLATION_FILE"
                fi
            fi
        done
    fi
    if [ "$unlabeled_count" -gt 0 ]; then
        echo "[$timestamp] VIOLATION: $unlabeled_count volume(s) missing dev.clawker.managed=true" | tee -a "$VIOLATION_FILE"
    fi

    # Summary line
    echo "[$timestamp] containers=$containers copy=$copy_containers volumes=$volumes unlabeled_vols=$unlabeled_count no_test_label=$containers_no_test,$vols_no_test no_testname=$containers_no_testname,$vols_no_testname"
}

echo "=== Clawker Leak Monitor Started (interval: ${INTERVAL}s) ==="
echo "Violation file: $VIOLATION_FILE"

while true; do
    check_resources
    sleep "$INTERVAL"
done
