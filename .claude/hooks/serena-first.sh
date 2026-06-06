#!/usr/bin/env bash
# PreToolUse hook: enforce Serena semantic tools before Read/Edit.
# Lives in settings.local.json (personal, not checked in) because
# not all contributors have Serena configured.
#
# Session init tracking: checks for /tmp/.claude_serena_init_<ppid>.
# A companion PostToolUse hook on mcp__serena__check_onboarding_performed
# creates this marker after Serena init completes.

MARKER="/tmp/.claude_serena_init_${PPID}"

if [[ ! -f "$MARKER" ]]; then
  # DENY: an "allow" decision's reason is NOT surfaced to the model — it only
  # justifies auto-approval, so a "STOP" reason on an allow is silently ignored
  # and the Read proceeds. "deny" blocks the tool AND feeds the reason back to
  # the model as actionable feedback, forcing the init sequence to run first.
  DECISION="deny"
  REASON="STOP. Serena has NOT been initialized this session. Before doing ANYTHING else, run the Serena init sequence:

1. mcp__serena__initial_instructions
2. mcp__serena__check_onboarding_performed
3. mcp__serena__list_memories

Do NOT proceed with Read/Edit until Serena init is complete."
else
  DECISION="allow"
  REASON="SERENA FIRST: Before reading or editing files, use Serena semantic tools to explore and understand existing related code. This avoids guessing, grepping, and writing repetitive overly-specific logic.

Required workflow:
1. get_symbols_overview — understand file/module structure
2. find_symbol — locate specific types, functions, interfaces
3. find_referencing_symbols — understand callers and dependencies before changing APIs
4. search_for_pattern — find broader code patterns across the codebase

Try Serena for ANY file type (Go, Markdown, YAML, Bash, etc). Only fall back to Read/Edit if Serena does not support that language."
fi

jq -n --arg reason "$REASON" --arg decision "$DECISION" '{
  hookSpecificOutput: {
    hookEventName: "PreToolUse",
    permissionDecision: $decision,
    permissionDecisionReason: $reason
  }
}'
