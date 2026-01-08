#!/usr/bin/env bash
set -Eeuo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

# Test counters
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

# Helper to run a test
run_test() {
  local name="$1"
  local expected="$2"
  local actual="$3"

  TESTS_RUN=$((TESTS_RUN + 1))

  if [[ "$expected" == "$actual" ]]; then
    echo -e "${GREEN}✓${NC} $name"
    echo -e "  Expected: ${YELLOW}$expected${NC}"
    echo -e "  Actual:   ${YELLOW}$actual${NC}"
    TESTS_PASSED=$((TESTS_PASSED + 1))
    return 0
  else
    echo -e "${RED}✗${NC} $name"
    echo -e "  Expected: ${YELLOW}$expected${NC}"
    echo -e "  Actual:   ${YELLOW}$actual${NC}"
    TESTS_FAILED=$((TESTS_FAILED + 1))
    return 1
  fi
}

# ============================================================================
# PARSE SEMVER TESTS
# ============================================================================

test_parse_semver() {
  echo ""
  echo "=== Testing parse_semver ==="

  # Test full semver
  run_test "parse full semver 1.2.3" \
    '{"major":1,"minor":2,"patch":3,"pre":null,"build":null}' \
    "$(echo '"1.2.3"' | jq -c 'include "semver"; parse_semver')"

  # Test major only
  run_test "parse major only (2)" \
    '{"major":2,"minor":null,"patch":null,"pre":null,"build":null}' \
    "$(echo '"2"' | jq -c 'include "semver"; parse_semver')"

  # Test major.minor
  run_test "parse major.minor (2.1)" \
    '{"major":2,"minor":1,"patch":null,"pre":null,"build":null}' \
    "$(echo '"2.1"' | jq -c 'include "semver"; parse_semver')"

  # Test with prerelease
  run_test "parse with prerelease (1.0.0-alpha)" \
    '{"major":1,"minor":0,"patch":0,"pre":"alpha","build":null}' \
    "$(echo '"1.0.0-alpha"' | jq -c 'include "semver"; parse_semver')"

  # Test with build metadata
  run_test "parse with build (1.0.0+build.123)" \
    '{"major":1,"minor":0,"patch":0,"pre":null,"build":"build.123"}' \
    "$(echo '"1.0.0+build.123"' | jq -c 'include "semver"; parse_semver')"

  # Test with prerelease and build
  run_test "parse with prerelease and build (1.0.0-beta+build)" \
    '{"major":1,"minor":0,"patch":0,"pre":"beta","build":"build"}' \
    "$(echo '"1.0.0-beta+build"' | jq -c 'include "semver"; parse_semver')"

  # Test zero version
  run_test "parse zero version (0.0.0)" \
    '{"major":0,"minor":0,"patch":0,"pre":null,"build":null}' \
    "$(echo '"0.0.0"' | jq -c 'include "semver"; parse_semver')"

  # Test large numbers
  run_test "parse large numbers (100.200.300)" \
    '{"major":100,"minor":200,"patch":300,"pre":null,"build":null}' \
    "$(echo '"100.200.300"' | jq -c 'include "semver"; parse_semver')"
}

# ============================================================================
# IS VALID SEMVER TESTS
# ============================================================================

test_is_valid_semver() {
  echo ""
  echo "=== Testing is_valid_semver ==="

  run_test "valid: 1.2.3" "true" \
    "$(echo '"1.2.3"' | jq 'include "semver"; is_valid_semver')"

  run_test "valid: 1" "true" \
    "$(echo '"1"' | jq 'include "semver"; is_valid_semver')"

  run_test "valid: 1.0" "true" \
    "$(echo '"1.0"' | jq 'include "semver"; is_valid_semver')"

  run_test "valid: 1.0.0-alpha" "true" \
    "$(echo '"1.0.0-alpha"' | jq 'include "semver"; is_valid_semver')"

  run_test "invalid: v1.0.0 (has v prefix)" "false" \
    "$(echo '"v1.0.0"' | jq 'include "semver"; is_valid_semver')"

  run_test "invalid: 1.0.0.0 (4 parts)" "false" \
    "$(echo '"1.0.0.0"' | jq 'include "semver"; is_valid_semver')"

  run_test "invalid: abc" "false" \
    "$(echo '"abc"' | jq 'include "semver"; is_valid_semver')"

  run_test "invalid: 01.0.0 (leading zero)" "false" \
    "$(echo '"01.0.0"' | jq 'include "semver"; is_valid_semver')"
}

# ============================================================================
# SORT SEMVER TESTS
# ============================================================================

test_sort_semver() {
  echo ""
  echo "=== Testing sort_semver ==="

  # Basic sorting
  run_test "sort basic versions" \
    '["1.0.0","1.0.1","1.1.0","2.0.0"]' \
    "$(echo '["2.0.0","1.0.1","1.1.0","1.0.0"]' | jq -c 'include "semver"; sort_semver')"

  # Sorting with different patch levels
  run_test "sort patch versions" \
    '["1.0.1","1.0.2","1.0.10","1.0.11"]' \
    "$(echo '["1.0.10","1.0.2","1.0.1","1.0.11"]' | jq -c 'include "semver"; sort_semver')"

  # Sorting with prereleases (prereleases come before releases)
  run_test "sort with prereleases" \
    '["1.0.0-alpha","1.0.0-beta","1.0.0"]' \
    "$(echo '["1.0.0","1.0.0-alpha","1.0.0-beta"]' | jq -c 'include "semver"; sort_semver')"

  # Filter out invalid versions
  run_test "filter invalid versions" \
    '["1.0.0","2.0.0"]' \
    "$(echo '["1.0.0","invalid","2.0.0","v1.0"]' | jq -c 'include "semver"; sort_semver')"
}

# ============================================================================
# SORT SEMVER DESC TESTS
# ============================================================================

test_sort_semver_desc() {
  echo ""
  echo "=== Testing sort_semver_desc ==="

  run_test "sort descending" \
    '["2.0.0","1.1.0","1.0.1","1.0.0"]' \
    "$(echo '["1.0.0","2.0.0","1.0.1","1.1.0"]' | jq -c 'include "semver"; sort_semver_desc')"

  run_test "sort descending with patches" \
    '["1.0.11","1.0.10","1.0.2","1.0.1"]' \
    "$(echo '["1.0.10","1.0.2","1.0.1","1.0.11"]' | jq -c 'include "semver"; sort_semver_desc')"
}

# ============================================================================
# MATCH SEMVER TESTS
# ============================================================================

test_match_semver() {
  echo ""
  echo "=== Testing match_semver ==="

  local versions='["1.0.0", "1.0.1","1.1.0","1.1.1","2.0.0","2.0.1","2.1.0","2.1.1"]'

  # Exact match
  run_test "exact match 1.0.0" "1.0.0" \
    "$(echo "$versions" | jq -r 'include "semver"; match_semver("1.0.0")')"

  # Major version match (should return highest in that major)
  run_test "match major 1 -> highest 1.x.x" "1.1.1" \
    "$(echo "$versions" | jq -r 'include "semver"; match_semver("1")')"

  # Major version match
  run_test "match major 2 -> highest 2.x.x" "2.1.1" \
    "$(echo "$versions" | jq -r 'include "semver"; match_semver("2")')"

  # Minor version match
  run_test "match minor 1.0 -> highest 1.0.x" "1.0.1" \
    "$(echo "$versions" | jq -r 'include "semver"; match_semver("1.0")')"

  # Minor version match
  run_test "match minor 2.0 -> highest 2.0.x" "2.0.1" \
    "$(echo "$versions" | jq -r 'include "semver"; match_semver("2.0")')"

  # No match returns empty
  run_test "no match returns empty" "" \
    "$(echo "$versions" | jq -r 'include "semver"; match_semver("3")')"

  # Match with prereleases in list (should skip prereleases unless exact)
  local versions_with_pre='["1.0.0","1.0.1-rc1","1.0.1","1.0.2-beta"]'
  run_test "match skips prereleases" "1.0.1" \
    "$(echo "$versions_with_pre" | jq -r 'include "semver"; match_semver("1.0")')"

  # Exact prerelease match
  run_test "exact prerelease match" "1.0.1-rc1" \
    "$(echo "$versions_with_pre" | jq -r 'include "semver"; match_semver("1.0.1-rc1")')"
}

# ============================================================================
# COMPARE SEMVER TESTS
# ============================================================================

test_compare_semver() {
  echo ""
  echo "=== Testing compare_semver ==="

  run_test "1.0.0 < 2.0.0" "-1" \
    "$(jq -n 'include "semver"; compare_semver("1.0.0"; "2.0.0")')"

  run_test "2.0.0 > 1.0.0" "1" \
    "$(jq -n 'include "semver"; compare_semver("2.0.0"; "1.0.0")')"

  run_test "1.0.0 == 1.0.0" "0" \
    "$(jq -n 'include "semver"; compare_semver("1.0.0"; "1.0.0")')"

  run_test "1.0.0 < 1.1.0" "-1" \
    "$(jq -n 'include "semver"; compare_semver("1.0.0"; "1.1.0")')"

  run_test "1.0.0 < 1.0.1" "-1" \
    "$(jq -n 'include "semver"; compare_semver("1.0.0"; "1.0.1")')"

  run_test "1 < 2" "-1" \
    "$(jq -n 'include "semver"; compare_semver("1"; "2")')"

  run_test "1.1 > 1.0" "1" \
    "$(jq -n 'include "semver"; compare_semver("1.1"; "1.0")')"
}

# ============================================================================
# RUN ALL TESTS
# ============================================================================

main() {
  echo "Running semver.jq tests..."
  echo "Using jq library path: $(pwd)"

  # Change to script directory for jq include path
  cd "$(dirname "$(readlink -f "$BASH_SOURCE")")"

  test_parse_semver
  test_is_valid_semver
  test_sort_semver
  test_sort_semver_desc
  test_match_semver
  test_compare_semver

  echo ""
  echo "=========================================="
  echo -e "Tests run: $TESTS_RUN"
  echo -e "${GREEN}Passed: $TESTS_PASSED${NC}"
  if [[ $TESTS_FAILED -gt 0 ]]; then
    echo -e "${RED}Failed: $TESTS_FAILED${NC}"
    exit 1
  else
    echo -e "Failed: 0"
    echo -e "${GREEN}All tests passed!${NC}"
  fi
}

main "$@"
