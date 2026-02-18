package shared

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Helpers ---

// readyLine is the ready signal prepended to NDJSON streams.
const readyLine = "[clawker] ready\n"

// ndjson joins lines into an NDJSON stream with the ready signal prefix.
func ndjson(lines ...string) string {
	return readyLine + strings.Join(lines, "\n") + "\n"
}

// --- ParseStream tests ---

func TestParseStream_FullConversation(t *testing.T) {
	stream := ndjson(
		`{"type":"system","subtype":"init","session_id":"s1","model":"claude-opus-4-6","tools":["Bash","Read"],"cwd":"/workspace","permissionMode":"default"}`,
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_01","role":"assistant","model":"claude-opus-4-6","stop_reason":"tool_use","content":[{"type":"text","text":"I'll search for the file."},{"type":"tool_use","id":"toolu_01","name":"Glob","input":{"pattern":"**/*.go"}}],"usage":{"input_tokens":100,"output_tokens":50}}}`,
		`{"type":"user","session_id":"s1","parent_tool_use_id":null,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"main.go\nutil.go"}]}}`,
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_02","role":"assistant","model":"claude-opus-4-6","stop_reason":"end_turn","content":[{"type":"text","text":"Found 2 Go files."}],"usage":{"input_tokens":200,"output_tokens":30}}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":5000,"duration_api_ms":4000,"num_turns":2,"result":"Found 2 Go files.","total_cost_usd":0.05,"usage":{"input_tokens":300,"output_tokens":80}}`,
	)

	var (
		systemEvents    []*SystemEvent
		assistantEvents []*AssistantEvent
		userEvents      []*UserEvent
		resultEvents    []*ResultEvent
	)

	handler := &StreamHandler{
		OnSystem:    func(e *SystemEvent) { systemEvents = append(systemEvents, e) },
		OnAssistant: func(e *AssistantEvent) { assistantEvents = append(assistantEvents, e) },
		OnUser:      func(e *UserEvent) { userEvents = append(userEvents, e) },
		OnResult:    func(e *ResultEvent) { resultEvents = append(resultEvents, e) },
	}

	result, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify callback counts.
	assert.Len(t, systemEvents, 1)
	assert.Len(t, assistantEvents, 2)
	assert.Len(t, userEvents, 1)
	assert.Len(t, resultEvents, 1)

	// System event.
	assert.Equal(t, SystemSubtypeInit, systemEvents[0].Subtype)
	assert.Equal(t, "s1", systemEvents[0].SessionID)
	assert.Equal(t, "claude-opus-4-6", systemEvents[0].Model)
	assert.Equal(t, []string{"Bash", "Read"}, systemEvents[0].Tools)
	assert.Equal(t, "/workspace", systemEvents[0].CWD)

	// First assistant message (text + tool_use).
	msg1 := assistantEvents[0].Message
	assert.Equal(t, "msg_01", msg1.ID)
	assert.Equal(t, "tool_use", msg1.StopReason)
	assert.Len(t, msg1.Content, 2)
	assert.Equal(t, "I'll search for the file.", msg1.Content[0].Text)
	assert.Equal(t, "Glob", msg1.Content[1].Name)
	assert.Equal(t, 100, msg1.Usage.InputTokens)

	// User tool result.
	assert.Equal(t, "user", userEvents[0].Message.Role)
	assert.Len(t, userEvents[0].Message.Content, 1)
	assert.Equal(t, ContentTypeToolResult, userEvents[0].Message.Content[0].Type)
	assert.Equal(t, "toolu_01", userEvents[0].Message.Content[0].ToolUseID)

	// Second assistant message (text only).
	msg2 := assistantEvents[1].Message
	assert.Equal(t, "end_turn", msg2.StopReason)
	assert.Equal(t, "Found 2 Go files.", msg2.ExtractText())

	// Result.
	assert.True(t, result.IsSuccess())
	assert.Equal(t, "Found 2 Go files.", result.Result)
	assert.Equal(t, 5000, result.DurationMS)
	assert.Equal(t, 2, result.NumTurns)
	assert.InDelta(t, 0.05, result.TotalCostUSD, 0.001)
	assert.Equal(t, 300, result.Usage.InputTokens)
	assert.Equal(t, 80, result.Usage.OutputTokens)
}

func TestParseStream_SystemInit(t *testing.T) {
	stream := ndjson(
		`{"type":"system","subtype":"init","session_id":"sess-abc","model":"claude-sonnet-4-5","tools":["Bash","Read","Write","Edit","Glob","Grep"],"cwd":"/home/user/project","permissionMode":"bypassPermissions"}`,
		`{"type":"result","subtype":"success","session_id":"sess-abc","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"done","total_cost_usd":0.01}`,
	)

	var sys *SystemEvent
	handler := &StreamHandler{
		OnSystem: func(e *SystemEvent) { sys = e },
	}

	result, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, sys)

	assert.Equal(t, EventTypeSystem, sys.Type)
	assert.Equal(t, SystemSubtypeInit, sys.Subtype)
	assert.Equal(t, "sess-abc", sys.SessionID)
	assert.Equal(t, "claude-sonnet-4-5", sys.Model)
	assert.Equal(t, "bypassPermissions", sys.PermissionMode)
	assert.Len(t, sys.Tools, 6)
	assert.Nil(t, sys.CompactMetadata)
}

func TestParseStream_CompactBoundary(t *testing.T) {
	stream := ndjson(
		`{"type":"system","subtype":"compact_boundary","session_id":"s1","compact_metadata":{"trigger":"auto","pre_tokens":150000}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	var sys *SystemEvent
	handler := &StreamHandler{
		OnSystem: func(e *SystemEvent) { sys = e },
	}

	result, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, sys)

	assert.Equal(t, SystemSubtypeCompactBoundary, sys.Subtype)
	require.NotNil(t, sys.CompactMetadata)
	assert.Equal(t, "auto", sys.CompactMetadata.Trigger)
	assert.Equal(t, 150000, sys.CompactMetadata.PreTokens)
}

func TestParseStream_SuccessResult(t *testing.T) {
	stream := ndjson(
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":5432,"duration_api_ms":4200,"num_turns":3,"result":"All tasks completed successfully.","total_cost_usd":0.0234,"usage":{"input_tokens":3000,"output_tokens":500,"cache_read_input_tokens":1200}}`,
	)

	result, err := ParseStream(context.Background(), strings.NewReader(stream), nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.True(t, result.IsSuccess())
	assert.False(t, result.IsError)
	assert.Equal(t, ResultSubtypeSuccess, result.Subtype)
	assert.Equal(t, "s1", result.SessionID)
	assert.Equal(t, 5432, result.DurationMS)
	assert.Equal(t, 4200, result.DurationAPIMS)
	assert.Equal(t, 3, result.NumTurns)
	assert.Equal(t, "All tasks completed successfully.", result.Result)
	assert.InDelta(t, 0.0234, result.TotalCostUSD, 0.0001)
	assert.Equal(t, 3000, result.Usage.InputTokens)
	assert.Equal(t, 500, result.Usage.OutputTokens)
	assert.Equal(t, 1200, result.Usage.CacheReadInputTokens)
	assert.Empty(t, result.Errors)
	assert.Equal(t, "All tasks completed successfully.", result.CombinedText())
}

func TestParseStream_ErrorMaxTurns(t *testing.T) {
	stream := ndjson(
		`{"type":"result","subtype":"error_max_turns","session_id":"s1","is_error":true,"duration_ms":60000,"duration_api_ms":55000,"num_turns":10,"total_cost_usd":0.15,"errors":["Maximum turns (10) reached"],"usage":{"input_tokens":5000,"output_tokens":2000}}`,
	)

	result, err := ParseStream(context.Background(), strings.NewReader(stream), nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.False(t, result.IsSuccess())
	assert.True(t, result.IsError)
	assert.Equal(t, ResultSubtypeErrorMaxTurns, result.Subtype)
	assert.Equal(t, 10, result.NumTurns)
	assert.Equal(t, []string{"Maximum turns (10) reached"}, result.Errors)
	assert.Empty(t, result.Result)
	assert.Equal(t, "Maximum turns (10) reached", result.CombinedText())
}

func TestParseStream_ErrorDuringExecution(t *testing.T) {
	stream := ndjson(
		`{"type":"result","subtype":"error_during_execution","session_id":"s1","is_error":true,"duration_ms":1000,"duration_api_ms":800,"num_turns":1,"total_cost_usd":0.001,"errors":["API error: 500 Internal Server Error","Retry limit exceeded"]}`,
	)

	result, err := ParseStream(context.Background(), strings.NewReader(stream), nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.False(t, result.IsSuccess())
	assert.Equal(t, ResultSubtypeErrorDuringExecution, result.Subtype)
	assert.Len(t, result.Errors, 2)
	assert.Equal(t, "API error: 500 Internal Server Error\nRetry limit exceeded", result.CombinedText())
}

func TestParseStream_ErrorMaxBudget(t *testing.T) {
	stream := ndjson(
		`{"type":"result","subtype":"error_max_budget_usd","session_id":"s1","is_error":true,"duration_ms":30000,"duration_api_ms":25000,"num_turns":5,"total_cost_usd":1.0,"errors":["Budget limit ($1.00) exceeded"]}`,
	)

	result, err := ParseStream(context.Background(), strings.NewReader(stream), nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.False(t, result.IsSuccess())
	assert.Equal(t, ResultSubtypeErrorMaxBudget, result.Subtype)
}

func TestParseStream_NoResultEvent(t *testing.T) {
	stream := ndjson(
		`{"type":"system","subtype":"init","session_id":"s1","model":"claude-opus-4-6"}`,
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_01","role":"assistant","model":"claude-opus-4-6","stop_reason":"end_turn","content":[{"type":"text","text":"hello"}]}}`,
	)

	result, err := ParseStream(context.Background(), strings.NewReader(stream), nil)
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stream ended without result event")
}

func TestParseStream_EmptyStream(t *testing.T) {
	result, err := ParseStream(context.Background(), strings.NewReader(""), nil)
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stream ended before ready signal")
}

func TestParseStream_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Pre-cancel the context so ParseStream checks immediately.
	cancel()

	// Even a simple stream should fail with cancelled context.
	stream := ndjson(
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	result, err := ParseStream(ctx, strings.NewReader(stream), nil)
	// The first scan succeeds (ready line), then context check triggers.
	assert.Nil(t, result)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestParseStream_MalformedLinesSkipped(t *testing.T) {
	stream := ndjson(
		`not json at all`,
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"unknown_future_type","data":"something"}`,
		`{{{invalid json`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	var systemCount int
	handler := &StreamHandler{
		OnSystem: func(e *SystemEvent) { systemCount++ },
	}

	result, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, systemCount)
	assert.True(t, result.IsSuccess())
}

func TestParseStream_EmptyLinesSkipped(t *testing.T) {
	stream := readyLine + "\n\n" + `{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}` + "\n\n"

	result, err := ParseStream(context.Background(), strings.NewReader(stream), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsSuccess())
}

func TestParseStream_NilHandler(t *testing.T) {
	stream := ndjson(
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_01","role":"assistant","model":"claude-opus-4-6","stop_reason":"end_turn","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	result, err := ParseStream(context.Background(), strings.NewReader(stream), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsSuccess())
}

func TestParseStream_MalformedResultEventReturnsError(t *testing.T) {
	stream := ndjson(
		`{"type":"result","subtype":123}`,
	)

	result, err := ParseStream(context.Background(), strings.NewReader(stream), nil)
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse result event")
}

func TestParseStream_ParentToolUseID(t *testing.T) {
	parentID := "toolu_parent_01"
	stream := ndjson(
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":"toolu_parent_01","message":{"id":"msg_sub","role":"assistant","model":"claude-opus-4-6","stop_reason":"end_turn","content":[{"type":"text","text":"subagent response"}]}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	var assistant *AssistantEvent
	handler := &StreamHandler{
		OnAssistant: func(e *AssistantEvent) { assistant = e },
	}

	result, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, assistant)
	require.NotNil(t, assistant.ParentToolUseID)
	assert.Equal(t, parentID, *assistant.ParentToolUseID)
}

func TestParseStream_NullParentToolUseID(t *testing.T) {
	stream := ndjson(
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_01","role":"assistant","model":"claude-opus-4-6","stop_reason":"end_turn","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	var assistant *AssistantEvent
	handler := &StreamHandler{
		OnAssistant: func(e *AssistantEvent) { assistant = e },
	}

	result, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, assistant)
	assert.Nil(t, assistant.ParentToolUseID)
}

func TestParseStream_ThinkingContentBlock(t *testing.T) {
	stream := ndjson(
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_01","role":"assistant","model":"claude-opus-4-6","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"Let me consider this carefully..."},{"type":"text","text":"Here is my answer."}]}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	var assistant *AssistantEvent
	handler := &StreamHandler{
		OnAssistant: func(e *AssistantEvent) { assistant = e },
	}

	result, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, assistant)

	assert.Len(t, assistant.Message.Content, 2)
	assert.Equal(t, ContentTypeThinking, assistant.Message.Content[0].Type)
	assert.Equal(t, "Let me consider this carefully...", assistant.Message.Content[0].Thinking)
	assert.Equal(t, ContentTypeText, assistant.Message.Content[1].Type)
	assert.Equal(t, "Here is my answer.", assistant.Message.Content[1].Text)

	// ExtractText should only return text blocks, not thinking.
	assert.Equal(t, "Here is my answer.", assistant.Message.ExtractText())
}

func TestParseStream_MultipleToolUses(t *testing.T) {
	stream := ndjson(
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_01","role":"assistant","model":"claude-opus-4-6","stop_reason":"tool_use","content":[{"type":"text","text":"I'll read two files."},{"type":"tool_use","id":"toolu_01","name":"Read","input":{"file_path":"/a.go"}},{"type":"tool_use","id":"toolu_02","name":"Read","input":{"file_path":"/b.go"}}]}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	var assistant *AssistantEvent
	handler := &StreamHandler{
		OnAssistant: func(e *AssistantEvent) { assistant = e },
	}

	result, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, result)

	toolBlocks := assistant.Message.ToolUseBlocks()
	assert.Len(t, toolBlocks, 2)
	assert.Equal(t, "Read", toolBlocks[0].Name)
	assert.Equal(t, "toolu_01", toolBlocks[0].ID)
	assert.Equal(t, "Read", toolBlocks[1].Name)
	assert.Equal(t, "toolu_02", toolBlocks[1].ID)

	// Verify input JSON is preserved.
	var input struct {
		FilePath string `json:"file_path"`
	}
	require.NoError(t, json.Unmarshal(toolBlocks[0].Input, &input))
	assert.Equal(t, "/a.go", input.FilePath)
}

func TestParseStream_CallbackOrder(t *testing.T) {
	stream := ndjson(
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_01","role":"assistant","model":"claude-opus-4-6","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","session_id":"s1","parent_tool_use_id":null,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"file1\nfile2"}]}}`,
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_02","role":"assistant","model":"claude-opus-4-6","stop_reason":"end_turn","content":[{"type":"text","text":"Done."}]}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":2,"result":"Done.","total_cost_usd":0.01}`,
	)

	var order []string
	handler := &StreamHandler{
		OnSystem:    func(e *SystemEvent) { order = append(order, "system") },
		OnAssistant: func(e *AssistantEvent) { order = append(order, "assistant") },
		OnUser:      func(e *UserEvent) { order = append(order, "user") },
		OnResult:    func(e *ResultEvent) { order = append(order, "result") },
	}

	_, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)

	assert.Equal(t, []string{"system", "assistant", "user", "assistant", "result"}, order)
}

func TestParseStream_WithLoopStatusBlock(t *testing.T) {
	// Simulate an assistant message containing a LOOP_STATUS block.
	statusText := "I've completed the implementation.\n\n---LOOP_STATUS---\nSTATUS: COMPLETE\nTASKS_COMPLETED_THIS_LOOP: 3\nFILES_MODIFIED: 5\nTESTS_STATUS: PASSING\nWORK_TYPE: IMPLEMENTATION\nEXIT_SIGNAL: true\nRECOMMENDATION: All tasks completed\n---END_LOOP_STATUS---"

	stream := ndjson(
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_01","role":"assistant","model":"claude-opus-4-6","stop_reason":"end_turn","content":[{"type":"text","text":"`+strings.ReplaceAll(statusText, "\n", "\\n")+`"}]}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	var assistant *AssistantEvent
	handler := &StreamHandler{
		OnAssistant: func(e *AssistantEvent) { assistant = e },
	}

	result, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Extract text and parse LOOP_STATUS with existing analyzer.
	text := assistant.Message.ExtractText()
	status := ParseStatus(text)
	require.NotNil(t, status)
	assert.Equal(t, "COMPLETE", status.Status)
	assert.Equal(t, 3, status.TasksCompleted)
	assert.Equal(t, 5, status.FilesModified)
	assert.True(t, status.ExitSignal)
}

// --- ContentBlock helper tests ---

func TestContentBlock_ToolResultText_String(t *testing.T) {
	block := ContentBlock{
		Type:      ContentTypeToolResult,
		ToolUseID: "toolu_01",
		Content:   json.RawMessage(`"file1.go\nfile2.go\nfile3.go"`),
	}

	assert.Equal(t, "file1.go\nfile2.go\nfile3.go", block.ToolResultText())
}

func TestContentBlock_ToolResultText_Array(t *testing.T) {
	block := ContentBlock{
		Type:      ContentTypeToolResult,
		ToolUseID: "toolu_01",
		Content:   json.RawMessage(`[{"type":"text","text":"line one"},{"type":"text","text":"line two"}]`),
	}

	assert.Equal(t, "line one\nline two", block.ToolResultText())
}

func TestContentBlock_ToolResultText_EmptyContent(t *testing.T) {
	block := ContentBlock{
		Type:      ContentTypeToolResult,
		ToolUseID: "toolu_01",
	}

	assert.Equal(t, "", block.ToolResultText())
}

func TestContentBlock_ToolResultText_WrongType(t *testing.T) {
	block := ContentBlock{
		Type: ContentTypeText,
		Text: "some text",
	}

	assert.Equal(t, "", block.ToolResultText())
}

func TestContentBlock_ToolResultText_RawFallback(t *testing.T) {
	// Content that is neither a string nor an array of blocks.
	block := ContentBlock{
		Type:      ContentTypeToolResult,
		ToolUseID: "toolu_01",
		Content:   json.RawMessage(`42`),
	}

	assert.Equal(t, "42", block.ToolResultText())
}

// --- AssistantMessage helper tests ---

func TestAssistantMessage_ExtractText_MultipleBlocks(t *testing.T) {
	msg := AssistantMessage{
		Content: []ContentBlock{
			{Type: ContentTypeText, Text: "First paragraph."},
			{Type: ContentTypeToolUse, Name: "Bash"},
			{Type: ContentTypeText, Text: "Second paragraph."},
		},
	}

	assert.Equal(t, "First paragraph.\nSecond paragraph.", msg.ExtractText())
}

func TestAssistantMessage_ExtractText_NoTextBlocks(t *testing.T) {
	msg := AssistantMessage{
		Content: []ContentBlock{
			{Type: ContentTypeToolUse, Name: "Bash"},
		},
	}

	assert.Equal(t, "", msg.ExtractText())
}

func TestAssistantMessage_ExtractText_EmptyContent(t *testing.T) {
	msg := AssistantMessage{}
	assert.Equal(t, "", msg.ExtractText())
}

func TestAssistantMessage_ToolUseBlocks_NoTools(t *testing.T) {
	msg := AssistantMessage{
		Content: []ContentBlock{
			{Type: ContentTypeText, Text: "Just text."},
		},
	}

	assert.Empty(t, msg.ToolUseBlocks())
}

// --- TokenUsage tests ---

func TestTokenUsage_Total(t *testing.T) {
	usage := TokenUsage{
		InputTokens:  1000,
		OutputTokens: 500,
	}
	assert.Equal(t, 1500, usage.Total())
}

func TestTokenUsage_Total_Nil(t *testing.T) {
	var usage *TokenUsage
	assert.Equal(t, 0, usage.Total())
}

// --- ResultEvent helper tests ---

func TestResultEvent_IsSuccess(t *testing.T) {
	tests := []struct {
		subtype  string
		expected bool
	}{
		{ResultSubtypeSuccess, true},
		{ResultSubtypeErrorMaxTurns, false},
		{ResultSubtypeErrorDuringExecution, false},
		{ResultSubtypeErrorMaxBudget, false},
	}

	for _, tt := range tests {
		t.Run(tt.subtype, func(t *testing.T) {
			r := ResultEvent{Subtype: tt.subtype}
			assert.Equal(t, tt.expected, r.IsSuccess())
		})
	}
}

func TestResultEvent_CombinedText_Success(t *testing.T) {
	r := ResultEvent{
		Subtype: ResultSubtypeSuccess,
		Result:  "All done.",
	}
	assert.Equal(t, "All done.", r.CombinedText())
}

func TestResultEvent_CombinedText_Error(t *testing.T) {
	r := ResultEvent{
		Subtype: ResultSubtypeErrorMaxTurns,
		Errors:  []string{"Error one", "Error two"},
	}
	assert.Equal(t, "Error one\nError two", r.CombinedText())
}

func TestResultEvent_CombinedText_EmptySuccess(t *testing.T) {
	r := ResultEvent{
		Subtype: ResultSubtypeSuccess,
	}
	assert.Equal(t, "", r.CombinedText())
}

func TestResultEvent_CombinedText_ErrorNoMessages(t *testing.T) {
	r := ResultEvent{
		Subtype: ResultSubtypeErrorMaxTurns,
		IsError: true,
	}
	assert.Equal(t, "", r.CombinedText())
}

// --- TextAccumulator tests ---

func TestTextAccumulator_FullConversation(t *testing.T) {
	stream := ndjson(
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_01","role":"assistant","model":"claude-opus-4-6","stop_reason":"tool_use","content":[{"type":"text","text":"Let me check."},{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","session_id":"s1","parent_tool_use_id":null,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"file.go"}]}}`,
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_02","role":"assistant","model":"claude-opus-4-6","stop_reason":"end_turn","content":[{"type":"text","text":"Found the file."}]}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":2,"result":"ok","total_cost_usd":0.01}`,
	)

	acc, handler := NewTextAccumulator()
	result, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "Let me check.\nFound the file.", acc.Text())
	assert.Equal(t, 1, acc.ToolCallCount())
}

func TestTextAccumulator_NoText(t *testing.T) {
	stream := ndjson(
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_01","role":"assistant","model":"claude-opus-4-6","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	acc, handler := NewTextAccumulator()
	_, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)

	assert.Equal(t, "", acc.Text())
	assert.Equal(t, 1, acc.ToolCallCount())
}

func TestTextAccumulator_WithLoopStatus(t *testing.T) {
	statusText := "Done!\n\n---LOOP_STATUS---\nSTATUS: COMPLETE\nEXIT_SIGNAL: true\nRECOMMENDATION: All good\n---END_LOOP_STATUS---"

	stream := ndjson(
		`{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_01","role":"assistant","model":"claude-opus-4-6","stop_reason":"end_turn","content":[{"type":"text","text":"`+strings.ReplaceAll(statusText, "\n", "\\n")+`"}]}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	acc, handler := NewTextAccumulator()
	_, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)

	// Verify accumulated text can be fed to AnalyzeOutput.
	text := acc.Text()
	analysis := AnalyzeOutput(text)
	require.NotNil(t, analysis.Status)
	assert.Equal(t, "COMPLETE", analysis.Status.Status)
	assert.True(t, analysis.Status.ExitSignal)
}

// --- Event type constant tests ---

func TestEventTypeConstants(t *testing.T) {
	assert.Equal(t, EventType("system"), EventTypeSystem)
	assert.Equal(t, EventType("assistant"), EventTypeAssistant)
	assert.Equal(t, EventType("user"), EventTypeUser)
	assert.Equal(t, EventType("result"), EventTypeResult)
}

func TestContentTypeConstants(t *testing.T) {
	assert.Equal(t, "text", ContentTypeText)
	assert.Equal(t, "tool_use", ContentTypeToolUse)
	assert.Equal(t, "tool_result", ContentTypeToolResult)
	assert.Equal(t, "thinking", ContentTypeThinking)
}

func TestResultSubtypeConstants(t *testing.T) {
	assert.Equal(t, "success", ResultSubtypeSuccess)
	assert.Equal(t, "error_max_turns", ResultSubtypeErrorMaxTurns)
	assert.Equal(t, "error_during_execution", ResultSubtypeErrorDuringExecution)
	assert.Equal(t, "error_max_budget_usd", ResultSubtypeErrorMaxBudget)
}

func TestSystemSubtypeConstants(t *testing.T) {
	assert.Equal(t, "init", SystemSubtypeInit)
	assert.Equal(t, "compact_boundary", SystemSubtypeCompactBoundary)
}

// --- StreamDeltaEvent helper tests ---

func TestStreamDeltaEvent_TextDelta(t *testing.T) {
	e := StreamDeltaEvent{
		Event: StreamAPIEvent{
			Type:  "content_block_delta",
			Delta: &StreamDelta{Type: "text_delta", Text: "hello"},
		},
	}
	assert.Equal(t, "hello", e.TextDelta())
}

func TestStreamDeltaEvent_TextDelta_InputJSON(t *testing.T) {
	e := StreamDeltaEvent{
		Event: StreamAPIEvent{
			Type:  "content_block_delta",
			Delta: &StreamDelta{Type: "input_json_delta", PartialJSON: `{"key"`},
		},
	}
	assert.Equal(t, "", e.TextDelta())
}

func TestStreamDeltaEvent_TextDelta_NilDelta(t *testing.T) {
	e := StreamDeltaEvent{
		Event: StreamAPIEvent{Type: "content_block_delta"},
	}
	assert.Equal(t, "", e.TextDelta())
}

func TestStreamDeltaEvent_TextDelta_WrongEventType(t *testing.T) {
	e := StreamDeltaEvent{
		Event: StreamAPIEvent{
			Type:  "message_start",
			Delta: &StreamDelta{Type: "text_delta", Text: "nope"},
		},
	}
	assert.Equal(t, "", e.TextDelta())
}

func TestStreamDeltaEvent_ToolName(t *testing.T) {
	e := StreamDeltaEvent{
		Event: StreamAPIEvent{
			Type:         "content_block_start",
			ContentBlock: &StreamContentBlock{Type: "tool_use", Name: "Bash", ID: "toolu_01"},
		},
	}
	assert.Equal(t, "Bash", e.ToolName())
	assert.True(t, e.IsToolStart())
}

func TestStreamDeltaEvent_ToolName_TextBlock(t *testing.T) {
	e := StreamDeltaEvent{
		Event: StreamAPIEvent{
			Type:         "content_block_start",
			ContentBlock: &StreamContentBlock{Type: "text"},
		},
	}
	assert.Equal(t, "", e.ToolName())
	assert.False(t, e.IsToolStart())
}

func TestStreamDeltaEvent_ToolName_WrongEventType(t *testing.T) {
	e := StreamDeltaEvent{
		Event: StreamAPIEvent{
			Type:         "content_block_delta",
			ContentBlock: &StreamContentBlock{Type: "tool_use", Name: "Bash"},
		},
	}
	assert.Equal(t, "", e.ToolName())
}

func TestStreamDeltaEvent_ToolName_NilContentBlock(t *testing.T) {
	e := StreamDeltaEvent{
		Event: StreamAPIEvent{Type: "content_block_start"},
	}
	assert.Equal(t, "", e.ToolName())
}

func TestStreamDeltaEvent_IsContentBlockStop(t *testing.T) {
	e := StreamDeltaEvent{
		Event: StreamAPIEvent{Type: "content_block_stop", Index: 1},
	}
	assert.True(t, e.IsContentBlockStop())
}

func TestStreamDeltaEvent_IsContentBlockStop_False(t *testing.T) {
	e := StreamDeltaEvent{
		Event: StreamAPIEvent{Type: "content_block_delta"},
	}
	assert.False(t, e.IsContentBlockStop())
}

func TestStreamDeltaEvent_UUID(t *testing.T) {
	stream := ndjson(
		`{"type":"stream_event","uuid":"evt-123","session_id":"s1","parent_tool_use_id":"toolu_parent","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	var streamEvent *StreamDeltaEvent
	handler := &StreamHandler{
		OnStreamEvent: func(e *StreamDeltaEvent) { streamEvent = e },
	}

	_, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, streamEvent)

	assert.Equal(t, "evt-123", streamEvent.UUID)
	require.NotNil(t, streamEvent.ParentToolUseID)
	assert.Equal(t, "toolu_parent", *streamEvent.ParentToolUseID)
	assert.Equal(t, "hi", streamEvent.TextDelta())
}

func TestStreamDeltaEvent_ToolUseBlock_Parse(t *testing.T) {
	stream := ndjson(
		`{"type":"stream_event","session_id":"s1","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_01","name":"Read"}}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	var streamEvent *StreamDeltaEvent
	handler := &StreamHandler{
		OnStreamEvent: func(e *StreamDeltaEvent) { streamEvent = e },
	}

	_, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, streamEvent)

	assert.Equal(t, "Read", streamEvent.ToolName())
	assert.True(t, streamEvent.IsToolStart())
	require.NotNil(t, streamEvent.Event.ContentBlock)
	assert.Equal(t, "toolu_01", streamEvent.Event.ContentBlock.ID)
}

func TestStreamDeltaEvent_MessageDelta_Parse(t *testing.T) {
	stream := ndjson(
		`{"type":"stream_event","session_id":"s1","event":{"type":"message_delta","message":{"stop_reason":"end_turn"},"usage":{"input_tokens":500,"output_tokens":200}}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	var streamEvent *StreamDeltaEvent
	handler := &StreamHandler{
		OnStreamEvent: func(e *StreamDeltaEvent) { streamEvent = e },
	}

	_, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, streamEvent)

	assert.Equal(t, "message_delta", streamEvent.Event.Type)
	require.NotNil(t, streamEvent.Event.Message)
	assert.Equal(t, "end_turn", streamEvent.Event.Message.StopReason)
	require.NotNil(t, streamEvent.Event.Usage)
	assert.Equal(t, 500, streamEvent.Event.Usage.InputTokens)
}

// --- Ready signal gating tests ---

func TestParseStream_WaitsForReadySignal(t *testing.T) {
	// Lines before the ready signal should be skipped; JSON parsed after.
	stream := "[clawker] installing packages...\n" +
		"[clawker] setting up workspace\n" +
		"[clawker] ready\n" +
		ndjson(
			`{"type":"system","subtype":"init","session_id":"s1","model":"claude-opus-4-6"}`,
			`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
		)

	var sys *SystemEvent
	handler := &StreamHandler{
		OnSystem: func(e *SystemEvent) { sys = e },
	}

	result, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, sys)
	assert.Equal(t, "s1", sys.SessionID)
	assert.True(t, result.IsSuccess())
}

func TestParseStream_ErrorSignalDuringInit(t *testing.T) {
	stream := "[clawker] installing packages...\n" +
		"[clawker] error: entrypoint failed: exit code 1\n" +
		"[clawker] ready\n" // should never reach this

	result, err := ParseStream(context.Background(), strings.NewReader(stream), nil)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container init failed")
	assert.Contains(t, err.Error(), "entrypoint failed")
}

func TestParseStream_StreamEndsBeforeReady(t *testing.T) {
	stream := "[clawker] installing packages...\n" +
		"[clawker] setting up workspace\n"

	result, err := ParseStream(context.Background(), strings.NewReader(stream), nil)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream ended before ready signal")
}

func TestParseStream_ContextCancelledDuringGate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	stream := "[clawker] installing packages...\n" +
		"[clawker] ready\n"

	result, err := ParseStream(ctx, strings.NewReader(stream), nil)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, context.Canceled)
}

// --- UUID on other event types ---

func TestSystemEvent_UUID(t *testing.T) {
	stream := ndjson(
		`{"type":"system","uuid":"sys-uuid-1","subtype":"init","session_id":"s1","model":"claude-opus-4-6"}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	var sys *SystemEvent
	handler := &StreamHandler{
		OnSystem: func(e *SystemEvent) { sys = e },
	}

	_, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, sys)
	assert.Equal(t, "sys-uuid-1", sys.UUID)
}

func TestAssistantEvent_UUID(t *testing.T) {
	stream := ndjson(
		`{"type":"assistant","uuid":"ast-uuid-1","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_01","role":"assistant","model":"claude-opus-4-6","stop_reason":"end_turn","content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	var ast *AssistantEvent
	handler := &StreamHandler{
		OnAssistant: func(e *AssistantEvent) { ast = e },
	}

	_, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, ast)
	assert.Equal(t, "ast-uuid-1", ast.UUID)
}

func TestUserEvent_UUID(t *testing.T) {
	stream := ndjson(
		`{"type":"user","uuid":"usr-uuid-1","session_id":"s1","parent_tool_use_id":null,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"done"}]}}`,
		`{"type":"result","subtype":"success","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":80,"num_turns":1,"result":"ok","total_cost_usd":0.01}`,
	)

	var usr *UserEvent
	handler := &StreamHandler{
		OnUser: func(e *UserEvent) { usr = e },
	}

	_, err := ParseStream(context.Background(), strings.NewReader(stream), handler)
	require.NoError(t, err)
	require.NotNil(t, usr)
	assert.Equal(t, "usr-uuid-1", usr.UUID)
}
