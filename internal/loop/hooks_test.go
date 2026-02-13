package loop

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- DefaultHooks ---

func TestDefaultHooks_HasExpectedEvents(t *testing.T) {
	hooks := DefaultHooks()

	assert.Contains(t, hooks, EventStop, "default hooks must include Stop event")
	assert.Contains(t, hooks, EventSessionStart, "default hooks must include SessionStart event")
}

func TestDefaultHooks_StopEvent(t *testing.T) {
	hooks := DefaultHooks()

	groups := hooks[EventStop]
	require.Len(t, groups, 1, "Stop event should have exactly one matcher group")

	group := groups[0]
	assert.Empty(t, group.Matcher, "Stop hook should match all stop events (no matcher)")
	require.Len(t, group.Hooks, 1, "Stop group should have exactly one handler")

	handler := group.Hooks[0]
	assert.Equal(t, HandlerCommand, handler.Type)
	assert.Contains(t, handler.Command, StopCheckScriptPath, "command must reference stop-check script")
	assert.Contains(t, handler.Command, "node", "command must use node to execute the script")
	assert.Greater(t, handler.Timeout, 0, "stop hook must have a timeout to prevent hangs")
}

func TestDefaultHooks_SessionStartEvent(t *testing.T) {
	hooks := DefaultHooks()

	groups := hooks[EventSessionStart]
	require.Len(t, groups, 1, "SessionStart event should have exactly one matcher group")

	group := groups[0]
	assert.Equal(t, "compact", group.Matcher, "SessionStart hook should only fire on compact events")
	require.Len(t, group.Hooks, 1, "SessionStart group should have exactly one handler")

	handler := group.Hooks[0]
	assert.Equal(t, HandlerCommand, handler.Type)
	assert.NotEmpty(t, handler.Command, "compact reminder command should not be empty")
}

// --- DefaultHookFiles ---

func TestDefaultHookFiles_ContainsStopScript(t *testing.T) {
	files := DefaultHookFiles()

	content, ok := files[StopCheckScriptPath]
	assert.True(t, ok, "DefaultHookFiles must contain the stop-check script at %s", StopCheckScriptPath)
	assert.NotEmpty(t, content, "stop-check script must not be empty")
}

func TestDefaultHookFiles_StopScriptIsValidJS(t *testing.T) {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found, skipping JS syntax check")
	}

	files := DefaultHookFiles()
	content := files[StopCheckScriptPath]

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "stop-check.js")
	require.NoError(t, os.WriteFile(scriptPath, content, 0o644))

	cmd := exec.Command(nodeBin, "--check", scriptPath)
	out, err := cmd.CombinedOutput()
	assert.NoError(t, err, "stop-check.js has syntax errors: %s", string(out))
}

func TestDefaultHookFiles_OnlyExpectedFiles(t *testing.T) {
	files := DefaultHookFiles()

	assert.Len(t, files, 1, "DefaultHookFiles should contain exactly one file")
	assert.Contains(t, files, StopCheckScriptPath)
}

// --- ResolveHooks ---

func TestResolveHooks_DefaultsWhenEmpty(t *testing.T) {
	hooks, files, err := ResolveHooks("")
	require.NoError(t, err)

	assert.Equal(t, DefaultHooks(), hooks)
	assert.Equal(t, DefaultHookFiles(), files)
}

func TestResolveHooks_CustomFile(t *testing.T) {
	custom := HookConfig{
		EventPreToolUse: {
			{
				Matcher: "Bash",
				Hooks: []HookHandler{
					{Type: HandlerCommand, Command: "echo 'pre-tool'"},
				},
			},
		},
	}

	dir := t.TempDir()
	filePath := filepath.Join(dir, "hooks.json")
	data, err := json.Marshal(custom)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filePath, data, 0o644))

	hooks, files, err := ResolveHooks(filePath)
	require.NoError(t, err)

	// Custom hooks should completely replace defaults
	assert.Contains(t, hooks, EventPreToolUse)
	assert.NotContains(t, hooks, EventStop, "custom hooks replace defaults entirely")

	// Custom hooks should not include default hook files
	assert.Empty(t, files, "custom hooks should not include default hook files")
}

func TestResolveHooks_FileNotFound(t *testing.T) {
	_, _, err := ResolveHooks("/nonexistent/hooks.json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading hooks file")
}

func TestResolveHooks_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(filePath, []byte("not json{"), 0o644))

	_, _, err := ResolveHooks(filePath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing hooks file")
}

// --- MarshalSettingsJSON ---

func TestHookConfig_MarshalSettingsJSON(t *testing.T) {
	hooks := DefaultHooks()

	data, err := hooks.MarshalSettingsJSON()
	require.NoError(t, err)

	// Must produce valid JSON
	assert.True(t, json.Valid(data), "MarshalSettingsJSON must produce valid JSON")

	// Must have "hooks" wrapper key
	var wrapper map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &wrapper))
	assert.Contains(t, wrapper, "hooks", "output must have a 'hooks' top-level key")
}

func TestHookConfig_MarshalSettingsJSON_RoundTrip(t *testing.T) {
	original := DefaultHooks()

	data, err := original.MarshalSettingsJSON()
	require.NoError(t, err)

	var wrapper struct {
		Hooks HookConfig `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(data, &wrapper))

	assert.Equal(t, original, wrapper.Hooks, "round-trip marshal/unmarshal should produce identical config")
}

func TestHookConfig_MarshalSettingsJSON_EmptyConfig(t *testing.T) {
	hooks := HookConfig{}

	data, err := hooks.MarshalSettingsJSON()
	require.NoError(t, err)

	var wrapper map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &wrapper))
	assert.Contains(t, wrapper, "hooks")
}

// --- Stop-check script content contract ---

func TestStopCheckScript_ContainsExpectedPatterns(t *testing.T) {
	files := DefaultHookFiles()
	script := string(files[StopCheckScriptPath])

	assert.Contains(t, script, "stop_hook_active",
		"stop script must check stop_hook_active for recursion prevention")

	assert.Contains(t, script, "LOOP_STATUS",
		"stop script must search for LOOP_STATUS in transcript")

	assert.Contains(t, script, "process.exit(2)",
		"stop script must use exit code 2 to block stop")

	assert.Contains(t, script, "process.exit(0)",
		"stop script must use exit code 0 to allow stop")

	assert.Contains(t, script, "stdin",
		"stop script must read hook input from stdin")
}

func TestStopCheckScript_CatchBlocksLogErrors(t *testing.T) {
	files := DefaultHookFiles()
	script := string(files[StopCheckScriptPath])

	assert.Contains(t, script, "stop-check.js: unexpected error:",
		"outermost catch must log errors to stderr before exiting")

	assert.Contains(t, script, "stop-check.js: cannot read claude dir:",
		"findTranscript catch must log directory access errors to stderr")
}

func TestStopCheckScript_HasSelfDefenseTimeout(t *testing.T) {
	files := DefaultHookFiles()
	script := string(files[StopCheckScriptPath])

	assert.Contains(t, script, "setTimeout",
		"stop script must have a self-defense timeout")
}

func TestStopCheckScript_ContainsLoopStatusMarkers(t *testing.T) {
	files := DefaultHookFiles()
	script := string(files[StopCheckScriptPath])

	assert.Contains(t, script, "---LOOP_STATUS---")
	assert.Contains(t, script, "---END_LOOP_STATUS---")
}

// --- Compact reminder text ---

func TestCompactReminderText(t *testing.T) {
	hooks := DefaultHooks()

	groups := hooks[EventSessionStart]
	require.NotEmpty(t, groups)

	handler := groups[0].Hooks[0]
	assert.Contains(t, handler.Command, "LOOP_STATUS",
		"compact reminder must reference LOOP_STATUS")
}

// --- Constants ---

func TestEventConstants_AreNonEmpty(t *testing.T) {
	events := []string{EventStop, EventSessionStart, EventPreToolUse, EventPostToolUse, EventNotification}
	for _, e := range events {
		assert.NotEmpty(t, e, "event constant must not be empty")
	}
}

func TestHandlerTypeConstants_AreNonEmpty(t *testing.T) {
	types := []string{HandlerCommand, HandlerPrompt, HandlerAgent}
	for _, h := range types {
		assert.NotEmpty(t, h, "handler type constant must not be empty")
	}
}

func TestHookScriptDir_IsAbsolute(t *testing.T) {
	assert.True(t, strings.HasPrefix(HookScriptDir, "/"),
		"HookScriptDir must be an absolute path")
}

func TestStopCheckScriptPath_IsUnderHookScriptDir(t *testing.T) {
	assert.True(t, strings.HasPrefix(StopCheckScriptPath, HookScriptDir),
		"StopCheckScriptPath must be under HookScriptDir")
}
