package logger

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"
)

// captureLogger is an in-memory log.Logger that records every Emit
// for assertion. Embeds embedded.Logger to satisfy the forward-compat
// hook on the OTEL Logger interface.
type captureLogger struct {
	embedded.Logger
	mu      sync.Mutex
	records []log.Record
}

func (c *captureLogger) Emit(_ context.Context, r log.Record) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
}

func (c *captureLogger) Enabled(_ context.Context, _ log.EnabledParameters) bool {
	return true
}

func (c *captureLogger) get() []log.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]log.Record, len(c.records))
	copy(out, c.records)
	return out
}

// findAttr searches a record's attributes by key.
func findAttr(t *testing.T, r log.Record, key string) (log.Value, bool) {
	t.Helper()
	var got log.Value
	var found bool
	r.WalkAttributes(func(kv log.KeyValue) bool {
		if kv.Key == key {
			got = kv.Value
			found = true
			return false
		}
		return true
	})
	return got, found
}

// emit pumps one zerolog-shaped JSON line through the writer and
// returns the captured Record.
func emit(t *testing.T, payload map[string]any) log.Record {
	t.Helper()
	cap := &captureLogger{}
	w := newOtelLogWriter(cap)
	b, err := json.Marshal(payload)
	require.NoError(t, err)
	n, err := w.Write(b)
	require.NoError(t, err)
	require.Equal(t, len(b), n)
	got := cap.get()
	require.Len(t, got, 1)
	return got[0]
}

// TestOtelWriter_BodyAndSeverityAndTime — the three load-bearing
// projections from a zerolog record onto an OTEL log Record.
func TestOtelWriter_BodyAndSeverityAndTime(t *testing.T) {
	r := emit(t, map[string]any{
		"level":   "warn",
		"message": "container event received",
		"time":    "2026-04-25T10:00:00Z",
		"id":      "ctr1",
	})
	require.Equal(t, "container event received", r.Body().AsString())
	require.Equal(t, log.SeverityWarn, r.Severity())
	require.Equal(t, "warn", r.SeverityText())
	require.Equal(t, time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC), r.Timestamp().UTC())
	id, ok := findAttr(t, r, "id")
	require.True(t, ok)
	require.Equal(t, "ctr1", id.AsString())
}

// TestOtelWriter_LevelMapping — every zerolog level lands on its OTEL
// counterpart. Regression in the upstream-mirrored mapping is the
// reason this test exists.
func TestOtelWriter_LevelMapping(t *testing.T) {
	cases := []struct {
		zerologLevel string
		want         log.Severity
	}{
		{"trace", log.SeverityUndefined}, // trace not modelled — falls through
		{"debug", log.SeverityDebug},
		{"info", log.SeverityInfo},
		{"warn", log.SeverityWarn},
		{"error", log.SeverityError},
		{"fatal", log.SeverityFatal2},
		{"panic", log.SeverityFatal1},
	}
	for _, tc := range cases {
		t.Run(tc.zerologLevel, func(t *testing.T) {
			r := emit(t, map[string]any{
				"level":   tc.zerologLevel,
				"message": "x",
			})
			require.Equal(t, tc.want, r.Severity())
		})
	}
}

// TestOtelWriter_TimestampShapes — supports RFC3339, RFC3339Nano, and
// numeric epoch seconds (zerolog's TimeFieldFormat options).
func TestOtelWriter_TimestampShapes(t *testing.T) {
	t.Run("rfc3339", func(t *testing.T) {
		r := emit(t, map[string]any{"level": "info", "time": "2026-04-25T10:00:00Z", "message": "x"})
		require.Equal(t, time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC), r.Timestamp().UTC())
	})
	t.Run("rfc3339nano", func(t *testing.T) {
		r := emit(t, map[string]any{"level": "info", "time": "2026-04-25T10:00:00.123456789Z", "message": "x"})
		require.Equal(t, 123456789, r.Timestamp().UTC().Nanosecond())
	})
	t.Run("float epoch", func(t *testing.T) {
		r := emit(t, map[string]any{"level": "info", "time": float64(1700000000.5), "message": "x"})
		got := r.Timestamp().UTC()
		require.Equal(t, int64(1700000000), got.Unix())
		require.Equal(t, 500000000, got.Nanosecond())
	})
}

// TestOtelWriter_TypePromotion — the writer promotes JSON-decoded
// float64 to int64 when integral so attribute consumers see the
// natural numeric type. The point of the writer over the upstream
// bridge is field fidelity; a regression that drops Int64 would lie
// about port numbers in Loki.
func TestOtelWriter_TypePromotion(t *testing.T) {
	r := emit(t, map[string]any{
		"level":   "info",
		"message": "x",
		"port":    float64(4319),            // integral → int64
		"weight":  float64(0.5),             // non-integral → float64
		"flag":    true,                     // bool
		"name":    "alpine",                 // string
		"tags":    []any{"a", "b"},          // slice → JSON-encoded string
		"meta":    map[string]any{"k": "v"}, // map → JSON-encoded string
	})

	port, ok := findAttr(t, r, "port")
	require.True(t, ok)
	require.Equal(t, int64(4319), port.AsInt64())

	weight, ok := findAttr(t, r, "weight")
	require.True(t, ok)
	require.InDelta(t, 0.5, weight.AsFloat64(), 0.0001)

	flag, ok := findAttr(t, r, "flag")
	require.True(t, ok)
	require.Equal(t, true, flag.AsBool())

	name, ok := findAttr(t, r, "name")
	require.True(t, ok)
	require.Equal(t, "alpine", name.AsString())

	tags, ok := findAttr(t, r, "tags")
	require.True(t, ok)
	require.Equal(t, `["a","b"]`, tags.AsString())

	meta, ok := findAttr(t, r, "meta")
	require.True(t, ok)
	require.Equal(t, `{"k":"v"}`, meta.AsString())
}

// TestOtelWriter_MessageAlias — zerolog's MessageFieldName defaults to
// "message" but some configs use "msg". Currently we honour
// MessageFieldName as set on the package; assert "message" is the
// default.
func TestOtelWriter_MessageAlias(t *testing.T) {
	r := emit(t, map[string]any{"level": "info", "message": "via message"})
	require.Equal(t, "via message", r.Body().AsString())
}

// TestOtelWriter_MalformedJsonReturnsLengthAndDoesNotEmit — zerolog
// must not see a partial-write situation; the writer drops malformed
// records entirely. The OTEL SDK error handler captures the failure.
func TestOtelWriter_MalformedJsonReturnsLengthAndDoesNotEmit(t *testing.T) {
	cap := &captureLogger{}
	w := newOtelLogWriter(cap)
	bad := []byte(`{not valid json`)
	n, err := w.Write(bad)
	require.NoError(t, err)
	require.Equal(t, len(bad), n)
	require.Empty(t, cap.get(), "malformed JSON must not produce a Record")
}
