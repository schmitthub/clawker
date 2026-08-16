#!/usr/bin/env node
// PostToolUse hook: mark Serena as initialized for this session.
// Fires after mcp__serena__check_onboarding_performed completes.
//
// Run by Claude Code's JS runtime — native JSON, no jq. Keyed STRICTLY on the
// Claude session id from stdin. There is deliberately NO $PPID fallback: hooks
// are spawned under drifting parents, so a $PPID-keyed marker written here may
// never be seen by the PreToolUse gate, leaving it stuck on a false DENY.
// Without a session id we write nothing rather than strand a useless marker.
import { closeSync, openSync } from "node:fs";

const chunks = [];
for await (const c of process.stdin) chunks.push(c);
const input = JSON.parse(Buffer.concat(chunks).toString("utf8") || "{}");

if (input.session_id) {
  // touch: create the marker if absent, leave it otherwise.
  closeSync(openSync(`/tmp/.claude_serena_init_${input.session_id}`, "a"));
}
