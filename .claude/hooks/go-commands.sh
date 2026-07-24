#!/usr/bin/env bash
# PreToolUse[Bash] guard: block `go test ./...` (any flags/env, compound
# commands included). The repo-wide pattern matches test/e2e, whose suite
# tears down the host controlplane — running it from an agent session has
# repeatedly killed the live CP. Use `make test` or targeted packages.
set -euo pipefail

cmd=$(jq -r '.tool_input.command // ""')

if grep -qE '(^|[^[:alnum:]_-])go[[:space:]]+test([^|;&]*[[:space:]])?\./\.\.\.' <<<"$cmd"; then
  cat <<'JSON'
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"BLOCKED: `go test ./...` sweeps test/e2e, which tears down the host controlplane. Use `make test` or list specific packages (e.g. `go test ./controlplane/... ./internal/...`)."}}
JSON
fi
exit 0
