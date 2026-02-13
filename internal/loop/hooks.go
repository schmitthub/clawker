package loop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Hook event names matching Claude Code settings.json.
const (
	EventStop         = "Stop"
	EventSessionStart = "SessionStart"
	EventPreToolUse   = "PreToolUse"
	EventPostToolUse  = "PostToolUse"
	EventNotification = "Notification"
)

// Hook handler types matching Claude Code settings.json.
const (
	HandlerCommand = "command"
	HandlerPrompt  = "prompt"
	HandlerAgent   = "agent"
)

// Script paths inside the container.
const (
	HookScriptDir       = "/tmp/clawker-hooks"
	StopCheckScriptPath = "/tmp/clawker-hooks/stop-check.js"
)

// HookHandler is a single hook action matching the Claude Code settings.json format.
type HookHandler struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// HookMatcherGroup is a matcher regex paired with one or more hook handlers.
type HookMatcherGroup struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []HookHandler `json:"hooks"`
}

// HookConfig maps event names to matcher groups.
// This is the value of the "hooks" key in Claude Code's settings.json.
type HookConfig map[string][]HookMatcherGroup

// compactReminderText is the message output by the SessionStart compact hook.
// It reminds the agent about LOOP_STATUS requirements after context compaction.
const compactReminderText = `IMPORTANT REMINDER: You are running inside an autonomous loop managed by Clawker.
You MUST output a ---LOOP_STATUS--- block at the end of EVERY response.
If you do not include the LOOP_STATUS block, you will be prevented from stopping.
Refer to your system prompt for the exact format with all required fields.
---LOOP_STATUS--- and ---END_LOOP_STATUS--- markers are mandatory.`

// DefaultHooks returns the default hook configuration for loop execution.
// The Stop hook blocks Claude from stopping unless a LOOP_STATUS block is present.
// The SessionStart hook re-injects a LOOP_STATUS reminder after context compaction.
func DefaultHooks() HookConfig {
	return HookConfig{
		EventStop: {
			{
				Hooks: []HookHandler{
					{
						Type:    HandlerCommand,
						Command: "node " + StopCheckScriptPath,
						Timeout: 10,
					},
				},
			},
		},
		EventSessionStart: {
			{
				Matcher: "compact",
				Hooks: []HookHandler{
					{
						Type:    HandlerCommand,
						Command: compactReminderCommand(),
					},
				},
			},
		},
	}
}

// compactReminderCommand builds a shell command that outputs the compact reminder
// text with proper newlines. Uses printf to avoid shell-specific echo behavior.
func compactReminderCommand() string {
	return fmt.Sprintf("printf '%%s\\n' %s", shellQuote(compactReminderText))
}

// DefaultHookFiles returns the scripts that default hooks reference.
// Keys are absolute paths inside the container; values are file contents.
func DefaultHookFiles() map[string][]byte {
	return map[string][]byte{
		StopCheckScriptPath: []byte(stopCheckScript),
	}
}

// ResolveHooks loads hook configuration from a file or returns defaults.
// If hooksFile is empty, returns DefaultHooks() and DefaultHookFiles().
// If hooksFile is provided, it is read as a complete HookConfig replacement
// with no default hook files (custom hooks manage their own scripts).
func ResolveHooks(hooksFile string) (HookConfig, map[string][]byte, error) {
	if hooksFile == "" {
		return DefaultHooks(), DefaultHookFiles(), nil
	}

	data, err := os.ReadFile(hooksFile)
	if err != nil {
		return nil, nil, fmt.Errorf("reading hooks file: %w", err)
	}

	var config HookConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, nil, fmt.Errorf("parsing hooks file %s: %w", filepath.Base(hooksFile), err)
	}

	return config, nil, nil
}

// MarshalSettingsJSON serializes the hook config as a settings.json fragment
// with the "hooks" wrapper key: {"hooks": {...}}.
func (hc HookConfig) MarshalSettingsJSON() ([]byte, error) {
	wrapper := struct {
		Hooks HookConfig `json:"hooks"`
	}{
		Hooks: hc,
	}
	return json.MarshalIndent(wrapper, "", "  ")
}

// shellQuote wraps a string in single quotes with proper escaping.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// stopCheckScript is the Node.js script that enforces LOOP_STATUS output.
// It runs as a Stop hook: exit 0 allows stop, exit 2 blocks it.
const stopCheckScript = `#!/usr/bin/env node
'use strict';

// Stop hook for Clawker loop enforcement.
// Reads hook input from stdin, checks the transcript for a LOOP_STATUS block.
// Exit 0 = allow stop, Exit 2 = block stop (Claude retries).

const fs = require('fs');
const path = require('path');

// Self-defense timeout: allow stop if script takes too long
setTimeout(() => { process.exit(0); }, 5000);

let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (chunk) => { input += chunk; });
process.stdin.on('end', () => {
  try {
    const hookInput = JSON.parse(input);

    // Prevent recursion: if a stop hook is already active, allow stop
    if (hookInput.stop_hook_active) {
      process.exit(0);
    }

    const sessionId = hookInput.session_id;
    if (!sessionId) {
      // No session ID available — can't check transcript, allow stop
      process.exit(0);
    }

    // Find the transcript JSONL file for this session.
    // Claude Code stores transcripts at ~/.claude/projects/<hash>/<session_id>.jsonl
    const claudeDir = path.join(process.env.HOME || '/root', '.claude', 'projects');
    const transcript = findTranscript(claudeDir, sessionId);
    if (!transcript) {
      // Can't find transcript — allow stop gracefully
      process.exit(0);
    }

    // Read only the tail of the transcript to avoid loading large files.
    // 64KB is enough for the last several JSONL events.
    const tailSize = 64 * 1024;
    const stat = fs.statSync(transcript);
    const readSize = Math.min(stat.size, tailSize);
    const buf = Buffer.alloc(readSize);
    const fd = fs.openSync(transcript, 'r');
    fs.readSync(fd, buf, 0, readSize, Math.max(0, stat.size - readSize));
    fs.closeSync(fd);
    const lines = buf.toString('utf8').trim().split('\n');

    // Check the last 10 lines for an assistant message containing LOOP_STATUS
    const tail = lines.slice(-10);
    for (let i = tail.length - 1; i >= 0; i--) {
      try {
        const event = JSON.parse(tail[i]);
        if (event.type === 'assistant' && event.message) {
          const text = extractText(event.message);
          if (text.includes('---LOOP_STATUS---') && text.includes('---END_LOOP_STATUS---')) {
            process.exit(0);
          }
        }
      } catch (_) {
        // Skip malformed lines (including partial first line from tail read)
      }
    }

    // LOOP_STATUS not found — block the stop
    process.stderr.write(
      'LOOP_STATUS block not found in your last response. ' +
      'You MUST output a ---LOOP_STATUS--- block before stopping. ' +
      'Review your system prompt for the required format.'
    );
    process.exit(2);

  } catch (e) {
    // Unexpected error (TypeError, EPERM, etc.) — log and allow stop
    process.stderr.write('stop-check.js: unexpected error: ' + e.message + '\n');
    process.exit(0);
  }
});

// findTranscript searches for a JSONL file matching the session ID.
function findTranscript(claudeDir, sessionId) {
  const target = sessionId + '.jsonl';
  try {
    const dirs = fs.readdirSync(claudeDir);
    for (const dir of dirs) {
      const candidate = path.join(claudeDir, dir, target);
      if (fs.existsSync(candidate)) {
        return candidate;
      }
    }
  } catch (e) {
    // Directory doesn't exist or isn't readable
    process.stderr.write('stop-check.js: cannot read claude dir: ' + e.message + '\n');
  }
  return null;
}

// extractText concatenates all text content blocks from an assistant message.
function extractText(message) {
  if (!message.content || !Array.isArray(message.content)) return '';
  return message.content
    .filter(b => b.type === 'text' && b.text)
    .map(b => b.text)
    .join('\n');
}
`
