#!/usr/bin/env bash
# SessionStart hook: re-arm the Serena init gate at every session boundary.
#
# The init marker (/tmp/.claude_serena_init_<PPID>) is keyed on $PPID — the
# long-lived `claude` process, which survives /clear, /compact, and /resume.
# Without this reset the PreToolUse DENY gate (serena-first.sh) fires only ONCE
# per process lifetime: after the first init ever, every subsequent session
# sees the stale marker and silently skips enforcement.
#
# Deleting the marker here forces re-init on every session boundary. Runs
# unfiltered (startup|resume|clear|compact) so no boundary slips through.
rm -f "/tmp/.claude_serena_init_${PPID}"

# stdout from a SessionStart hook is injected as session context — proactively
# instruct the model to init before its first action so it doesn't eat a denied
# Read first. The PreToolUse gate is the hard backstop if this nudge is ignored.
cat <<'EOF'
SERENA INIT REQUIRED — strictly enforced this session. Before ANY Read/Edit, run:
1. mcp__serena__initial_instructions
2. mcp__serena__check_onboarding_performed
3. mcp__serena__list_memories
The PreToolUse gate will DENY Read/Edit until this completes.
EOF
