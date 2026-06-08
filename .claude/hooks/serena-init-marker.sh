#!/usr/bin/env bash
# PostToolUse hook: mark Serena as initialized for this session.
# Fires after mcp__serena__check_onboarding_performed completes.
#
# Keyed on the Claude session id (from stdin), NOT $PPID: hooks are spawned
# under drifting parents, so a $PPID marker written here may never be seen by
# the PreToolUse gate, leaving it stuck on a false DENY. Fall back to $PPID
# only if session_id is absent.
SID="$(jq -r '.session_id // empty')"
touch "/tmp/.claude_serena_init_${SID:-$PPID}"
