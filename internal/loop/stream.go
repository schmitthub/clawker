package loop

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/schmitthub/clawker/internal/logger"
)

// EventType discriminates top-level stream-json events.
type EventType string

const (
	EventTypeSystem    EventType = "system"
	EventTypeAssistant EventType = "assistant"
	EventTypeUser      EventType = "user"
	EventTypeResult    EventType = "result"
)

// System event subtypes.
const (
	SystemSubtypeInit            = "init"
	SystemSubtypeCompactBoundary = "compact_boundary"
)

// Result event subtypes.
const (
	ResultSubtypeSuccess              = "success"
	ResultSubtypeErrorMaxTurns        = "error_max_turns"
	ResultSubtypeErrorDuringExecution = "error_during_execution"
	ResultSubtypeErrorMaxBudget       = "error_max_budget_usd"
)

// Content block types within messages.
const (
	ContentTypeText       = "text"
	ContentTypeToolUse    = "tool_use"
	ContentTypeToolResult = "tool_result"
	ContentTypeThinking   = "thinking"
)

// maxScannerBuffer is the max line size for the NDJSON scanner (10 MB).
// Large tool results (file reads, search results) can produce very long lines.
const maxScannerBuffer = 10 * 1024 * 1024

// SystemEvent is emitted once at session start (init) or on conversation compaction.
type SystemEvent struct {
	Type           EventType `json:"type"`
	Subtype        string    `json:"subtype"`
	SessionID      string    `json:"session_id"`
	Model          string    `json:"model,omitempty"`
	Tools          []string  `json:"tools,omitempty"`
	CWD            string    `json:"cwd,omitempty"`
	PermissionMode string    `json:"permissionMode,omitempty"`

	// CompactBoundary-only fields.
	CompactMetadata *CompactMetadata `json:"compact_metadata,omitempty"`
}

// CompactMetadata describes a conversation compaction event.
type CompactMetadata struct {
	Trigger   string `json:"trigger"`
	PreTokens int    `json:"pre_tokens"`
}

// AssistantEvent is a complete assistant message containing text and/or tool invocations.
type AssistantEvent struct {
	Type            EventType      `json:"type"`
	SessionID       string         `json:"session_id"`
	ParentToolUseID *string        `json:"parent_tool_use_id"`
	Message         AssistantMessage `json:"message"`
}

// AssistantMessage is the Anthropic API message object embedded in an AssistantEvent.
type AssistantMessage struct {
	ID         string         `json:"id"`
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Content    []ContentBlock `json:"content"`
	Usage      *TokenUsage    `json:"usage,omitempty"`
}

// ExtractText returns the concatenated text from all text content blocks.
func (m *AssistantMessage) ExtractText() string {
	var texts []string
	for _, block := range m.Content {
		if block.Type == ContentTypeText && block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// ToolUseBlocks returns all tool_use content blocks from the message.
func (m *AssistantMessage) ToolUseBlocks() []ContentBlock {
	var blocks []ContentBlock
	for _, block := range m.Content {
		if block.Type == ContentTypeToolUse {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// ContentBlock is a polymorphic element in a message's content array.
// Discriminate on Type to determine which fields are populated.
type ContentBlock struct {
	Type string `json:"type"`

	// Text block: populated when Type == "text".
	Text string `json:"text,omitempty"`

	// Tool use block: populated when Type == "tool_use".
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// Tool result block: populated when Type == "tool_result".
	// Content is json.RawMessage because the API sends either a string or an array.
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`

	// Thinking block: populated when Type == "thinking".
	Thinking string `json:"thinking,omitempty"`
}

// ToolResultText extracts the tool result content as a string.
// Handles both the string form and the array-of-blocks form.
func (b *ContentBlock) ToolResultText() string {
	if b.Type != ContentTypeToolResult || len(b.Content) == 0 {
		return ""
	}
	// Try string first (most common).
	var s string
	if err := json.Unmarshal(b.Content, &s); err == nil {
		return s
	}
	// Try array of text blocks.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b.Content, &blocks); err == nil {
		var texts []string
		for _, block := range blocks {
			if block.Text != "" {
				texts = append(texts, block.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	// Fallback: raw JSON string.
	return string(b.Content)
}

// UserEvent is a tool result message returned after tool execution.
type UserEvent struct {
	Type            EventType        `json:"type"`
	SessionID       string           `json:"session_id"`
	ParentToolUseID *string          `json:"parent_tool_use_id"`
	Message         UserEventMessage `json:"message"`
}

// UserEventMessage is the user message embedded in a UserEvent.
type UserEventMessage struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// TokenUsage tracks API token consumption.
type TokenUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// Total returns the sum of input and output tokens.
// Cache tokens are not added separately because the Anthropic API's
// input_tokens field already accounts for cache reads in billing.
func (u *TokenUsage) Total() int {
	if u == nil {
		return 0
	}
	return u.InputTokens + u.OutputTokens
}

// ResultEvent is the final event in the stream, indicating completion or error.
type ResultEvent struct {
	Type          EventType   `json:"type"`
	Subtype       string      `json:"subtype"`
	SessionID     string      `json:"session_id"`
	IsError       bool        `json:"is_error"`
	DurationMS    int         `json:"duration_ms"`
	DurationAPIMS int         `json:"duration_api_ms"`
	NumTurns      int         `json:"num_turns"`
	TotalCostUSD  float64     `json:"total_cost_usd"`
	Usage         *TokenUsage `json:"usage,omitempty"`

	// Success-only fields.
	Result string `json:"result,omitempty"`

	// Error-only fields.
	Errors []string `json:"errors,omitempty"`
}

// IsSuccess returns true if this result represents a successful completion.
func (r *ResultEvent) IsSuccess() bool {
	return r.Subtype == ResultSubtypeSuccess
}

// CombinedText returns the result text (success) or joined error messages.
func (r *ResultEvent) CombinedText() string {
	if r.IsSuccess() {
		return r.Result
	}
	return strings.Join(r.Errors, "\n")
}

// StreamHandler receives parsed stream-json events via callbacks.
// All callbacks are optional — nil callbacks are skipped.
type StreamHandler struct {
	// OnSystem is called for system events (init, compact_boundary).
	OnSystem func(*SystemEvent)

	// OnAssistant is called for each complete assistant message.
	OnAssistant func(*AssistantEvent)

	// OnUser is called for each tool result message.
	OnUser func(*UserEvent)

	// OnResult is called when the final result event arrives.
	OnResult func(*ResultEvent)
}

// ParseStream reads NDJSON lines from r, dispatches events to handler,
// and returns the final ResultEvent. Returns an error if the stream ends
// without a result event, on context cancellation, or on scan failure.
//
// Malformed lines are debug-logged and skipped. Unrecognized event types
// are silently skipped for forward compatibility. Known event types that
// fail to parse (system, assistant, user) are warn-logged and skipped.
// A malformed result event returns an error (terminal event corruption).
func ParseStream(ctx context.Context, r io.Reader, handler *StreamHandler) (*ResultEvent, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBuffer)

	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Peek at the type field to determine which struct to unmarshal into.
		var envelope struct {
			Type EventType `json:"type"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			logger.Debug().Err(err).Int("line_len", len(line)).Msg("skipping malformed stream line")
			continue
		}

		switch envelope.Type {
		case EventTypeSystem:
			var event SystemEvent
			if err := json.Unmarshal(line, &event); err != nil {
				logger.Warn().Err(err).Str("type", "system").Msg("failed to parse known stream event")
				continue
			}
			if handler != nil && handler.OnSystem != nil {
				handler.OnSystem(&event)
			}

		case EventTypeAssistant:
			var event AssistantEvent
			if err := json.Unmarshal(line, &event); err != nil {
				logger.Warn().Err(err).Str("type", "assistant").Msg("failed to parse known stream event")
				continue
			}
			if handler != nil && handler.OnAssistant != nil {
				handler.OnAssistant(&event)
			}

		case EventTypeUser:
			var event UserEvent
			if err := json.Unmarshal(line, &event); err != nil {
				logger.Warn().Err(err).Str("type", "user").Msg("failed to parse known stream event")
				continue
			}
			if handler != nil && handler.OnUser != nil {
				handler.OnUser(&event)
			}

		case EventTypeResult:
			var event ResultEvent
			if err := json.Unmarshal(line, &event); err != nil {
				return nil, fmt.Errorf("failed to parse result event: %w", err)
			}
			if handler != nil && handler.OnResult != nil {
				handler.OnResult(&event)
			}
			return &event, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("stream read error: %w", err)
	}

	return nil, fmt.Errorf("stream ended without result event")
}

// TextAccumulator collects assistant text output across multiple messages.
// It is a convenience handler that aggregates text for LOOP_STATUS parsing
// and output analysis after the stream completes.
type TextAccumulator struct {
	texts     []string
	toolCalls int
}

// NewTextAccumulator creates a TextAccumulator and returns it along with
// a StreamHandler wired to collect text. Pass the handler to ParseStream.
func NewTextAccumulator() (*TextAccumulator, *StreamHandler) {
	acc := &TextAccumulator{}
	handler := &StreamHandler{
		OnAssistant: func(e *AssistantEvent) {
			if text := e.Message.ExtractText(); text != "" {
				acc.texts = append(acc.texts, text)
			}
			acc.toolCalls += len(e.Message.ToolUseBlocks())
		},
	}
	return acc, handler
}

// Text returns all accumulated assistant text joined by newlines.
func (a *TextAccumulator) Text() string {
	return strings.Join(a.texts, "\n")
}

// ToolCallCount returns the total number of tool invocations observed.
func (a *TextAccumulator) ToolCallCount() int {
	return a.toolCalls
}
