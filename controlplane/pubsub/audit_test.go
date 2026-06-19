package pubsub_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/internal/logger"
)

// countLines returns the number of non-empty newline-delimited records in the
// captured logger output. Each accepted Publish emits exactly one audit line,
// so this counts how many times the internal audit hook fired.
func countLines(b []byte) int {
	n := 0
	for _, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
	}
	return n
}

// panicMarshaler is a payload whose MarshalZerologObject panics. The audit hook
// asserts the payload to zerolog.LogObjectMarshaler and embeds it; a panic in
// that embed must be contained by the hook's recover, never reach the producer.
type panicMarshaler struct{}

func (panicMarshaler) MarshalZerologObject(*zerolog.Event) { panic("marshal boom") }

// marshalerPayload carries domain-identity fields surfaced via
// MarshalZerologObject — the canonical shape the audit hook embeds. It
// implements ONLY the marshaler; it deliberately has no EventName/OccurredAt
// identity methods (that legacy contract is gone).
type marshalerPayload struct {
	ContainerID string
	Agent       string
}

func (p marshalerPayload) MarshalZerologObject(e *zerolog.Event) {
	e.Str("container_id", p.ContainerID).Str("agent", p.Agent)
}

// publishOne drives a real pubsub.Topic[T] (whose NewTopic self-attaches the
// single internal audit hook), publishes one event through it, and returns the
// single JSON audit line. The hook runs synchronously in the producer goroutine
// during Publish, so the buffer is fully written once Publish returns.
func publishOne[T any](t *testing.T, source string, ts int64, payload T) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	top, err := pubsub.NewTopic[T](logger.NewWriter(&buf))
	require.NoError(t, err)
	t.Cleanup(func() { _ = top.Close() })

	ok := top.Publish(pubsub.Event[T]{Source: source, Timestamp: ts, Payload: payload})
	require.True(t, ok, "publish must be accepted")

	line := bytes.TrimSpace(buf.Bytes())
	require.NotEmpty(t, line, "audit hook must emit exactly one log line")

	var got map[string]any
	require.NoError(t, json.Unmarshal(line, &got), "log line must be valid JSON: %s", line)
	return got
}

// TestAuditHook_KeysOnEnvelope proves the audit line keys on the ENVELOPE
// (Source + Timestamp), not on any payload identity method, and embeds the
// payload's MarshalZerologObject fields. This is the load-bearing audit
// contract the orchestrator no longer hand-wires: NewTopic attaches it. A
// regression that re-introduced a payload-identity assertion or dropped the
// marshaler embed would change these fields.
func TestAuditHook_KeysOnEnvelope(t *testing.T) {
	t.Parallel()

	ts := time.Unix(0, 1_700_000_000_000_000_000).UTC()
	got := publishOne(t, "firewall", ts.UnixNano(), marshalerPayload{
		ContainerID: "container-abc",
		Agent:       "worker",
	})

	assert.Equal(t, "firewall", got["source"], "source is the envelope Source")
	assert.Equal(t, "firewall", got["message"], "message is keyed on the envelope Source")
	require.Contains(t, got, "timestamp", "timestamp is the envelope Timestamp")

	// The envelope Timestamp (UnixNano) is stamped as the audit line timestamp.
	parsed, err := time.Parse(time.RFC3339Nano, got["timestamp"].(string))
	require.NoError(t, err, "timestamp must be RFC3339Nano")
	assert.True(t, parsed.Equal(ts), "timestamp must equal the envelope Timestamp")

	// Embedded LogObjectMarshaler fields prove the marshaler assertion fired.
	assert.Equal(t, "container-abc", got["container_id"])
	assert.Equal(t, "worker", got["agent"])

	// No legacy identity fields survive.
	assert.NotContains(t, got, "event", "no payload-identity event= field")
	assert.NotContains(t, got, "occurred_at", "no payload OccurredAt field")
}

// TestAuditHook_PlainPayloadFallsBackToSource covers the fallback branch: a
// payload that does NOT implement zerolog.LogObjectMarshaler still produces a
// Source-keyed line with no embedded fields.
func TestAuditHook_PlainPayloadFallsBackToSource(t *testing.T) {
	t.Parallel()

	got := publishOne(t, "plain-source", time.Now().UnixNano(), fooEvent{Action: "noop", N: 1})

	assert.Equal(t, "plain-source", got["message"],
		"message falls back to Source when payload is not a marshaler")
	assert.Equal(t, "plain-source", got["source"])
	assert.NotContains(t, got, "container_id", "no embedded fields for a non-marshaler payload")
}
