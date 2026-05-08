#!/usr/bin/env bash
# PostToolUse hook: mark Serena as initialized for this session.
# Fires after mcp__serena__check_onboarding_performed completes.
touch "/tmp/.claude_serena_init_${PPID}"
