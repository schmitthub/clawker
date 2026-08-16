#!/usr/bin/env node
// PreToolUse hook: enforce Serena semantic tools before Read/Edit.
// Lives in settings.local.json (personal, not checked in) because not all
// contributors have Serena configured.
//
// Run by Claude Code's JS runtime — native JSON in/out, no jq, no shell-escape
// hazards. Keyed STRICTLY on the Claude session id (from stdin), which is
// stable across hook spawns, /compact, and /resume. There is deliberately NO
// $PPID fallback: hooks run under drifting parents, so a $PPID-keyed marker
// written by the PostToolUse companion may never be found by this gate
// (permanent false DENY). Without a session id the gate fails closed.
//
// Session init tracking: checks for /tmp/.claude_serena_init_<session_id>.
// A companion PostToolUse hook on mcp__serena__check_onboarding_performed
// creates this marker after Serena init completes.
import { existsSync } from "node:fs";

const chunks = [];
for await (const c of process.stdin) chunks.push(c);
const input = JSON.parse(Buffer.concat(chunks).toString("utf8") || "{}");
const sid = input.session_id;

const inited = sid ? existsSync(`/tmp/.claude_serena_init_${sid}`) : false;

// DENY when not initialized: an "allow" decision's reason is NOT surfaced to
// the model — it only justifies auto-approval, so a "STOP" reason on an allow
// is silently ignored and the Read proceeds. "deny" blocks the tool AND feeds
// the reason back to the model as actionable feedback, forcing init first. A
// missing session id is treated as not-initialized (fail closed) — the gate
// cannot verify init without it.
const permissionDecision = inited ? "allow" : "deny";
const permissionDecisionReason = inited
  ? `SERENA FIRST: Before reading or editing files, use Serena semantic tools to explore and understand existing related code. This avoids guessing, grepping, and writing repetitive overly-specific logic.

Required workflow:
1. get_symbols_overview — understand file/module structure
2. find_symbol — locate specific types, functions, interfaces
3. find_referencing_symbols — understand callers and dependencies before changing APIs
4. search_for_pattern — find broader code patterns across the codebase

Try Serena for ANY file type (Go, Markdown, YAML, Bash, etc). Only fall back to Read/Edit if Serena does not support that language.`
  : `STOP. Serena has NOT been initialized this session. Before doing ANYTHING else, run the Serena init sequence:

1. mcp__serena__initial_instructions
2. mcp__serena__check_onboarding_performed
3. mcp__serena__list_memories

Do NOT proceed with Read/Edit until Serena init is complete.`;

process.stdout.write(
  JSON.stringify({
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision,
      permissionDecisionReason,
    },
  }),
);
