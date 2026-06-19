package agent

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
)

// TestAgentEvent_MarshalZerologObject asserts the (Type, Action)
// discriminator survives on the marshaled audit fields. The legacy
// EventName()/OccurredAt() identity methods are gone — the discriminator
// is no longer a category string an audit hook keys on (that hook now
// keys on the envelope Source/Timestamp), but it MUST still ride the
// marshaled payload as type/action so a format regression (wrong field
// name, swapped Type/Action, dropped field) doesn't ship silently.
func TestAgentEvent_MarshalZerologObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		typ        EventType
		action     Action
		wantType   string
		wantAction string
	}{
		{
			name:       "session connected",
			typ:        DialerEventType,
			action:     ActionConnected,
			wantType:   "session",
			wantAction: "connected",
		},
		{
			name:       "exec failed",
			typ:        ExecutorEventType,
			action:     ActionExecFailed,
			wantType:   "exec",
			wantAction: "exec_failed",
		},
		{
			name:       "registry untrusted",
			typ:        RegistryEventType,
			action:     ActionUntrusted,
			wantType:   "registry",
			wantAction: "untrusted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := AgentEvent{Message: Message{Type: tt.typ, Action: tt.action}}

			var buf bytes.Buffer
			lg := zerolog.New(&buf)
			lg.Log().EmbedObject(e).Send()

			var got map[string]any
			if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal marshaled event: %v\nraw: %s", err, buf.String())
			}
			if got["type"] != tt.wantType {
				t.Errorf("type field = %v, want %q", got["type"], tt.wantType)
			}
			if got["action"] != tt.wantAction {
				t.Errorf("action field = %v, want %q", got["action"], tt.wantAction)
			}
		})
	}
}
