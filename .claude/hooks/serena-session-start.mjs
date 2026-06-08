#!/usr/bin/env node
// SessionStart hook: re-arm the Serena init gate at every session boundary.
//
// The init marker (/tmp/.claude_serena_init_<session_id>) is keyed STRICTLY on
// the Claude session id from stdin — stable across hook spawns and
// /compact|/resume, where $PPID is NOT (hooks run under drifting parents, so a
// $PPID-keyed marker written by the PostToolUse companion may never be found by
// the PreToolUse gate). There is deliberately NO $PPID fallback. Without this
// reset the PreToolUse DENY gate (serena-first.mjs) would, after the first
// init, see the stale marker and silently skip enforcement.
//
// Deleting the marker here forces re-init on every session boundary. Runs
// unfiltered (startup|resume|clear|compact) so no boundary slips through.
// Run by Claude Code's JS runtime — native JSON, no jq.
import { rmSync } from "node:fs";

const chunks = [];
for await (const c of process.stdin) chunks.push(c);
const input = JSON.parse(Buffer.concat(chunks).toString("utf8") || "{}");

if (input.session_id) {
  rmSync(`/tmp/.claude_serena_init_${input.session_id}`, { force: true });
}

// stdout from a SessionStart hook is injected as session context — proactively
// instruct the model to init before its first action so it doesn't eat a denied
// Read first. The PreToolUse gate is the hard backstop if this nudge is ignored.
process.stdout.write(`SERENA INIT REQUIRED — strictly enforced this session. Before ANY Read/Edit, run:
1. mcp__serena__initial_instructions
2. mcp__serena__check_onboarding_performed
3. mcp__serena__list_memories
The PreToolUse gate will DENY Read/Edit until this completes.
`);
